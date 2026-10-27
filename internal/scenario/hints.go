package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var hintLadder = []string{
	"## Hint 1: where to look",
	"## Hint 2: what to observe",
	"## Hint 3: what it means",
	"## Hint 4: what to change",
}

var mutatingVerbs = map[string]bool{
	"set": true, "edit": true, "patch": true, "apply": true, "create": true,
	"replace": true, "delete": true, "scale": true, "label": true,
	"annotate": true, "expose": true, "run": true, "autoscale": true,
	"cordon": true, "uncordon": true, "drain": true, "taint": true,
}

var mutatingRollout = map[string]bool{"undo": true, "restart": true, "pause": true, "resume": true}

var readVerbs = map[string]bool{
	"get": true, "describe": true, "logs": true, "explain": true, "top": true,
	"events": true, "auth": true, "api-resources": true, "api-versions": true,
	"version": true, "config": true, "diff": true, "wait": true,
	"port-forward": true, "exec": true, "debug": true, "cp": true,
	"attach": true, "cluster-info": true, "kustomize": true, "proxy": true,
	"certificate": true, "plugin": true, "completion": true, "alpha": true,
	"options": true, "help": true,
}

func loadHints(dir string, s *Scenario, fail failFunc) {
	b, err := os.ReadFile(filepath.Join(dir, fileHints))
	if err != nil {
		fail(fileHints, "%v", err)
		return
	}
	doc := string(b)
	if _, err := parseTemplate(fileHints, doc); err != nil {
		fail(fileHints, "%v", err)
		return
	}
	sections, err := parseHintSections(doc)
	if err != nil {
		fail(fileHints, "%v", err)
		return
	}
	validateHintLadder(sections, fail)
	for _, h := range sections {
		if cmd := mutatingKubectl(h.body); cmd != "" {
			fail(fileHints, "%q contains the mutating command %q; hints describe where to look and what to change, the learner types the change", h.heading, cmd)
		}
	}
	s.hintsDoc = doc
	s.Hints = make([]string, len(sections))
	for i, h := range sections {
		s.Hints[i] = h.body
	}
}

func validateHintLadder(sections []hintSection, fail failFunc) {
	if n := len(sections); n < minHints || n > len(hintLadder) {
		fail(fileHints, "has %d hints, want %d or %d: %s", n, minHints, len(hintLadder), strings.Join(hintLadder, ", "))
	}
	for i, h := range sections {
		if i >= len(hintLadder) {
			break
		}
		if h.heading != hintLadder[i] {
			fail(fileHints, "hint %d heading is %q, want %q", i+1, h.heading, hintLadder[i])
		}
	}
}

type hintSection struct {
	heading string
	body    string
}

func parseHintSections(doc string) ([]hintSection, error) {
	locs := outsideFences(doc, hintPattern.FindAllStringIndex(doc, -1))
	if len(locs) == 0 {
		return nil, errors.New("no \"## Hint N\" section found, at least one is required")
	}
	var hints []hintSection
	var errs []error
	for i, loc := range locs {
		end := len(doc)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		heading := strings.TrimSpace(doc[loc[0]:loc[1]])
		body := strings.TrimSpace(doc[loc[1]:end])
		if body == "" {
			errs = append(errs, fmt.Errorf("%s is empty", heading))
			continue
		}
		hints = append(hints, hintSection{heading: heading, body: body})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return hints, nil
}

func mutatingKubectl(text string) string {
	text = strings.NewReplacer("`", "\n", "|", "\n", ";", "\n", "&", "\n", "$(", "\n", "(", " ", ")", " ").Replace(text)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f != "kubectl" && f != "k" {
				continue
			}
			if verb := kubectlVerb(fields[i+1:]); verb != "" {
				return "kubectl " + verb
			}
		}
	}
	return ""
}

func kubectlVerb(args []string) string {
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		a = strings.Trim(a, `"'.,:`)
		switch {
		case a == "rollout":
			for _, b := range args[i+1:] {
				if strings.HasPrefix(b, "-") {
					continue
				}
				if b = strings.Trim(b, `"'.,:`); mutatingRollout[b] {
					return "rollout " + b
				}
				return ""
			}
			return ""
		case mutatingVerbs[a]:
			return a
		case readVerbs[a]:
			return ""
		}
	}
	return ""
}
