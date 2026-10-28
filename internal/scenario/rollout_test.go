package scenario

import (
	"strings"
	"testing"
)

const rolloutCheck = `  - id: rolled-out
    description: deployment app finished rolling out
    why: old healthy pods keep readyReplicas up while a broken new template crash-loops
    depends_on: [exists]
    rollout:
      kind: Deployment
      namespace: "{{ .Namespace }}"
      name: app
`

func TestLoadRolloutCheck(t *testing.T) {
	f := validFixture()
	f["checks.yaml"] += rolloutCheck
	f["instruction.md"] += "- deployment app finished rolling out\n"
	s, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rd, err := s.Render(7)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := rd.Checks[len(rd.Checks)-1].Rollout
	if got == nil || got.Kind != "Deployment" || got.Name != "app" || got.Namespace != rd.Namespace {
		t.Errorf("rendered rollout: got %+v, want Deployment app in %s", got, rd.Namespace)
	}
}

func TestLoadRolloutCheckErrors(t *testing.T) {
	tests := []struct {
		name  string
		check string
		want  []string
	}{
		{
			name:  "unsupported kind and missing name",
			check: strings.NewReplacer("kind: Deployment", "kind: DaemonSet", "      name: app\n", "").Replace(rolloutCheck),
			want:  []string{`rollout.kind must be Deployment or StatefulSet, got "DaemonSet"`, "rollout.name is required"},
		},
		{
			name:  "missing kind",
			check: strings.Replace(rolloutCheck, "      kind: Deployment\n", "", 1),
			want:  []string{"rollout.kind is required"},
		},
		{
			name:  "validator on a rollout",
			check: rolloutCheck + "    equals: \"2\"\n",
			want:  []string{"a rollout check is its own assertion and takes no validator"},
		},
		{
			name:  "rollout next to a resource",
			check: rolloutCheck + "    resource:\n      apiVersion: apps/v1\n      kind: Deployment\n      name: app\n",
			want:  []string{"exactly one source is allowed, got 2"},
		},
		{
			name:  "template error in the name",
			check: strings.Replace(rolloutCheck, "name: app", "name: \"{{ .Vars.image \"", 1),
			want:  []string{"rollout.name:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validFixture()
			f["checks.yaml"] += tt.check
			f["instruction.md"] += "- deployment app finished rolling out\n"
			_, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
			if err == nil {
				t.Fatalf("Load succeeded, want errors containing %q", tt.want)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%v", w, err)
				}
			}
		})
	}
}
