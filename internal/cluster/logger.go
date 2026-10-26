package cluster

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	kindlog "sigs.k8s.io/kind/pkg/log"
)

type writerLogger struct {
	w io.Writer
}

func newWriterLogger(w io.Writer) kindlog.Logger {
	if w == nil {
		return kindlog.NoopLogger{}
	}
	return writerLogger{w: w}
}

func (l writerLogger) Warn(message string) { l.emit("warning: ", message) }

func (l writerLogger) Warnf(format string, args ...interface{}) {
	l.emit("warning: ", fmt.Sprintf(format, args...))
}

func (l writerLogger) Error(message string) { l.emit("error: ", message) }

func (l writerLogger) Errorf(format string, args ...interface{}) {
	l.emit("error: ", fmt.Sprintf(format, args...))
}

func (l writerLogger) V(level kindlog.Level) kindlog.InfoLogger {
	if level > 0 {
		return kindlog.NoopInfoLogger{}
	}
	return writerInfoLogger{l}
}

func (l writerLogger) emit(prefix, message string) {
	line := filterLine(message)
	if line == "" {
		return
	}
	fmt.Fprintln(l.w, prefix+line)
}

type writerInfoLogger struct {
	writerLogger
}

func (l writerInfoLogger) Info(message string) { l.emit("", message) }

func (l writerInfoLogger) Infof(format string, args ...interface{}) {
	l.emit("", fmt.Sprintf(format, args...))
}

func (l writerInfoLogger) Enabled() bool { return true }

// filterLine turns one kind log message into a learner-facing line: blank
// messages are dropped, non-ASCII runes (emoji, bullets, check marks) are
// removed, runs of spaces are collapsed and the result is trimmed. A message
// with several lines keeps one line per non-blank line.
func filterLine(message string) string {
	var out []string
	for _, raw := range strings.Split(message, "\n") {
		var b strings.Builder
		for _, r := range raw {
			if r > unicode.MaxASCII {
				continue
			}
			b.WriteRune(r)
		}
		line := strings.Join(strings.Fields(b.String()), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
