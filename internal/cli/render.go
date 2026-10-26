package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/render"
)

// printer is the printer on stdout at the run's width.
func (a *app) printer() render.Printer {
	return a.term.Printer()
}

// writeGrade prints every result and the summary line; hide keeps the
// actual value of spoiler checks back (see render.SpoilerHidden).
func (a *app) writeGrade(w io.Writer, g grader.Grade, hide bool) {
	p := a.printer()
	for _, r := range g.Results {
		for _, line := range render.ResultLines(r, p.Tag(r.Status), true, hide) {
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintln(w, render.SummaryLine(g))
}

// table writes rows as aligned columns separated by two spaces.
func table(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// conceptsLine lists ids as "a, b".
func conceptsLine(ids []string) string {
	return strings.Join(ids, ", ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
