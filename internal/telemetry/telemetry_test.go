package telemetry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
)

const fixtureNS = "task-hello-ab12c"

func at(clock string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, "2026-10-25T"+clock+"Z")
	if err != nil {
		panic(err)
	}
	return t
}

func describe(events []AuditEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, fmt.Sprintf("%s %s %d", e.Verb, Target(e), e.Code))
	}
	return out
}

func TestReadAudit(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		since     time.Time
		until     time.Time
		namespace string
		want      []string
	}{
		{
			name:      "learner requests in the namespace and the namespace object",
			file:      "audit.log",
			since:     at("10:00:00"),
			until:     at("10:30:00"),
			namespace: fixtureNS,
			want: []string{
				"list core/pods/ 200",
				"get core/pods/hello-7d9f8b6c5-x2x9q 200",
				"list core/events/ 200",
				"get core/namespaces/task-hello-ab12c 200",
				"get core/pods/hello-7d9f8b6c5-x2x9q 200",
				"delete core/pods/hello-7d9f8b6c5-x2x9q 200",
				"patch apps/deployments/hello 200",
				"update apps/deployments/hello 409",
				"create core/pods/hello-7d9f8b6c5-ab3cd 101",
			},
		},
		{
			name:      "window excludes earlier and later events",
			file:      "audit.log",
			since:     at("10:00:30"),
			until:     at("10:01:30"),
			namespace: fixtureNS,
			want: []string{
				"get core/pods/hello-7d9f8b6c5-x2x9q 200",
				"delete core/pods/hello-7d9f8b6c5-x2x9q 200",
			},
		},
		{
			name:      "late event inside a wider window",
			file:      "audit.log",
			since:     at("11:00:00"),
			until:     at("12:00:00"),
			namespace: fixtureNS,
			want:      []string{"get apps/deployments/hello 200"},
		},
		{
			name:      "other namespace",
			file:      "audit.log",
			since:     at("10:00:00"),
			until:     at("10:30:00"),
			namespace: "default",
			want:      []string{"list core/pods/ 200"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := ReadAudit(filepath.Join("testdata", tc.file), tc.since, tc.until, tc.namespace, nil)
			if err != nil {
				t.Fatalf("ReadAudit: %v", err)
			}
			if got := describe(events); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("events:\ngot  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestReadAuditFields(t *testing.T) {
	events, err := ReadAudit(filepath.Join("testdata", "audit.log"), at("10:00:00"), at("10:30:00"), fixtureNS, nil)
	if err != nil || len(events) == 0 {
		t.Fatalf("ReadAudit: got %d events, %v", len(events), err)
	}
	logs := events[4]
	want := AuditEvent{
		Time:        at("10:00:40.1"),
		Verb:        "get",
		User:        "kubernetes-admin",
		UserAgent:   "kubectl/v1.33.1 (linux/arm64) kubernetes/8adc0f0",
		Group:       "",
		Version:     "v1",
		Resource:    "pods",
		Subresource: "log",
		Namespace:   fixtureNS,
		Name:        "hello-7d9f8b6c5-x2x9q",
		Code:        200,
	}
	if !reflect.DeepEqual(logs, want) {
		t.Errorf("event 4:\ngot  %+v\nwant %+v", logs, want)
	}
}

func TestReadAuditReadsRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	rotated, err := os.ReadFile(filepath.Join("testdata", "rotated", "audit-2026-10-25T09-00-00.000.log"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "audit-2026-10-25T09-00-00.000.log")
	stale := filepath.Join(dir, "audit-2026-10-24T09-00-00.000.log")
	for _, p := range []string{fresh, stale} {
		if err := os.WriteFile(p, rotated, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(fresh, at("09:30:00"), at("09:30:00")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, at("08:00:00"), at("08:00:00")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := ReadAudit(filepath.Join(dir, "audit.log"), at("09:00:00"), at("11:00:00"), fixtureNS, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(events); len(got) != 1 {
		t.Errorf("ReadAudit with a fresh and a stale rotated file: got %q, want the one event of the fresh file", got)
	}
	if err := os.Chtimes(fresh, at("08:30:00"), at("08:30:00")); err != nil {
		t.Fatal(err)
	}
	events, err = ReadAudit(filepath.Join(dir, "audit.log"), at("09:00:00"), at("11:00:00"), fixtureNS, nil)
	if err != nil || len(events) != 0 {
		t.Errorf("ReadAudit with rotated files modified before since: got %q, %v, want none", describe(events), err)
	}
}

func TestReadAuditMissing(t *testing.T) {
	_, err := ReadAudit(filepath.Join(t.TempDir(), "audit.log"), at("10:00:00"), at("11:00:00"), fixtureNS, nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadAudit on a missing file: got %v, want fs.ErrNotExist", err)
	}
}

func auditJSON(ns, group, resource, name, verb, ua string) string {
	return fmt.Sprintf(`{"kind":"Event","apiVersion":"audit.k8s.io/v1","stage":"ResponseComplete","verb":%q,"user":{"username":"kubernetes-admin"},"userAgent":%q,"objectRef":{"resource":%q,"namespace":%q,"name":%q,"apiGroup":%q},"responseStatus":{"code":200},"requestReceivedTimestamp":"2026-10-25T10:00:05Z"}`, verb, ua, resource, ns, name, group)
}

func TestReadAuditClusterScoped(t *testing.T) {
	lines := []string{
		auditJSON("", "", "nodes", "kubedrill-worker", "patch", "kubectl/v1.37.0"),
		auditJSON("", "", "persistentvolumes", "pv-1", "get", "kubectl/v1.37.0"),
		auditJSON("", "storage.k8s.io", "storageclasses", "fast", "patch", "kubectl/v1.37.0"),
		auditJSON("", "rbac.authorization.k8s.io", "clusterroles", "view", "get", "kubectl/v1.37.0"),
		auditJSON("", "", "nodes", "kubedrill-worker", "get", "kubedrill-check/v1.37.0 (linux/arm64)"),
		auditJSON("other", "", "pods", "p", "get", "kubectl/v1.37.0"),
	}
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := ReadAudit(path, at("10:00:00"), at("10:30:00"), fixtureNS, []string{"Storage.k8s.io/StorageClasses/fast"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"patch core/nodes/kubedrill-worker 200", "get core/persistentvolumes/pv-1 200", "patch storage.k8s.io/storageclasses/fast 200"}
	if got := describe(events); !reflect.DeepEqual(got, want) {
		t.Errorf("cluster-scoped events:\ngot  %q\nwant %q", got, want)
	}
	if s := Summarize(events, true, nil, []string{"storage.k8s.io/storageclasses/fast"}); s.Verdict != journal.VerdictRootCause {
		t.Errorf("verdict with a cluster-scoped fix target write: got %q, want %q", s.Verdict, journal.VerdictRootCause)
	}
}

func TestMatchTarget(t *testing.T) {
	tests := []struct {
		target  string
		targets []string
		want    bool
	}{
		{"core/configmaps/app", []string{"/configmaps/app"}, true},
		{"/configmaps/app", []string{"core/configmaps/app"}, true},
		{"apps/deployments/hello", []string{" Apps/Deployments/Hello "}, true},
		{"apps/deployments/hello", []string{"apps/deployments/other", "core/pods/hello"}, false},
		{"apps/deployments/hello", nil, false},
	}
	for _, tt := range tests {
		if got := MatchTarget(tt.target, tt.targets); got != tt.want {
			t.Errorf("MatchTarget(%q, %q): got %v, want %v", tt.target, tt.targets, got, tt.want)
		}
	}
}

func TestReadAuditUnreadable(t *testing.T) {
	_, err := ReadAudit(t.TempDir(), at("10:00:00"), at("11:00:00"), fixtureNS, nil)
	if err == nil || !strings.Contains(err.Error(), "read audit log") {
		t.Errorf("ReadAudit on a directory: got %v, want a read error", err)
	}
}

func TestReadShellLog(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		since time.Time
		until time.Time
		want  []string
	}{
		{
			name:  "window and partial last line",
			file:  "commands.jsonl",
			since: at("10:00:00"),
			until: at("10:30:00"),
			want: []string{
				"kubectl get pods",
				"k describe pod hello-7d9f8b6c5-x2x9q",
				"cat /etc/hosts",
				"kubectl logs hello-7d9f8b6c5-x2x9q | grep error",
				"kubectl set image deployment/hello hello=nginx:1.27.2",
			},
		},
		{name: "empty window", file: "commands.jsonl", since: at("12:00:00"), until: at("13:00:00"), want: []string{}},
		{name: "missing file", file: "absent.jsonl", since: at("10:00:00"), until: at("13:00:00"), want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmds, err := ReadShellLog(filepath.Join("testdata", tc.file), tc.since, tc.until)
			if err != nil {
				t.Fatalf("ReadShellLog: %v", err)
			}
			got := []string{}
			for _, c := range cmds {
				got = append(got, c.Cmd)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("commands:\ngot  %q\nwant %q", got, tc.want)
			}
		})
	}
	cmds, _ := ReadShellLog(filepath.Join("testdata", "commands.jsonl"), at("10:00:00"), at("10:30:00"))
	want := ShellCommand{Time: at("10:00:39"), Cmd: "kubectl logs hello-7d9f8b6c5-x2x9q | grep error", Exit: 1, DurationMS: 150}
	if len(cmds) < 4 || !reflect.DeepEqual(cmds[3], want) {
		t.Errorf("command 3: got %+v, want %+v", cmds, want)
	}
}

func TestIsWrite(t *testing.T) {
	for verb, want := range map[string]bool{
		"create": true, "update": true, "patch": true, "delete": true, "deletecollection": true,
		"get": false, "list": false, "watch": false, "": false, "PATCH": false,
	} {
		if got := IsWrite(verb); got != want {
			t.Errorf("IsWrite(%q): got %v, want %v", verb, got, want)
		}
	}
}

func ptr(t time.Time) *time.Time { return &t }

func TestSummarizeFixtures(t *testing.T) {
	window := [2]time.Time{at("10:00:00"), at("10:30:00")}
	shell, err := ReadShellLog(filepath.Join("testdata", "commands.jsonl"), window[0], window[1])
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		file      string
		available bool
		shell     []ShellCommand
		targets   []string
		check     func(t *testing.T, got summary)
	}{
		{
			name: "root cause after reads", file: "audit.log", available: true, shell: shell,
			targets: []string{"apps/deployments/hello"},
			check: func(t *testing.T, got summary) {
				want := summary{
					Source: "audit+shell", Verdict: journal.VerdictRootCause, Reads: 7, Writes: 2, ReadsBeforeFirstWrite: 5,
					FirstWriteTarget: "core/pods/hello-7d9f8b6c5-x2x9q", FirstWriteAt: ptr(at("10:01:00.1")),
					FirstKubectlAt: ptr(at("10:00:04.512345678")), CommandsTotal: 5, KubectlCommands: 4, FailedCommands: 1,
				}
				want.compare(t, got)
			},
		},
		{
			name: "symptom only", file: "symptom.log", available: true, shell: nil,
			targets: []string{"apps/deployments/hello", "core/configmaps/hello"},
			check: func(t *testing.T, got summary) {
				want := summary{
					Source: "audit", Verdict: journal.VerdictSymptomOnly, Reads: 1, Writes: 2, ReadsBeforeFirstWrite: 1,
					FirstWriteTarget: "core/pods/hello-7d9f8b6c5-x2x9q", FirstWriteAt: ptr(at("10:00:30.1")),
					FirstKubectlAt: ptr(at("10:00:05.1")),
				}
				want.compare(t, got)
			},
		},
		{
			name: "core service as a fix target", file: "symptom.log", available: true,
			targets: []string{"core/services/web"},
			check: func(t *testing.T, got summary) {
				if got.Verdict != journal.VerdictRootCause {
					t.Errorf("verdict: got %q, want root-cause", got.Verdict)
				}
			},
		},
		{
			name: "core group written empty in a fix target", file: "symptom.log", available: true,
			targets: []string{"/services/web"},
			check: func(t *testing.T, got summary) {
				if got.Verdict != journal.VerdictRootCause || got.FirstWriteTarget != "core/pods/hello-7d9f8b6c5-x2x9q" {
					t.Errorf("got verdict %q first target %q, want root-cause and core/pods/hello-7d9f8b6c5-x2x9q", got.Verdict, got.FirstWriteTarget)
				}
			},
		},
		{
			name: "audit unavailable", file: "audit.log", available: false, shell: shell,
			targets: []string{"apps/deployments/hello"},
			check: func(t *testing.T, got summary) {
				want := summary{
					Source: "shell", Verdict: journal.VerdictUnknown, FirstKubectlAt: ptr(at("10:00:04.512345678")),
					CommandsTotal: 5, KubectlCommands: 4, FailedCommands: 1,
				}
				want.compare(t, got)
			},
		},
		{
			name: "nothing at all", file: "absent.log", available: false,
			check: func(t *testing.T, got summary) {
				want := summary{Source: "none", Verdict: journal.VerdictUnknown}
				want.compare(t, got)
			},
		},
		{
			name: "audit available, no requests", file: "empty.log", available: true,
			check: func(t *testing.T, got summary) {
				want := summary{Source: "audit", Verdict: journal.VerdictNoWrites}
				want.compare(t, got)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := ReadAudit(filepath.Join("testdata", tc.file), window[0], window[1], fixtureNS, nil)
			if err != nil && !(tc.file == "absent.log" && errors.Is(err, fs.ErrNotExist)) {
				t.Fatal(err)
			}
			got := Summarize(events, tc.available, tc.shell, tc.targets)
			tc.check(t, summary{
				Source: got.Source, Verdict: got.Verdict, Reads: got.Reads, Writes: got.Writes,
				ReadsBeforeFirstWrite: got.ReadsBeforeFirstWrite, FirstWriteTarget: got.FirstWriteTarget,
				FirstWriteAt: got.FirstWriteAt, FirstKubectlAt: got.FirstKubectlAt, CommandsTotal: got.CommandsTotal,
				KubectlCommands: got.KubectlCommands, FailedCommands: got.FailedCommands,
			})
		})
	}
}

type summary struct {
	Source, FirstWriteTarget                       string
	Verdict                                        journal.Verdict
	Reads, Writes, ReadsBeforeFirstWrite           int
	CommandsTotal, KubectlCommands, FailedCommands int
	FirstWriteAt, FirstKubectlAt                   *time.Time
}

func (want summary) compare(t *testing.T, got summary) {
	t.Helper()
	eqTime := func(a, b *time.Time) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.Equal(*b)
	}
	if !eqTime(got.FirstWriteAt, want.FirstWriteAt) || !eqTime(got.FirstKubectlAt, want.FirstKubectlAt) {
		t.Errorf("times: got first write %v first kubectl %v, want %v and %v", got.FirstWriteAt, got.FirstKubectlAt, want.FirstWriteAt, want.FirstKubectlAt)
	}
	got.FirstWriteAt, got.FirstKubectlAt, want.FirstWriteAt, want.FirstKubectlAt = nil, nil, nil, nil
	if got != want {
		t.Errorf("summary:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestSummarizeCommands(t *testing.T) {
	base := at("10:00:00")
	var shell []ShellCommand
	for i := 0; i < 60; i++ {
		shell = append(shell, ShellCommand{Time: base.Add(time.Duration(i) * time.Second), Cmd: fmt.Sprintf("kubectl get pods # %d", i)})
	}
	shell = append(shell,
		ShellCommand{Time: base.Add(2 * time.Minute), Cmd: "KUBECONFIG=/tmp/k /usr/local/bin/kubectl get ns", Exit: 0},
		ShellCommand{Time: base.Add(3 * time.Minute), Cmd: "kubectx", Exit: 127},
		ShellCommand{Time: base.Add(4 * time.Minute), Cmd: "  ", Exit: 0},
	)
	got := Summarize(nil, false, shell, nil)
	if len(got.Commands) != 50 || got.Commands[0] != "kubectl get pods # 0" || got.Commands[49] != "kubectl get pods # 49" {
		t.Errorf("commands: got %d starting %q, want the first 50 in order", len(got.Commands), got.Commands)
	}
	if got.KubectlCommands != 61 || got.CommandsTotal != 63 || got.FailedCommands != 1 {
		t.Errorf("counts: got kubectl %d total %d failed %d, want 61 63 1", got.KubectlCommands, got.CommandsTotal, got.FailedCommands)
	}
}

func TestSummarizeWriteRules(t *testing.T) {
	base := at("10:00:00")
	tests := []struct {
		name        string
		events      []AuditEvent
		wantWrites  int
		wantReads   int
		wantVerdict journal.Verdict
	}{
		{
			name: "scale subresource hits the deployment",
			events: []AuditEvent{
				{Time: base, Verb: "get", Group: "apps", Resource: "deployments", Name: "hello", Code: 200},
				{Time: base.Add(time.Second), Verb: "patch", Group: "apps", Resource: "deployments", Subresource: "scale", Name: "hello", Code: 200},
			},
			wantWrites: 1, wantReads: 1, wantVerdict: journal.VerdictRootCause,
		},
		{
			name: "forbidden write is not a write",
			events: []AuditEvent{
				{Time: base, Verb: "patch", Group: "apps", Resource: "deployments", Name: "hello", Code: 403},
			},
			wantWrites: 0, wantReads: 1, wantVerdict: journal.VerdictNoWrites,
		},
		{
			name: "unsorted input is ordered by time",
			events: []AuditEvent{
				{Time: base.Add(time.Minute), Verb: "delete", Resource: "pods", Name: "p", Code: 200},
				{Time: base, Verb: "list", Resource: "pods", Code: 200},
			},
			wantWrites: 1, wantReads: 1, wantVerdict: journal.VerdictSymptomOnly,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Summarize(tc.events, true, nil, []string{"apps/deployments/hello"})
			if got.Writes != tc.wantWrites || got.Reads != tc.wantReads || got.Verdict != tc.wantVerdict {
				t.Errorf("got writes %d reads %d verdict %q, want %d %d %q", got.Writes, got.Reads, got.Verdict, tc.wantWrites, tc.wantReads, tc.wantVerdict)
			}
		})
	}
	unsorted := Summarize(tests[2].events, true, nil, nil)
	if unsorted.ReadsBeforeFirstWrite != 1 {
		t.Errorf("reads before first write on unsorted input: got %d, want 1", unsorted.ReadsBeforeFirstWrite)
	}
}

func TestSummarizeSetImageAlone(t *testing.T) {
	events, err := ReadAudit(filepath.Join("testdata", "setimage.log"), at("10:01:00"), at("10:01:30"), fixtureNS, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(events); !reflect.DeepEqual(got, []string{"get apps/deployments/hello 200", "patch apps/deployments/hello 200"}) {
		t.Fatalf("events: got %v", got)
	}
	got := Summarize(events, true, nil, []string{"apps/deployments/hello"})
	if got.Reads != 0 || got.ReadsBeforeFirstWrite != 0 || got.Writes != 1 || got.Verdict != journal.VerdictRootCause {
		t.Errorf("kubectl set image alone: got reads %d before-write %d writes %d verdict %q, want 0 0 1 root-cause", got.Reads, got.ReadsBeforeFirstWrite, got.Writes, got.Verdict)
	}
}

func TestSummarizeEventsReadCounts(t *testing.T) {
	events, err := ReadAudit(filepath.Join("testdata", "setimage.log"), at("10:00:00"), at("10:01:30"), fixtureNS, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := Summarize(events, true, nil, []string{"apps/deployments/hello"})
	if got.Reads != 1 || got.ReadsBeforeFirstWrite != 1 || got.Writes != 1 || got.Verdict != journal.VerdictRootCause {
		t.Errorf("events list then set image: got reads %d before-write %d writes %d verdict %q, want 1 1 1 root-cause", got.Reads, got.ReadsBeforeFirstWrite, got.Writes, got.Verdict)
	}
}

func TestPreWriteReadRules(t *testing.T) {
	base := at("10:00:00")
	ua, other := "kubectl/v1.37.0", "k9s/v0.50"
	get := AuditEvent{Time: base, Verb: "get", UserAgent: ua, Group: "apps", Resource: "deployments", Namespace: "ns", Name: "hello", Code: 200}
	patch := AuditEvent{Time: base.Add(200 * time.Millisecond), Verb: "patch", UserAgent: ua, Group: "apps", Resource: "deployments", Namespace: "ns", Name: "hello", Code: 200}
	tests := []struct {
		name      string
		read      func(AuditEvent) AuditEvent
		wantReads int
	}{
		{"same client same object", func(e AuditEvent) AuditEvent { return e }, 0},
		{"another user agent", func(e AuditEvent) AuditEvent { e.UserAgent = other; return e }, 1},
		{"another object", func(e AuditEvent) AuditEvent { e.Name = "other"; return e }, 1},
		{"one second or more before", func(e AuditEvent) AuditEvent { e.Time = patch.Time.Add(-time.Second); return e }, 1},
		{"a list is an observation", func(e AuditEvent) AuditEvent { e.Verb = "list"; e.Name = ""; return e }, 1},
	}
	for _, tc := range tests {
		got := Summarize([]AuditEvent{tc.read(get), patch}, true, nil, nil)
		if got.Reads != tc.wantReads || got.ReadsBeforeFirstWrite != tc.wantReads {
			t.Errorf("%s: got reads %d before-write %d, want %d", tc.name, got.Reads, got.ReadsBeforeFirstWrite, tc.wantReads)
		}
	}
	writes := []struct {
		name      string
		write     func(AuditEvent) AuditEvent
		wantReads int
	}{
		{"a rejected write keeps the read and counts as one", func(e AuditEvent) AuditEvent { e.Code = 422; return e }, 2},
		{"an exec keeps the read and counts as one", func(e AuditEvent) AuditEvent { e.Verb = "create"; e.Subresource = "exec"; return e }, 2},
	}
	for _, tc := range writes {
		got := Summarize([]AuditEvent{get, tc.write(patch)}, true, nil, nil)
		if got.Reads != tc.wantReads || got.ReadsBeforeFirstWrite != tc.wantReads {
			t.Errorf("%s: got reads %d before-write %d, want %d", tc.name, got.Reads, got.ReadsBeforeFirstWrite, tc.wantReads)
		}
	}
}

func TestReadAuditStageTimestampAndEmptyNamespace(t *testing.T) {
	path := filepath.Join("testdata", "setimage.log")
	events, err := ReadAudit(path, at("10:01:59"), at("10:02:01"), fixtureNS, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].Time.Equal(at("10:02:00")) {
		t.Errorf("event without requestReceivedTimestamp: got %+v, want one at the stageTimestamp 10:02:00", events)
	}
	events, err = ReadAudit(path, at("10:00:00"), at("11:00:00"), "", nil)
	if err != nil || len(events) != 0 {
		t.Errorf("empty namespace: got %v, %v, want no events", events, err)
	}
}

func TestMatchTargetThroughParseFixTarget(t *testing.T) {
	if !MatchTarget("core/services/web", []string{"CORE/Services/web"}) {
		t.Errorf("MatchTarget: core group in capitals did not match")
	}
	if MatchTarget("core/services/web", []string{"services/web"}) {
		t.Errorf("MatchTarget: a two part target matched")
	}
}

func TestMetadataOnly(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"no body", ``, false},
		{"null body", `null`, false},
		{"annotate", `{"metadata":{"annotations":{"team":"shop"}}}`, true},
		{"rollout restart", `{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"2026-10-27T12:00:00Z"}}}}}`, true},
		{"set image", `{"spec":{"template":{"spec":{"containers":[{"name":"web","image":"nginx:1.27-alpine"}]}}}}`, false},
		{"replicas", `{"spec":{"replicas":3}}`, false},
		{"full object update", `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"s"},"spec":{"replicas":3,"template":{"metadata":{},"spec":{}}}}`, false},
		{"configmap data", `{"data":{"index.html":"x"}}`, false},
		{"json patch on labels", `[{"op":"add","path":"/metadata/labels/x","value":"y"}]`, true},
		{"json patch on the image", `[{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"nginx"}]`, false},
		{"empty json patch", `[]`, false},
		{"not json", `{`, false},
	}
	for _, tc := range tests {
		if got := metadataOnly([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: metadataOnly = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMetadataOnlyWriteIsNotRootCause(t *testing.T) {
	restart := `{"kind":"Event","apiVersion":"audit.k8s.io/v1","stage":"ResponseComplete","verb":"patch","user":{"username":"kubernetes-admin"},"userAgent":"kubectl/v1.37.0","objectRef":{"resource":"deployments","namespace":"` + fixtureNS + `","name":"hello","apiGroup":"apps"},"responseStatus":{"code":200},"requestObject":{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"x"}}}}},"requestReceivedTimestamp":"2026-10-25T10:00:05Z"}`
	setImage := strings.Replace(restart, `{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"x"}}}`, `{"spec":{"containers":[{"name":"web","image":"nginx"}]}}`, 1)
	setImage = strings.Replace(setImage, "10:00:05Z", "10:00:09Z", 1)
	tests := []struct {
		name         string
		lines        []string
		wantVerdict  journal.Verdict
		wantMetaOnly bool
	}{
		{"rollout restart only", []string{restart}, journal.VerdictSymptomOnly, true},
		{"rollout restart then set image", []string{restart, setImage}, journal.VerdictRootCause, true},
		{"set image", []string{setImage}, journal.VerdictRootCause, false},
	}
	for _, tc := range tests {
		path := filepath.Join(t.TempDir(), "audit.log")
		if err := os.WriteFile(path, []byte(strings.Join(tc.lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		events, err := ReadAudit(path, at("10:00:00"), at("10:30:00"), fixtureNS, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := Summarize(events, true, nil, []string{"apps/deployments/hello"})
		if got.Verdict != tc.wantVerdict || got.FirstWriteMetadataOnly != tc.wantMetaOnly {
			t.Errorf("%s: verdict %q first write metadata only %v, want %q %v", tc.name, got.Verdict, got.FirstWriteMetadataOnly, tc.wantVerdict, tc.wantMetaOnly)
		}
	}
}
