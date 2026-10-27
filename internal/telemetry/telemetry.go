// Package telemetry reads what the learner did during an episode: the API
// server audit log (audit.k8s.io/v1 Event JSON lines) and the workstation
// shell log, and summarises both into journal.Telemetry. It never talks to
// the cluster; the files are written by the API server and the workstation.
package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// MetadataOnly is set when the audit log holds the request body of a
	// patch or update and that body changes only metadata (labels,
	// annotations) of the object or of its pod template, as kubectl
	// annotate, label and rollout restart do. Without a body it is false.
	MetadataOnly bool
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
	RequestObject            json.RawMessage `json:"requestObject"`
	RequestReceivedTimestamp time.Time       `json:"requestReceivedTimestamp"`
	StageTimestamp           time.Time       `json:"stageTimestamp"`
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
// next to it (audit-*.log) modified at or after since. The current file is
// opened before the backups are listed, so a rotation in between neither
// loses the lines it moved nor reads them twice. Only the
// ResponseComplete stage is kept. Requests from kubedrill itself (user agent
// containing "kubedrill", which covers the kubedrill-check kubectl of script
// checks), from system users (controllers, nodes, service accounts) and
// dry-run requests are dropped. Lines that do not decode, such as a partial
// last line while the API server writes, and lines over 16 MiB are skipped.
// A missing path returns
// an error that satisfies errors.Is(err, fs.ErrNotExist): with the audit log
// on, a missing file means the trajectory is unknown, not empty.
func ReadAudit(path string, since, until time.Time, namespace string, fixTargets []string) ([]AuditEvent, error) {
	return readAudit(path, since, until, namespace, fixTargets, nil)
}

// readAudit is ReadAudit; afterOpen, when not nil, runs between opening the
// current file and listing the backups.
func readAudit(path string, since, until time.Time, namespace string, fixTargets []string, afterOpen func()) ([]AuditEvent, error) {
	cur, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	defer cur.Close()
	curInfo, err := cur.Stat()
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	if afterOpen != nil {
		afterOpen()
	}
	backups := rotated(path, since, curInfo)
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
		if (l.Verb == "patch" || l.Verb == "update") && ref.Subresource == "" {
			e.MetadataOnly = metadataOnly(l.RequestObject)
		}
		out = append(out, e)
	}
	for _, f := range backups {
		if err := scanFile(f, keep); err != nil {
			return nil, fmt.Errorf("read audit log: %w", err)
		}
	}
	if err := eachLine(cur, maxLine, keep); err != nil {
		return nil, fmt.Errorf("read audit log: scan %s: %w", path, err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// rotated lists the backups the API server keeps next to path
// (<base>-<timestamp><ext>) modified at or after since, oldest first,
// leaving out the one that is the same file as current (the open current
// file, renamed by a rotation after it was opened).
func rotated(path string, since time.Time, current os.FileInfo) []string {
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
		if err != nil || info.IsDir() || info.ModTime().Before(since) || os.SameFile(info, current) {
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
// decode and lines over 16 MiB are skipped, and a line whose leading ts is
// outside the window is skipped without decoding the rest. A missing file
// returns nil, nil; an error means the file could not be read at all.
func ReadShellLog(path string, since, until time.Time) ([]ShellCommand, error) {
	var out []ShellCommand
	err := scanFile(path, func(line []byte) {
		if ts, ok := leadingTS(line); ok && (ts.Before(since) || ts.After(until)) {
			return
		}
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
// scenario.ParseFixTarget returns it) with more than a metadata change
// (MetadataOnly false: a rollout restart or an annotation is not a fix) and
// "symptom-only" otherwise. Commands lists up to 50 kubectl command lines from the shell
// log, in order.
func Summarize(audit []AuditEvent, auditAvailable bool, shell []ShellCommand, fixTargets []string) journal.Telemetry {
	t := journal.Telemetry{Source: source(auditAvailable, len(shell) > 0), Verdict: journal.VerdictUnknown}
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
				t.FirstWriteMetadataOnly = e.MetadataOnly
			}
			if MatchTarget(target, fixTargets) && !e.MetadataOnly {
				hit = true
			}
		}
		switch {
		case t.Writes == 0:
			t.Verdict = journal.VerdictNoWrites
		case hit:
			t.Verdict = journal.VerdictRootCause
		default:
			t.Verdict = journal.VerdictSymptomOnly
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

// preWriteReads marks, for every write that counts (countsAsWrite: it
// succeeded and is not a connection subresource) in events (sorted by
// time), the
// nearest preceding get of the same object by the same user agent less than
// preWriteWindow before it: the client's own read before it writes. Only
// that one get is marked; an earlier get within the window is the learner
// looking.
func preWriteReads(events []AuditEvent) map[int]bool {
	own := map[int]bool{}
	for i, w := range events {
		if !countsAsWrite(w) {
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

// RotateShellLog renames the shell log at path to path+".1", replacing an
// older backup, when it is larger than limit bytes, so the next episode
// reads a short file. The workstation shell reopens the log for every
// command, so the next command starts a new file. It reports whether it
// rotated; a missing file is not an error.
func RotateShellLog(path string, limit int64) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("rotate shell log: %w", err)
	}
	if info.Size() <= limit {
		return false, nil
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return false, fmt.Errorf("rotate shell log: %w", err)
	}
	return true, nil
}

var tsPrefix = []byte(`{"ts":"`)

// leadingTS parses the ts field when the line starts with it, as the
// workstation shell writes every line.
func leadingTS(line []byte) (time.Time, bool) {
	if !bytes.HasPrefix(line, tsPrefix) {
		return time.Time{}, false
	}
	rest := line[len(tsPrefix):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, string(rest[:end]))
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// scanFile calls fn for every non-empty line of path up to maxLine bytes; a
// missing file has no lines.
func scanFile(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := eachLine(f, maxLine, fn); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	return nil
}

// eachLine calls fn for every non-empty line of r without its line ending.
// A line longer than limit bytes is skipped, not an error; the slice passed
// to fn is only valid during the call.
func eachLine(r io.Reader, limit int, fn func(line []byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	overlong := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !overlong {
			if len(buf)+len(chunk) > limit+1 {
				overlong, buf = true, buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if !overlong {
			if line := bytes.TrimRight(buf, "\r\n"); len(line) > 0 {
				fn(line)
			}
		}
		buf, overlong = buf[:0], false
		if err != nil {
			return nil
		}
	}
}

// metadataOnly reports whether a patch or update request body changes only
// metadata: a merge or strategic merge patch (an object) whose keys outside
// metadata, apiVersion and kind are only spec.template.metadata, or a JSON
// patch (an array) whose every path is under /metadata or
// /spec/template/metadata. An empty or unreadable body is not metadata
// only, so a write without a recorded body keeps counting as a change.
func metadataOnly(body json.RawMessage) bool {
	if len(body) == 0 || string(body) == "null" {
		return false
	}
	var ops []struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(body, &ops) == nil {
		if len(ops) == 0 {
			return false
		}
		for _, op := range ops {
			if !strings.HasPrefix(op.Path, "/metadata") && !strings.HasPrefix(op.Path, "/spec/template/metadata") {
				return false
			}
		}
		return true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return false
	}
	for k, v := range obj {
		switch k {
		case "metadata", "apiVersion", "kind":
			continue
		case "spec":
			if !onlyTemplateMetadata(v) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// onlyTemplateMetadata reports whether a spec value holds nothing but
// template.metadata.
func onlyTemplateMetadata(spec json.RawMessage) bool {
	var s map[string]json.RawMessage
	if json.Unmarshal(spec, &s) != nil {
		return false
	}
	for k, v := range s {
		if k != "template" {
			return false
		}
		var tpl map[string]json.RawMessage
		if json.Unmarshal(v, &tpl) != nil {
			return false
		}
		for tk := range tpl {
			if tk != "metadata" {
				return false
			}
		}
	}
	return true
}
