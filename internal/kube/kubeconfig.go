package kube

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
)

// WriteContextKubeconfig reads srcPath and writes to dstPath a kubeconfig
// that holds only contextName, its cluster and its user, with that context
// current. The file is written with mode 0600 in a 0700 parent, so scripts
// that honor KUBECONFIG cannot reach another context, through a temporary
// file and a rename; it is left untouched when the content is unchanged.
func WriteContextKubeconfig(srcPath, contextName, dstPath string) error {
	cfg, err := clientcmd.LoadFromFile(srcPath)
	if err != nil {
		return fmt.Errorf("write kubeconfig for context %s: load %s: %w", contextName, srcPath, err)
	}
	if err := clientcmd.ResolveLocalPaths(cfg); err != nil {
		return fmt.Errorf("write kubeconfig for context %s: resolve paths in %s: %w", contextName, srcPath, err)
	}
	ctx, ok := cfg.Contexts[contextName]
	if !ok {
		return fmt.Errorf("context %s not found in kubeconfig %s; create the cluster with: kubedrill up", contextName, srcPath)
	}
	cluster, ok := cfg.Clusters[ctx.Cluster]
	if !ok {
		return fmt.Errorf("context %s in kubeconfig %s refers to missing cluster %q", contextName, srcPath, ctx.Cluster)
	}
	out := api.NewConfig()
	out.Contexts[contextName] = ctx
	out.Clusters[ctx.Cluster] = cluster
	if ctx.AuthInfo != "" {
		user, ok := cfg.AuthInfos[ctx.AuthInfo]
		if !ok {
			return fmt.Errorf("context %s in kubeconfig %s refers to missing user %q", contextName, srcPath, ctx.AuthInfo)
		}
		out.AuthInfos[ctx.AuthInfo] = user
	}
	out.CurrentContext = contextName

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return fmt.Errorf("write kubeconfig for context %s: %w", contextName, err)
	}
	data, err := clientcmd.Write(*out)
	if err != nil {
		return fmt.Errorf("write kubeconfig for context %s: encode: %w", contextName, err)
	}
	if _, err := atomicfile.WriteIfChanged(dstPath, data, 0o600); err != nil {
		return fmt.Errorf("write kubeconfig for context %s: %w", contextName, err)
	}
	return nil
}

// HasContext reports whether the kubeconfig at path holds contextName. A
// missing file is reported as false without an error.
func HasContext(path, contextName string) (bool, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("load kubeconfig %s: %w", path, err)
	}
	_, ok := cfg.Contexts[contextName]
	return ok, nil
}
