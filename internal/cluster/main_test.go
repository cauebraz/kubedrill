package cluster

import (
	"os"
	"testing"
)

// TestMain points the cluster state directory at a temporary directory so
// no test touches the learner's real state (records, kubeconfigs); tests
// that need their own directory still set XDG_STATE_HOME themselves.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kubedrill-cluster-test-")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// testStateDir is the directory StateDir resolves under the test's
// XDG_STATE_HOME; tests hand it to New with WithStateDir, as the
// composition root does.
func testStateDir(t *testing.T) string {
	t.Helper()
	dir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
