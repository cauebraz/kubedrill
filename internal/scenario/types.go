// Package scenario owns the on-disk format of a training scenario: the
// scenario.toml metadata, instruction.md, hints.md, story.md, the setup
// templates and the checks.yaml grading specification. Other packages consume the parsed
// Scenario value and never read the directory themselves.
package scenario

// Type classifies what the learner is asked to do.
type Type string

// Scenario types. A break-fix scenario deploys a broken state to repair, a
// build scenario asks for something new, a design scenario grades a written
// answer with a structural rubric, and an incident scenario is a timed drill
// with several simultaneous faults.
const (
	TypeBreakFix Type = "break-fix"
	TypeBuild    Type = "build"
	TypeDesign   Type = "design"
	TypeIncident Type = "incident"
)

// Meta is the [scenario] table of scenario.toml.
type Meta struct {
	ID        string   `toml:"id"`
	Title     string   `toml:"title"`
	Level     string   `toml:"level"`
	Module    string   `toml:"module"`
	Type      Type     `toml:"type"`
	Minutes   int      `toml:"minutes"`
	Namespace string   `toml:"namespace"`
	Requires  []string `toml:"requires"`
	Addons    []string `toml:"addons"`
	Exam      []string `toml:"exam"`
	Profile   string   `toml:"profile"`
	Concepts  []string `toml:"concepts"`
	// FixTargets lists the objects a root-cause fix writes, each shaped
	// "group/resource/name" with the core group written "core", for example
	// "apps/deployments/hello" or "core/services/hello". Telemetry compares
	// the learner's first write with these targets. A break-fix scenario
	// needs at least one; see ParseFixTarget.
	FixTargets []string `toml:"fix_targets"`
	// AllowPrivileged lets setup manifests use privileged containers,
	// hostPath volumes, hostNetwork, hostPID, hostIPC, added capabilities or
	// a docker.sock path, which Lint rejects otherwise. A scenario that sets
	// it needs maintainer review, since smoke applies its setup on the
	// contributor's cluster.
	AllowPrivileged bool `toml:"allow_privileged"`
	// Images lists images the learner's fix or the setup needs that no
	// setup manifest or probe names with a digest, each written
	// name:tag@sha256:digest, for example the image the fixed Deployment
	// runs. kubedrill up preloads them with the pinned setup and probe
	// images (Scenario.Images), so a solve needs no registry.
	Images []string `toml:"images"`
}

// Variant is one [[variant]] table: a named root cause or parameter set
// selected by the episode seed. Vars are exposed to setup templates, checks,
// instruction.md, hints.md and solution/README.md as {{ .Vars.<key> }}.
// RootCause is the reference diagnosis, one sentence of 20 to 300
// characters, required for every variant of a break-fix scenario; the
// learner's recorded diagnosis is compared with it after the episode.
type Variant struct {
	Name      string            `toml:"name"`
	Vars      map[string]string `toml:"vars"`
	RootCause string            `toml:"root_cause"`
}

// Approach is one valid way to solve the scenario, read from
// approaches/<name>/: Snippet is snippet.txt (1 to 8 lines, shown as code)
// and Body is content.md (the trade-offs of that route). Both are Go
// templates with the same data as instruction.md; Scenario.Approaches keeps
// the raw text and Rendered.Approaches the text for one episode.
type Approach struct {
	Name    string
	Snippet string
	Body    string
}

// ResourceRef identifies the object or list a check reads. Name and Selector
// are mutually exclusive; a Selector reads a list and the JSONPath applies to
// the List object (so paths start with {.items...}).
type ResourceRef struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Namespace  string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Name       string `yaml:"name,omitempty" json:"name,omitempty"`
	Selector   string `yaml:"selector,omitempty" json:"selector,omitempty"`
}

// Probe runs a one-shot pod in the scenario namespace and exposes its exit
// code and combined output to the validators.
type Probe struct {
	Image   string   `yaml:"image" json:"image"`
	Command []string `yaml:"command" json:"command"`
	Timeout string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// Check is one entry of checks.yaml. Exactly one source is set: Resource
// (read an object or list), Probe (run a pod) or Script (run a local command
// with kubectl on PATH; escape hatch). Validators apply to the extracted value:
// the JSONPath result for a Resource, the output for a Probe or Script.
// Fields are strings so templates such as {{ .Vars.image }} render before
// comparison. Why is learner-facing prose, 20 to 400 characters, stating what
// the check proves and why it matters in production; it is not templated.
// Spoiler marks a check whose actual value names the root cause (an image
// reference, a selector value); the grader copies it into the result and the
// CLI hides the actual value until the first hint or diagnosis.
type Check struct {
	ID          string   `yaml:"id" json:"id"`
	Description string   `yaml:"description" json:"description"`
	Why         string   `yaml:"why" json:"why"`
	DependsOn   []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Weight      float64  `yaml:"weight,omitempty" json:"weight,omitempty"`
	Critical    bool     `yaml:"critical,omitempty" json:"critical,omitempty"`
	Spoiler     bool     `yaml:"spoiler,omitempty" json:"spoiler,omitempty"`

	Resource *ResourceRef `yaml:"resource,omitempty" json:"resource,omitempty"`
	Path     string       `yaml:"path,omitempty" json:"path,omitempty"`
	Probe    *Probe       `yaml:"probe,omitempty" json:"probe,omitempty"`
	Script   string       `yaml:"script,omitempty" json:"script,omitempty"`

	Exists    *bool   `yaml:"exists,omitempty" json:"exists,omitempty"`
	Equals    *string `yaml:"equals,omitempty" json:"equals,omitempty"`
	NotEquals *string `yaml:"not_equals,omitempty" json:"not_equals,omitempty"`
	Matches   string  `yaml:"matches,omitempty" json:"matches,omitempty"`
	Contains  string  `yaml:"contains,omitempty" json:"contains,omitempty"`
	GTE       *string `yaml:"gte,omitempty" json:"gte,omitempty"`
	LTE       *string `yaml:"lte,omitempty" json:"lte,omitempty"`
	NotEmpty  bool    `yaml:"not_empty,omitempty" json:"not_empty,omitempty"`
	ExitCode  *int    `yaml:"exit_code,omitempty" json:"exit_code,omitempty"`
}

// Scenario is a fully loaded and validated scenario directory.
type Scenario struct {
	Dir         string
	Meta        Meta
	Variants    []Variant
	Instruction string
	Hints       []string
	Solution    string
	Checks      []Check
	SetupFiles  []string
	SolveScript string
	HookScript  string
	Approaches  []Approach
	// Story is the raw story.md, empty when the scenario has none: the
	// ticket told from zero for a learner new to the objects it involves.
	Story string

	hintsDoc      string
	simulationDoc string
}

// Rendered is a Scenario specialised for one seed: the variant is chosen,
// templates in setup files, checks, instruction.md, hints.md,
// solution/README.md, story.md, simulation.yaml and the approaches are expanded, and Vars
// carries the values the templates saw. Instruction, Hints, Solution, Story
// and Approaches are the learner-facing texts for this episode; the raw fields on Scenario keep
// the templates.
type Rendered struct {
	Scenario    *Scenario
	Seed        int64
	Variant     Variant
	Vars        map[string]string
	Namespace   string
	Manifests   []string
	Checks      []Check
	Instruction string
	Hints       []string
	Solution    string
	Story       string
	Approaches  []Approach
	// Simulation is the rendered simulation.yaml, nil when the scenario has
	// none.
	Simulation *Simulation
}
