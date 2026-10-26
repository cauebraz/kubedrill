// Package telemetry reads what the learner did during an episode: the API
// server audit log (audit.k8s.io/v1 Event JSON lines) and the workstation
// shell log, and summarises both into journal.Telemetry. It never talks to
// the cluster; the files are written by the API server and the workstation.
package telemetry

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const (
	stageResponseComplete = "ResponseComplete"
	ownUserAgent          = "kubedrill"
	systemUserPrefix      = "system:"
	maxCommands           = 50
	maxLine               = 16 * 1024 * 1024
	preWriteWindow        = time.Second
)

// Verdicts of Summarize.
const (
	VerdictRootCause   = "root-cause"
	VerdictSymptomOnly = "symptom-only"
	VerdictNoWrites    = "no-writes"
	VerdictUnknown     = "unknown"
)

// AuditEvent is the part of one audit.k8s.io/v1 Event the summary uses.
// Group is the API group as recorded, empty for the core group.
type AuditEvent struct {
	Time        time.Time
	Verb        string
	User        string
	UserAgent   string
	Group       string
	Version     string
	Resource    string
	Subresource string
	Namespace   string
	Name        string
	Code        int
}

// ShellCommand is one line of the workstation shell log.
type ShellCommand struct {
	Time       time.Time
	Cmd        string
	Exit       int
	DurationMS int64
}

type auditLine struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Stage      string `json:"stage"`
	RequestURI string `json:"requestURI"`
	Verb       string `json:"verb"`
	User       struct {
		Username string `json:"username"`
	} `json:"user"`
	UserAgent string `json:"userAgent"`
	ObjectRef *struct {
		Resource    string `json:"resource"`
		Namespace   string `json:"namespace"`
		Name        string `json:"name"`
		APIGroup    string `json:"apiGroup"`
		APIVersion  string `json:"apiVersion"`
		Subresource string `json:"subresource"`
	} `json:"objectRef"`
	ResponseStatus *struct {
		Code int `json:"code"`
	} `json:"responseStatus"`
	RequestReceivedTimestamp time.Time `json:"requestReceivedTimestamp"`
	StageTimestamp           time.Time `json:"stageTimestamp"`
}

type shellLine struct {
	TS   time.Time `json:"ts"`
	Cmd  string    `json:"cmd"`
	Exit int       `json:"exit"`
	MS   int64     `json:"ms"`
}

// ReadAudit returns the learner's requests received between since and until
// (inclusive) that touch namespace: objects in it, the namespace object
// itself, and cluster-scoped objects that are nodes, persistentvolumes or one
// of fixTargets (see MatchTarget). It reads path and the rotated siblings
// next to it (audit-*.log) modified at or after since. Only the
// ResponseComplete stage is kept. Requests from kubedrill itself (user agent
// containing "kubedrill", which covers the kubedrill-check kubectl of script
// checks), from system users (controllers, nodes, service accounts) and
// dry-run requests are dropped. Lines that do not decode, such as a partial
// last line while the API server writes, are skipped. A missing path returns
// an error that satisfies errors.Is(err, fs.ErrNotExist): with the audit log
// on, a missing file means the trajectory is unknown, not empty.
func ReadAudit(path string, since, until time.Time, namespace string, fixTargets []string) ([]AuditEvent, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	files := append(rotated(path, since), path)
	var out []AuditEvent
	keep := func(line []byte) {
		var l auditLine
		if json.Unmarshal(line, &l) != nil {
			return
		}
		if l.Kind != "Event" || l.Stage != stageResponseComplete || l.ObjectRef == nil {
			return
		}
		if strings.Contains(l.UserAgent, ownUserAgent) || strings.HasPrefix(l.User.Username, systemUserPrefix) {
			return
		}
		if strings.Contains(l.RequestURI, "dryRun=") {
			return
		}
		ref := l.ObjectRef
		if namespace == "" {
			return
		}
		inNamespace := ref.Namespace == namespace
		isNamespace := ref.APIGroup == "" && ref.Resource == "namespaces" && ref.Name == namespace
		if !inNamespace && !isNamespace && !(ref.Namespace == "" && clusterScopedKept(ref.APIGroup, ref.Resource, ref.Name, fixTargets)) {
			return
		}
		at := l.RequestReceivedTimestamp
		if at.IsZero() {
			at = l.StageTimestamp
		}
		if at.Before(since) || at.After(until) {
			return
		}
		e := AuditEvent{
			Time:        at,
			Verb:        l.Verb,
			User:        l.User.Username,
			UserAgent:   l.UserAgent,
			Group:       ref.APIGroup,
			Version:     ref.APIVersion,
			Resource:    ref.Resource,
			Subresource: ref.Subresource,
			Namespace:   ref.Namespace,
			Name:        ref.Name,
		}
		if l.ResponseStatus != nil {
			e.Code = l.ResponseStatus.Code
		}
		out = append(out, e)
	}
	for _, f := range files {
		if err := scanLines(f, keep); err != nil {
			return nil, fmt.Errorf("read audit log: %w", err)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// rotated lists the backups the API server keeps next to path
// (<base>-<timestamp><ext>) modified at or after since, oldest first.
func rotated(path string, since time.Time) []string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), base+"-*"+ext))
	if err != nil {
		return nil
	}
	type file struct {
		path string
		mod  time.Time
	}
	var found []file
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() || info.ModTime().Before(since) {
			continue
		}
		found = append(found, file{m, info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.Before(found[j].mod) })
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.path)
	}
	return out
}

func clusterScopedKept(group, resource, name string, fixTargets []string) bool {
	if group == "" && (resource == "nodes" || resource == "persistentvolumes") {
		return true
	}
	return MatchTarget(Target(AuditEvent{Group: group, Resource: resource, Name: name}), fixTargets)
}

// MatchTarget reports whether target (group/resource/name) equals one of
// fixTargets after normalization: lowercase, surrounding space trimmed, an
// empty group read as "core", then parsed with scenario.ParseFixTarget so
// both sides name the core group the same way.
func MatchTarget(target string, fixTargets []string) bool {
	t := normalizeTarget(target)
	for _, ft := range fixTargets {
		if normalizeTarget(ft) == t {
			return true
		}
	}
	return false
}

// ReadShellLog returns the shell commands in path run between since and
// until (inclusive), oldest first. Each line is
// {"ts":"<RFC3339Nano>","cmd":"...","exit":0,"ms":12}; lines that do not
// decode are skipped. A missing file returns nil, nil.
func ReadShellLog(path string, since, until time.Time) ([]ShellCommand, error) {
	var out []ShellCommand
	err := scanLines(path, func(line []byte) {
		var l shellLine
		if json.Unmarshal(line, &l) != nil || l.TS.IsZero() || strings.TrimSpace(l.Cmd) == "" {
			return
		}
		if l.TS.Before(since) || l.TS.After(until) {
			return
		}
		out = append(out, ShellCommand{Time: l.TS, Cmd: l.Cmd, Exit: l.Exit, DurationMS: l.MS})
	})
	if err != nil {
		return nil, fmt.Errorf("read shell log: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// IsWrite reports whether verb changes state: create, update, patch, delete
// or deletecollection.
func IsWrite(verb string) bool {
	switch verb {
	case "create", "update", "patch", "delete", "deletecollection":
		return true
	}
	return false
}

// Summarize builds the episode telemetry. Reads and writes come from the
// audit events; a write counts only when it succeeded (code below 400) and
// is not a connection subresource (exec, attach, portforward, proxy), which
// counts as a read. A get of the same object from the same user agent
// less than one second before a write is the client's own read before it
// writes (kubectl set image, edit, patch, scale) and counts neither as a
// read nor as an observation before the first write. The verdict is
// "unknown" without an audit log (the
// caller passes auditAvailable false when the log is off, missing or
// unreadable),
// "no-writes" when no write succeeded, "root-cause" when any write hit one of
// fixTargets (group/resource/name; the core group is "core" or empty, as
// scenario.ParseFixTarget returns it) and "symptom-only"
// otherwise. Commands lists up to 50 kubectl command lines from the shell
// log, in order.
func Summarize(audit []AuditEvent, auditAvailable bool, shell []ShellCommand, fixTargets []string) journal.Telemetry {
	t := journal.Telemetry{Source: source(auditAvailable, len(shell) > 0), Verdict: VerdictUnknown}
	var firstKubectl *time.Time
	if auditAvailable {
		events := append([]AuditEvent(nil), audit...)
		sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
		hit := false
		own := preWriteReads(events)
		for i, e := range events {
			if firstKubectl == nil {
				at := e.Time
				firstKubectl = &at
			}
			if own[i] {
				continue
			}
			if !countsAsWrite(e) {
				t.Reads++
				if t.FirstWriteAt == nil {
					t.ReadsBeforeFirstWrite++
				}
				continue
			}
			t.Writes++
			target := Target(e)
			if t.FirstWriteAt == nil {
				at := e.Time
				t.FirstWriteAt = &at
				t.FirstWriteTarget = target
			}
			if MatchTarget(target, fixTargets) {
				hit = true
			}
		}
		switch {
		case t.Writes == 0:
			t.Verdict = VerdictNoWrites
		case hit:
			t.Verdict = VerdictRootCause
		default:
			t.Verdict = VerdictSymptomOnly
		}
	}
	for _, c := range shell {
		t.CommandsTotal++
		if c.Exit != 0 {
			t.FailedCommands++
		}
		if !isKubectl(c.Cmd) {
			continue
		}
		t.KubectlCommands++
		if len(t.Commands) < maxCommands {
			t.Commands = append(t.Commands, strings.TrimSpace(c.Cmd))
		}
		if firstKubectl == nil || c.Time.Before(*firstKubectl) {
			at := c.Time
			firstKubectl = &at
		}
	}
	t.FirstKubectlAt = firstKubectl
	return t
}

// Target names the object of e as group/resource/name, with the core group
// written "core".
func Target(e AuditEvent) string {
	group := e.Group
	if group == "" {
		group = "core"
	}
	return strings.ToLower(group + "/" + e.Resource + "/" + e.Name)
}

// preWriteReads marks, for every write in events (sorted by time), the
// nearest preceding get of the same object by the same user agent less than
// preWriteWindow before it: the client's own read before it writes. Only
// that one get is marked; an earlier get within the window is the learner
// looking.
func preWriteReads(events []AuditEvent) map[int]bool {
	own := map[int]bool{}
	for i, w := range events {
		if !IsWrite(w.Verb) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			r := events[j]
			if w.Time.Sub(r.Time) >= preWriteWindow {
				break
			}
			if r.Verb == "get" && r.Subresource == "" && r.UserAgent == w.UserAgent && r.Group == w.Group &&
				r.Resource == w.Resource && r.Namespace == w.Namespace && r.Name == w.Name && r.Name != "" {
				own[j] = true
				break
			}
		}
	}
	return own
}

func normalizeTarget(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "/") {
		s = "core" + s
	}
	group, resource, name, err := scenario.ParseFixTarget(s)
	if err != nil {
		return s
	}
	if group == "" {
		group = "core"
	}
	return group + "/" + resource + "/" + name
}

func countsAsWrite(e AuditEvent) bool {
	if !IsWrite(e.Verb) || e.Code >= 400 {
		return false
	}
	switch e.Subresource {
	case "exec", "attach", "portforward", "proxy":
		return false
	}
	return true
}

func source(audit, shell bool) string {
	switch {
	case audit && shell:
		return "audit+shell"
	case audit:
		return "audit"
	case shell:
		return "shell"
	}
	return "none"
}

func isKubectl(cmd string) bool {
	fields := strings.Fields(cmd)
	for len(fields) > 0 && (strings.Contains(fields[0], "=") || fields[0] == "sudo" || fields[0] == "command") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	name := fields[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name == "kubectl" || name == "k"
}

func scanLines(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		fn(line)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	return nil
}
