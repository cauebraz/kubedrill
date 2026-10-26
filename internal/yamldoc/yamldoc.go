// Package yamldoc splits a multi-document YAML stream on its document
// separators. kube, scenario and cluster share it so that a manifest is cut
// the same way wherever it is read.
package yamldoc

import (
	"bytes"
)

var (
	separator = []byte("---")
	comment   = []byte("#")
)

// Split returns the documents of data, in order. A separator is a line that
// is exactly "---" or starts with "---" followed by a space or a tab (a
// trailing comment such as "--- # next" included), with any trailing
// whitespace or carriage return ignored. Documents that are empty or hold
// only blank lines and comment lines are dropped. Each document keeps its
// lines as they were, newline terminated.
func Split(data []byte) [][]byte {
	var docs [][]byte
	var cur []byte
	flush := func() {
		if !blank(cur) {
			docs = append(docs, cur)
		}
		cur = nil
	}
	rest := data
	for len(rest) > 0 {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line = rest[:i+1]
			rest = rest[i+1:]
		} else {
			rest = nil
		}
		if isSeparator(line) {
			flush()
			continue
		}
		cur = append(cur, line...)
	}
	flush()
	return docs
}

func isSeparator(line []byte) bool {
	trimmed := bytes.TrimRight(line, " \t\r\n")
	if !bytes.HasPrefix(trimmed, separator) {
		return false
	}
	if len(trimmed) == len(separator) {
		return true
	}
	c := trimmed[len(separator)]
	return c == ' ' || c == '\t'
}

func blank(doc []byte) bool {
	for _, line := range bytes.Split(doc, []byte("\n")) {
		t := bytes.TrimSpace(line)
		if len(t) > 0 && !bytes.HasPrefix(t, comment) {
			return false
		}
	}
	return true
}
