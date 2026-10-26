package kube

import (
	"bytes"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/cauebraz/kubedrill/internal/yamldoc"
)

// Decode parses one or more YAML documents into unstructured objects. The
// documents are cut with yamldoc.Split; a document that decodes to null is
// skipped. Every object needs metadata.name: Apply and ApplyCluster
// server-side apply by name, so generateName alone is refused.
func Decode(manifest string) ([]*unstructured.Unstructured, error) {
	docs := yamldoc.Split([]byte(manifest))
	objs := make([]*unstructured.Unstructured, 0, len(docs))
	for i, doc := range docs {
		data, err := yaml.YAMLToJSON(doc)
		if err != nil {
			return nil, fmt.Errorf("decode document %d: %w", i+1, err)
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			continue
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(data); err != nil {
			return nil, fmt.Errorf("decode document %d: %w", i+1, err)
		}
		if obj.GetName() == "" {
			if obj.GetGenerateName() != "" {
				return nil, fmt.Errorf("decode document %d: %s/%s has no metadata.name (generateName is not supported: objects are applied by name)", i+1, obj.GetAPIVersion(), obj.GetKind())
			}
			return nil, fmt.Errorf("decode document %d: %s/%s has no metadata.name", i+1, obj.GetAPIVersion(), obj.GetKind())
		}
		objs = append(objs, obj)
	}
	return objs, nil
}
