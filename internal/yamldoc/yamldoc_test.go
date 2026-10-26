package yamldoc

import (
	"bytes"
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "single document", in: "a: 1\n", want: []string{"a: 1\n"}},
		{name: "plain separator", in: "a: 1\n---\nb: 2\n", want: []string{"a: 1\n", "b: 2\n"}},
		{name: "leading separator", in: "---\na: 1\n", want: []string{"a: 1\n"}},
		{name: "separator with trailing comment", in: "a: 1\n--- # next\nb: 2\n", want: []string{"a: 1\n", "b: 2\n"}},
		{name: "separator with tab", in: "a: 1\n---\t\nb: 2\n", want: []string{"a: 1\n", "b: 2\n"}},
		{name: "separator with trailing spaces and CR", in: "a: 1\r\n---  \r\nb: 2\r\n", want: []string{"a: 1\r\n", "b: 2\r\n"}},
		{name: "dashes inside a value are not separators", in: "a: 1\n----\nb: 2\n", want: []string{"a: 1\n----\nb: 2\n"}},
		{name: "indented dashes are not separators", in: "a:\n  ---\nb: 2\n", want: []string{"a:\n  ---\nb: 2\n"}},
		{name: "comment-only and blank documents are dropped", in: "# header\n---\n\n---\na: 1\n---\n# tail\n", want: []string{"a: 1\n"}},
		{name: "no trailing newline", in: "a: 1\n---\nb: 2", want: []string{"a: 1\n", "b: 2"}},
		{name: "content after the separator starts the next document", in: "a: 1\n--- {kind: ConfigMap}\n", want: []string{"a: 1\n", "{kind: ConfigMap}\n"}},
		{name: "content after a leading separator", in: "--- \tkind: ConfigMap\nmetadata: {}\n", want: []string{"kind: ConfigMap\nmetadata: {}\n"}},
		{name: "block scalar indicator after the separator", in: "--- |\n  text\n", want: []string{"|\n  text\n"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, d := range Split([]byte(tc.in)) {
				got = append(got, string(d))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Split(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func FuzzSplit(f *testing.F) {
	for _, seed := range []string{
		"", "a: 1\n", "a: 1\n---\nb: 2\n", "--- # c\na: 1\n", "--- {kind: ConfigMap}\n",
		"---\t|\n  x\n", "a\r\n---  \r\nb\r\n", "----\n", "  ---\n", "# only\n---\n\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var want [][]byte
		for _, line := range bytes.SplitAfter(data, []byte("\n")) {
			if ok, content := separatorLine(line); ok {
				line = content
			}
			if contentLine(line) {
				want = append(want, line)
			}
		}
		var got [][]byte
		for _, doc := range Split(data) {
			if blank(doc) {
				t.Fatalf("Split(%q) returned a blank document %q", data, doc)
			}
			for _, line := range bytes.SplitAfter(doc, []byte("\n")) {
				if contentLine(line) {
					got = append(got, line)
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Split(%q) content lines: got %q, want %q", data, got, want)
		}
	})
}

func contentLine(line []byte) bool {
	t := bytes.TrimSpace(line)
	return len(t) > 0 && !bytes.HasPrefix(t, comment)
}
