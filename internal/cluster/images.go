package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// PullImages pulls every image into the containerd of every kind node with
// crictl, one docker exec per node and image bounded by 60 seconds. An
// image written as name:tag@sha256:digest is pulled by its digest and then
// tagged name:tag in the node's image store, so a pod that names only the
// tag (the learner's fix) starts without the registry. It returns every
// failure at once, each naming its node and image.
func (m *Manager) PullImages(ctx context.Context, images []string) error {
	var errs []error
	for _, n := range nodeNames {
		node := Name + "-" + n
		for _, image := range images {
			if err := m.pullImage(ctx, node, image); err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("pull %s: %w", image, ctx.Err())
				}
				errs = append(errs, fmt.Errorf("pull %s on %s: %w", image, node, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) pullImage(ctx context.Context, node, image string) error {
	if _, err := m.runFor(ctx, pullTimeout, "docker", "exec", node, "crictl", "pull", image); err != nil {
		return dockerError("docker exec "+node, err)
	}
	byDigest, byTag, ok := localRefs(image)
	if !ok {
		return nil
	}
	if _, err := m.runFor(ctx, pullTimeout, "docker", "exec", node, "ctr", "--namespace", "k8s.io", "images", "tag", "--force", byDigest, byTag); err != nil {
		return dockerError("docker exec "+node, err)
	}
	return nil
}

// localRefs returns the fully qualified digest and tag references of an
// image written name:tag@sha256:digest, as containerd stores them
// (docker.io/library/nginx@sha256:... and docker.io/library/nginx:1.27);
// ok is false for an image without both a tag and a digest.
func localRefs(image string) (byDigest, byTag string, ok bool) {
	named, digest, found := strings.Cut(image, "@")
	if !found || !strings.HasPrefix(digest, "sha256:") {
		return "", "", false
	}
	slash := strings.LastIndex(named, "/")
	colon := strings.LastIndex(named, ":")
	if colon <= slash {
		return "", "", false
	}
	name, tag := named[:colon], named[colon+1:]
	if tag == "" {
		return "", "", false
	}
	name = qualifiedName(name)
	return name + "@" + digest, name + ":" + tag, true
}

// qualifiedName adds docker.io and the library/ path of Docker Hub's
// official images to a short image name.
func qualifiedName(name string) string {
	first, rest, hasSlash := strings.Cut(name, "/")
	if hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost") {
		if first == "index.docker.io" {
			first = "docker.io"
		}
		if first == "docker.io" && !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
		return first + "/" + rest
	}
	if !hasSlash {
		return "docker.io/library/" + name
	}
	return "docker.io/" + name
}
