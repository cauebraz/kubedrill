package scenario

import (
	"strings"
	"testing"
)

func FuzzParseHintSections(f *testing.F) {
	for _, seed := range []string{"", fixtureHints, "## Hint 1\n", "## Hint 1: x\nbody\n## Hint 2\n", "## Hint 99:\t\n\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, doc string) {
		hints, err := parseHintSections(doc)
		if err != nil {
			return
		}
		for _, h := range hints {
			if strings.TrimSpace(h.body) == "" {
				t.Errorf("parseHintSections(%q): got empty body for %q", doc, h.heading)
			}
		}
	})
}

func FuzzMutatingKubectl(f *testing.F) {
	for _, seed := range []string{"", "kubectl", "kubectl get pods", "`kubectl -n x delete pod y`", "kubectl rollout", "kubectl rollout restart deploy/x", "$(kubectl apply -f -)", "kubectl --context=x rollout -w undo"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got := mutatingKubectl(text)
		if got != "" && !strings.HasPrefix(got, "kubectl ") {
			t.Errorf("mutatingKubectl(%q): got %q, want empty or a kubectl command", text, got)
		}
	})
}
