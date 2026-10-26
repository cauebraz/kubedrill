package render

import (
	"strings"
	"testing"
	"time"
)

func TestProgressDivertSendsStepsToSink(t *testing.T) {
	var errb strings.Builder
	p := NewProgress(&errb, time.Now)
	var got []string
	restore := p.Divert(func(msg string) { got = append(got, msg) })
	p.Step("applying setup")
	p.Finish()
	if errb.Len() != 0 || strings.Join(got, ",") != "applying setup" {
		t.Errorf("sink: stderr %q, sink %v", errb.String(), got)
	}
	restore()
	p.Step("creating namespace")
	p.Finish()
	if errb.String() != "creating namespace... done\n" || len(got) != 1 {
		t.Errorf("after restore: stderr %q, sink %v", errb.String(), got)
	}
}

func TestDoneStatus(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "done"},
		{99 * time.Millisecond, "done"},
		{100 * time.Millisecond, "done (0.1s)"},
		{1234 * time.Millisecond, "done (1.2s)"},
	} {
		if got := doneStatus(tc.d); got != tc.want {
			t.Errorf("doneStatus(%s): got %q, want %q", tc.d, got, tc.want)
		}
	}
}
