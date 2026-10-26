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
// is exactly "---" or starts with "---" followed by a space or a tab, with
// any trailing whitespace or carriage return ignored. Text after the
// separator that is not a comment ("--- {kind: ConfigMap}") becomes the
// first line of the next document; a trailing comment such as "--- # next"
// is dropped. Documents that are empty or hold only blank lines and comment
// lines are dropped. Each document keeps its lines as they were, newline
// terminated.
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
		if ok, content := separatorLine(line); ok {
			flush()
			cur = append(cur, content...)
			continue
		}
		cur = append(cur, line...)
	}
	flush()
	return docs
}

func separatorLine(line []byte) (bool, []byte) {
	trimmed := bytes.TrimRight(line, " \t\r\n")
	if !bytes.HasPrefix(trimmed, separator) {
		return false, nil
	}
	if len(trimmed) == len(separator) {
		return true, nil
	}
	if c := trimmed[len(separator)]; c != ' ' && c != '\t' {
		return false, nil
	}
	content := bytes.TrimLeft(line[len(separator):], " \t")
	if bytes.HasPrefix(content, comment) {
		return true, nil
	}
	return true, content
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
