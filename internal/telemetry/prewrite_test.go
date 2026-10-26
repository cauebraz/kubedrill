package telemetry

import (
	"testing"
	"time"
)

func TestPreWriteReadsExcludeOnlyTheNearestGet(t *testing.T) {
	patchAt := at("10:00:01")
	ua := "kubectl/v1.37.0"
	get := func(d time.Duration) AuditEvent {
		return AuditEvent{Time: patchAt.Add(-d), Verb: "get", UserAgent: ua, Group: "apps", Resource: "deployments", Namespace: "ns", Name: "hello", Code: 200}
	}
	learner := get(72 * time.Millisecond)
	client := get(5 * time.Millisecond)
	patch := AuditEvent{Time: patchAt, Verb: "patch", UserAgent: ua, Group: "apps", Resource: "deployments", Namespace: "ns", Name: "hello", Code: 200}

	own := preWriteReads([]AuditEvent{learner, client, patch})
	if own[0] || !own[1] || own[2] {
		t.Errorf("preWriteReads: got own=%v, want only index 1 (the get 5ms before the patch)", own)
	}
	got := Summarize([]AuditEvent{learner, client, patch}, true, nil, []string{"apps/deployments/hello"})
	if got.Reads != 1 || got.ReadsBeforeFirstWrite != 1 || got.Writes != 1 {
		t.Errorf("Summarize: got reads %d before-write %d writes %d, want 1 1 1 (the learner get at t-72ms counts)", got.Reads, got.ReadsBeforeFirstWrite, got.Writes)
	}
}

func TestPreWriteReadsSubresourceAndFirstKubectl(t *testing.T) {
	patchAt := at("10:00:01")
	ua := "kubectl/v1.37.0"
	logs := AuditEvent{Time: patchAt.Add(-500 * time.Millisecond), Verb: "get", Subresource: "log", UserAgent: ua, Resource: "pods", Namespace: "ns", Name: "hello", Code: 200}
	plain := AuditEvent{Time: patchAt.Add(-20 * time.Millisecond), Verb: "get", UserAgent: ua, Resource: "pods", Namespace: "ns", Name: "hello", Code: 200}
	patch := AuditEvent{Time: patchAt, Verb: "patch", UserAgent: ua, Resource: "pods", Namespace: "ns", Name: "hello", Code: 200}

	own := preWriteReads([]AuditEvent{logs, plain, patch})
	if own[0] || !own[1] {
		t.Errorf("preWriteReads: got own=%v, want the plain get excluded and the pods/log get kept", own)
	}
	got := Summarize([]AuditEvent{logs, plain, patch}, true, nil, nil)
	if got.Reads != 1 || got.ReadsBeforeFirstWrite != 1 || got.Writes != 1 {
		t.Errorf("Summarize: got reads %d before-write %d writes %d, want 1 1 1 (pods/log counts as a read)", got.Reads, got.ReadsBeforeFirstWrite, got.Writes)
	}
	if got.FirstKubectlAt == nil || !got.FirstKubectlAt.Equal(logs.Time) {
		t.Errorf("FirstKubectlAt: got %v, want the first learner event at %s", got.FirstKubectlAt, logs.Time)
	}
}
