package yamldoc

import (
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
