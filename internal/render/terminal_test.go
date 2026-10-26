package render

import (
	"os"
	"strings"
	"testing"
)

func TestIsTerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	regular, err := os.CreateTemp(t.TempDir(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	tests := []struct {
		name string
		r    any
	}{
		{name: "/dev/null, a character device", r: devNull},
		{name: "pipe", r: pr},
		{name: "regular file", r: regular},
		{name: "not a file", r: strings.NewReader("")},
		{name: "nil", r: nil},
	}
	for _, tc := range tests {
		if IsTerminal(tc.r) {
			t.Errorf("IsTerminal(%s): got true, want false", tc.name)
		}
	}
}
