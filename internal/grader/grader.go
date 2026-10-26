package grader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"

	"github.com/cauebraz/kubedrill/internal/kube"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/scriptrun"
)

// Kube is what the grader needs from a cluster client; *kube.Client satisfies it.
type Kube interface {
	Get(ctx context.Context, ref scenario.ResourceRef) (*unstructured.Unstructured, error)
	List(ctx context.Context, ref scenario.ResourceRef) (*unstructured.UnstructuredList, error)
	RunProbe(ctx context.Context, ns string, probe scenario.Probe) (kube.ProbeResult, error)
}

// ScriptRunner executes a script check and returns its exit code and combined output.
type ScriptRunner func(ctx context.Context, script string, env []string) (exitCode int, output string, err error)

// EnvFunc returns extra KEY=VALUE pairs for the scripts of one rendered
// scenario, so script checks see the same TASK_NS, SEED, VARIANT, VAR_* and
// KUBECONFIG as the setup hook and the solution.
type EnvFunc func(rd *scenario.Rendered) []string

// Option configures a Runner.
type Option func(*Runner)

// DefaultCheckTimeout bounds one check when WithCheckTimeout is not given.
const DefaultCheckTimeout = 30 * time.Second

const (
	maxTextLen = 200
	probeGrace = 30 * time.Second
)

// WithCheckTimeout sets the time budget of each check.
func WithCheckTimeout(d time.Duration) Option {
	return func(r *Runner) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// WithScriptRunner replaces the default runner of script checks, which runs
// bash -c through scriptrun.Run in the scenario directory and keeps at most
// scriptrun.DefaultLimit bytes of output.
func WithScriptRunner(fn ScriptRunner) Option {
	return func(r *Runner) {
		if fn != nil {
			r.script = fn
		}
	}
}

// WithEnv appends KEY=VALUE pairs to the environment of script checks.
func WithEnv(env []string) Option {
	return func(r *Runner) {
		r.env = append(r.env, env...)
	}
}

// WithEnvFunc adds per-scenario KEY=VALUE pairs to the environment of script
// checks, after the pairs given with WithEnv.
func WithEnvFunc(fn EnvFunc) Option {
	return func(r *Runner) {
		r.envFunc = fn
	}
}

// Runner grades rendered scenarios against one cluster.
type Runner struct {
	k          Kube
	timeout    time.Duration
	script     ScriptRunner
	env        []string
	envFunc    EnvFunc
	pathPrefix string
}

// New returns a Runner that reads the cluster through k.
func New(k Kube, opts ...Option) *Runner {
	r := &Runner{k: k, timeout: DefaultCheckTimeout}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Grade runs every check of rd in dependency order and aggregates the results.
// It returns an error only for a nil rendered scenario or a check graph that
// is not a DAG; anything that happens while checking is a Result with
// StatusError.
func (r *Runner) Grade(ctx context.Context, rd *scenario.Rendered) (Grade, error) {
	if rd == nil {
		return Grade{}, errors.New("grade: nil rendered scenario")
	}
	order, err := topoOrder(rd.Checks)
	if err != nil {
		return Grade{}, fmt.Errorf("grade: %w", err)
	}

	status := make(map[string]Status, len(order))
	results := make([]Result, 0, len(order))
	for _, c := range order {
		res := Result{ID: c.ID, Description: c.Description, Why: c.Why, Critical: c.Critical, Weight: c.Weight, Spoiler: c.Spoiler}
		if dep, ok := failedDependency(c, status); ok {
			res.Status = StatusSkipped
			res.Message = fmt.Sprintf("skipped: %s did not pass", dep)
		} else {
			start := time.Now()
			r.runCheck(ctx, rd, c, &res)
			res.Duration = time.Since(start)
		}
		status[c.ID] = res.Status
		results = append(results, res)
	}

	g := Grade{Results: results, At: time.Now(), Passed: len(results) > 0}
	var total, passed float64
	critical := false
	for _, res := range results {
		w := res.Weight
		if w <= 0 {
			w = 1
		}
		total += w
		if res.Status == StatusPass {
			passed += w
		} else {
			g.Passed = false
			if res.Critical {
				critical = true
			}
		}
	}
	if total > 0 {
		g.Score = passed / total
	}
	if critical {
		g.Score = 0
	}
	return g, nil
}

func failedDependency(c scenario.Check, status map[string]Status) (string, bool) {
	for _, dep := range c.DependsOn {
		if status[dep] != StatusPass {
			return dep, true
		}
	}
	return "", false
}

func topoOrder(checks []scenario.Check) ([]scenario.Check, error) {
	index := make(map[string]int, len(checks))
	for i, c := range checks {
		if _, dup := index[c.ID]; dup {
			return nil, fmt.Errorf("duplicate check id %q", c.ID)
		}
		index[c.ID] = i
	}
	indegree := make([]int, len(checks))
	dependents := make([][]int, len(checks))
	for i, c := range checks {
		for _, dep := range c.DependsOn {
			j, ok := index[dep]
			if !ok {
				return nil, fmt.Errorf("check %q depends on unknown check %q", c.ID, dep)
			}
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}
	done := make([]bool, len(checks))
	out := make([]scenario.Check, 0, len(checks))
	for len(out) < len(checks) {
		next := -1
		for i := range checks {
			if !done[i] && indegree[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			return nil, errors.New("check dependencies form a cycle")
		}
		done[next] = true
		out = append(out, checks[next])
		for _, d := range dependents[next] {
			indegree[d]--
		}
	}
	return out, nil
}

type extracted struct {
	value    string
	values   []string
	exitCode *int
}

func (r *Runner) runCheck(ctx context.Context, rd *scenario.Rendered, c scenario.Check, res *Result) {
	ctx, cancel := context.WithTimeout(ctx, r.checkTimeout(c))
	defer cancel()

	var ex extracted
	switch {
	case c.Resource != nil:
		done := r.extractResource(ctx, rd, c, res, &ex)
		if done {
			return
		}
	case c.Exists != nil && c.Resource == nil:
		setError(res, "exists applies to a resource source only")
		return
	case c.Probe != nil:
		pr, err := r.k.RunProbe(ctx, rd.Namespace, *c.Probe)
		if err != nil {
			setError(res, fmt.Sprintf("probe %s: %v", c.Probe.Image, err))
			return
		}
		code := pr.ExitCode
		ex = extracted{value: strings.TrimSpace(pr.Output), exitCode: &code}
		if ex.value != "" {
			ex.values = []string{ex.value}
		}
	case c.Script != "":
		code, out, err := r.runScript(ctx, rd, c.Script)
		if err != nil {
			setError(res, fmt.Sprintf("script: %v", err))
			return
		}
		ex = extracted{value: strings.TrimSpace(out), exitCode: &code}
		if ex.value != "" {
			ex.values = []string{ex.value}
		}
	default:
		setError(res, "check has no source: set resource, probe or script")
		return
	}
	validate(c, ex, res)
}

// checkTimeout is the time budget of one check: the configured timeout, or
// for a probe the probe's own timeout (kube.ProbeTimeout) plus the pod
// scheduling grace when that is longer. A probe timeout that does not parse
// keeps the configured timeout; RunProbe reports the parse error.
func (r *Runner) checkTimeout(c scenario.Check) time.Duration {
	if c.Probe == nil {
		return r.timeout
	}
	d, err := kube.ProbeTimeout(*c.Probe)
	if err != nil {
		return r.timeout
	}
	return max(r.timeout, d+probeGrace)
}

func (r *Runner) runScript(ctx context.Context, rd *scenario.Rendered, script string) (int, string, error) {
	env := append([]string(nil), r.env...)
	if r.envFunc != nil {
		env = append(env, r.envFunc(rd)...)
	}
	if r.pathPrefix != "" {
		env = prefixPath(env, r.pathPrefix)
	}
	if r.script != nil {
		return r.script(ctx, script, env)
	}
	dir := ""
	if rd.Scenario != nil {
		dir = rd.Scenario.Dir
	}
	return runBash(ctx, dir, script, env)
}

func (r *Runner) extractResource(ctx context.Context, rd *scenario.Rendered, c scenario.Check, res *Result, ex *extracted) bool {
	ref := *c.Resource
	if ref.Namespace == "" {
		ref.Namespace = rd.Namespace
	}
	if c.Exists != nil && c.Path != "" {
		setError(res, "exists applies to a resource without path; use not_empty for a path")
		return true
	}
	wantAbsent := c.Exists != nil && !*c.Exists
	var content map[string]any
	if ref.Selector != "" {
		list, err := r.k.List(ctx, ref)
		if err != nil {
			setError(res, fmt.Sprintf("list %s %s: %v", ref.Kind, ref.Selector, truncate(err.Error())))
			return true
		}
		n := 0
		if list != nil {
			n = len(list.Items)
		}
		if c.Exists != nil {
			if wantAbsent && n > 0 {
				setFail(res, "no matching objects", fmt.Sprintf("%d matching objects", n))
				return true
			}
			if !wantAbsent && n == 0 {
				setFail(res, "at least one matching object", "0 matching objects")
				return true
			}
		}
		if list == nil {
			list = &unstructured.UnstructuredList{}
		}
		content = list.UnstructuredContent()
	} else {
		obj, err := r.k.Get(ctx, ref)
		switch {
		case apierrors.IsNotFound(err):
			if wantAbsent {
				res.Status = StatusPass
				res.Expected = "absent"
				res.Actual = "not found"
			} else {
				setFail(res, "exists", "not found")
			}
			return true
		case err != nil:
			setError(res, fmt.Sprintf("get %s %s/%s: %v", ref.Kind, ref.Namespace, ref.Name, truncate(err.Error())))
			return true
		case wantAbsent:
			setFail(res, "absent", "exists")
			return true
		}
		if obj == nil {
			setError(res, fmt.Sprintf("get %s %s/%s: nil object", ref.Kind, ref.Namespace, ref.Name))
			return true
		}
		content = obj.Object
	}
	if c.Path == "" {
		return false
	}
	values, err := evalJSONPath(c.ID, c.Path, content)
	if err != nil {
		setError(res, fmt.Sprintf("jsonpath %s: %v", c.Path, truncate(err.Error())))
		return true
	}
	ex.values = values
	ex.value = strings.Join(values, " ")
	return false
}

func evalJSONPath(id, path string, content map[string]any) ([]string, error) {
	jp := jsonpath.New(id).AllowMissingKeys(true)
	if err := jp.Parse(path); err != nil {
		return nil, err
	}
	results, err := jp.FindResults(content)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, group := range results {
		for _, v := range group {
			if s, ok := valueText(v); ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func valueText(v reflect.Value) (string, bool) {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return "", false
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), true
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		b, err := json.Marshal(v.Interface())
		if err != nil {
			return fmt.Sprint(v.Interface()), true
		}
		return string(b), true
	default:
		return fmt.Sprint(v.Interface()), true
	}
}

func validate(c scenario.Check, ex extracted, res *Result) {
	value := strings.TrimSpace(ex.value)
	fromPath := c.Resource != nil && c.Path != ""
	actual := func() string {
		if len(ex.values) == 0 && fromPath {
			return "no value at " + c.Path
		}
		if value == "" {
			return `"" (empty)`
		}
		return truncate(value)
	}

	if c.Equals != nil && value != strings.TrimSpace(*c.Equals) {
		setFail(res, fmt.Sprintf("%q", strings.TrimSpace(*c.Equals)), actual())
		return
	}
	if c.NotEquals != nil && value == strings.TrimSpace(*c.NotEquals) {
		setFail(res, fmt.Sprintf("not %q", strings.TrimSpace(*c.NotEquals)), actual())
		return
	}
	if c.Matches != "" {
		re, err := regexp.Compile(c.Matches)
		if err != nil {
			setError(res, fmt.Sprintf("matches %s: %v", c.Matches, err))
			return
		}
		if !re.MatchString(value) {
			setFail(res, fmt.Sprintf("match /%s/", c.Matches), actual())
			return
		}
	}
	if c.Contains != "" && !strings.Contains(value, c.Contains) {
		setFail(res, fmt.Sprintf("contain %q", c.Contains), actual())
		return
	}
	if c.GTE != nil {
		got, want, err := parsePair(value, *c.GTE, "gte")
		if err != nil {
			setError(res, err.Error())
			return
		}
		if got < want {
			setFail(res, ">= "+strings.TrimSpace(*c.GTE), actual())
			return
		}
	}
	if c.LTE != nil {
		got, want, err := parsePair(value, *c.LTE, "lte")
		if err != nil {
			setError(res, err.Error())
			return
		}
		if got > want {
			setFail(res, "<= "+strings.TrimSpace(*c.LTE), actual())
			return
		}
	}
	if c.NotEmpty && (len(ex.values) == 0 || value == "") {
		setFail(res, "at least one value", actual())
		return
	}
	if c.ExitCode != nil {
		if ex.exitCode == nil {
			setError(res, "exit_code applies to probe and script checks only")
			return
		}
		if *ex.exitCode != *c.ExitCode {
			setFail(res, fmt.Sprintf("exit code %d", *c.ExitCode), fmt.Sprintf("exit code %d", *ex.exitCode))
			return
		}
	}
	res.Status = StatusPass
	if ex.exitCode != nil && value == "" {
		res.Actual = fmt.Sprintf("exit code %d", *ex.exitCode)
	} else {
		res.Actual = actual()
	}
}

func parsePair(value, bound, name string) (float64, float64, error) {
	want, err := strconv.ParseFloat(strings.TrimSpace(bound), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: bound %q is not a number", name, bound)
	}
	if math.IsNaN(want) || math.IsInf(want, 0) {
		return 0, 0, fmt.Errorf("%s: bound %q is not a finite number", name, bound)
	}
	got, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: value %q is not a number", name, truncate(value))
	}
	if math.IsNaN(got) || math.IsInf(got, 0) {
		return 0, 0, fmt.Errorf("%s: value %q is not a finite number", name, truncate(value))
	}
	return got, want, nil
}

func setFail(res *Result, expected, actual string) {
	res.Status = StatusFail
	res.Expected = expected
	res.Actual = actual
}

func setError(res *Result, msg string) {
	res.Status = StatusError
	res.Message = truncate(msg)
}

func truncate(s string) string {
	if len(s) <= maxTextLen {
		return s
	}
	cut := maxTextLen - 3
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func runBash(ctx context.Context, dir, script string, env []string) (int, string, error) {
	res, err := scriptrun.Run(ctx, scriptrun.Spec{Script: script, Dir: dir, Env: env})
	if err != nil {
		return -1, res.Output, err
	}
	return res.ExitCode, res.Output, nil
}
