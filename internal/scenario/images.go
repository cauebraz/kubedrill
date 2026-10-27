package scenario

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/cauebraz/kubedrill/internal/yamldoc"
)

var pinnedImageRe = regexp.MustCompile(`^[^\s@]+:[^\s@/:]+@sha256:[0-9a-f]{64}$`)

// containerImage is one container image of a setup manifest.
type containerImage struct {
	kind, name, container, image string
}

// Images returns the images a solve of s needs on the nodes, sorted and
// without duplicates: scenario.images, every probe image and every setup
// image pinned by digest, over every variant. A setup image without a
// digest is the bug of a variant or refused by lint, so it is left out.
func (s *Scenario) Images() ([]string, error) {
	set := map[string]bool{}
	for _, image := range s.Meta.Images {
		set[image] = true
	}
	var errs []error
	for _, seed := range s.VariantSeeds() {
		r, err := s.Render(seed)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, c := range r.Checks {
			if c.Probe != nil && c.Probe.Image != "" {
				set[c.Probe.Image] = true
			}
		}
		for _, m := range r.Manifests {
			for _, ci := range manifestImages(m) {
				if strings.Contains(ci.image, "@sha256:") {
					set[ci.image] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for image := range set {
		out = append(out, image)
	}
	sort.Strings(out)
	if len(errs) > 0 {
		return out, fmt.Errorf("images of %s: %w", s.Meta.ID, errors.Join(errs...))
	}
	return out, nil
}

// manifestImages lists the image of every container, init container and
// ephemeral container in a rendered manifest, at any depth, so pods,
// workloads and CronJob templates are covered alike.
func manifestImages(manifest string) []containerImage {
	var out []containerImage
	for _, doc := range yamldoc.Split([]byte(manifest)) {
		var obj map[string]any
		if err := yaml.Unmarshal(doc, &obj); err != nil {
			continue
		}
		kind, _ := obj["kind"].(string)
		var name string
		if md, ok := obj["metadata"].(map[string]any); ok {
			name, _ = md["name"].(string)
		}
		walkContainers(obj, func(container, image string) {
			out = append(out, containerImage{kind: kind, name: name, container: container, image: image})
		})
	}
	return out
}

func walkContainers(v any, fn func(container, image string)) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "containers" || k == "initContainers" || k == "ephemeralContainers" {
				if list, ok := t[k].([]any); ok {
					for _, item := range list {
						c, ok := item.(map[string]any)
						if !ok {
							continue
						}
						image, _ := c["image"].(string)
						name, _ := c["name"].(string)
						if image != "" {
							fn(name, image)
						}
					}
					continue
				}
			}
			walkContainers(t[k], fn)
		}
	case []any:
		for _, item := range t {
			walkContainers(item, fn)
		}
	}
}
