package render

import (
	"regexp"
	"strings"
)

var (
	mermaidNode  = regexp.MustCompile(`^([A-Za-z0-9_]+)\s*(\(\["(.*?)"\]\)|\["(.*?)"\]|\("(.*?)"\)|\{"(.*?)"\}|\[([^\]"]*)\]|\(([^)"]*)\))?`)
	mermaidEdge  = regexp.MustCompile(`^(<-->|-->|---|-\.->|==>)\s*(\|([^|]*)\|)?`)
	mermaidLabel = regexp.MustCompile(`^--\s+([^-|>]+?)\s+-->`)
	mermaidTag   = regexp.MustCompile(`<[^>]*>`)
	mermaidBreak = regexp.MustCompile(`(?i)<br\s*/?>`)
)

type mermaidGraph struct {
	order  []string
	labels map[string]string
	edges  []mermaidArrow
}

type mermaidArrow struct {
	from, to, label string
	both            bool
}

// mermaidAsText replaces each ```mermaid flowchart block of md with a
// plain-text drawing for the terminal: a vertical chain when the graph is a
// single path, otherwise the nodes followed by the list of arrows. A block
// that does not parse is left as it is.
func mermaidAsText(md string) string {
	lines := strings.Split(md, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```mermaid" {
			out = append(out, lines[i])
			continue
		}
		end := i + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
			end++
		}
		if end == len(lines) {
			out = append(out, lines[i:]...)
			break
		}
		g, ok := parseMermaid(lines[i+1 : end])
		if !ok {
			out = append(out, lines[i:end+1]...)
		} else {
			out = append(out, "```")
			out = append(out, g.text()...)
			out = append(out, "```")
		}
		i = end
	}
	return strings.Join(out, "\n")
}

func parseMermaid(body []string) (mermaidGraph, bool) {
	g := mermaidGraph{labels: map[string]string{}}
	header := false
	for _, raw := range body {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if !header {
			f := strings.Fields(line)
			if f[0] != "flowchart" && f[0] != "graph" {
				return g, false
			}
			header = true
			continue
		}
		if strings.HasPrefix(line, "subgraph ") || line == "end" || strings.HasPrefix(line, "direction ") {
			continue
		}
		if !g.statement(line) {
			return g, false
		}
	}
	return g, header && len(g.order) > 0
}

func (g *mermaidGraph) statement(line string) bool {
	prev := ""
	pendingLabel := ""
	pendingBoth := false
	for line != "" {
		m := mermaidNode.FindStringSubmatch(line)
		if m == nil {
			return false
		}
		id := m[1]
		g.node(id, firstNonEmpty(m[3:]...))
		if prev != "" {
			g.edges = append(g.edges, mermaidArrow{from: prev, to: id, label: pendingLabel, both: pendingBoth})
		}
		line = strings.TrimSpace(line[len(m[0]):])
		if line == "" {
			return true
		}
		pendingLabel, pendingBoth = "", false
		if e := mermaidLabel.FindStringSubmatch(line); e != nil {
			pendingLabel = strings.TrimSpace(e[1])
			line = strings.TrimSpace(line[len(e[0]):])
		} else if e := mermaidEdge.FindStringSubmatch(line); e != nil {
			pendingLabel, pendingBoth = strings.TrimSpace(e[3]), e[1] == "<-->"
			line = strings.TrimSpace(line[len(e[0]):])
		} else {
			return false
		}
		prev = id
	}
	return prev == ""
}

func (g *mermaidGraph) node(id, label string) {
	if _, seen := g.labels[id]; !seen {
		g.order = append(g.order, id)
		g.labels[id] = id
	}
	if label != "" {
		g.labels[id] = cleanMermaidLabel(label)
	}
}

func cleanMermaidLabel(s string) string {
	s = mermaidBreak.ReplaceAllString(s, "\n")
	s = mermaidTag.ReplaceAllString(s, "")
	s = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "#quot;", "'").Replace(s)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// text draws the graph: a chain when every node has at most one arrow in and
// one out and the arrows link all nodes in order, the nodes and an arrow
// list otherwise.
func (g mermaidGraph) text() []string {
	block := func(id string) []string {
		parts := strings.Split(g.labels[id], "\n")
		out := []string{parts[0]}
		for _, p := range parts[1:] {
			out = append(out, "  "+p)
		}
		return out
	}
	if chain, ok := g.chain(); ok {
		var out []string
		for i, a := range chain {
			out = append(out, block(a.from)...)
			bar := "    |"
			if a.label != "" {
				bar += " " + a.label
			}
			out = append(out, bar, "    v")
			if i == len(chain)-1 {
				out = append(out, block(a.to)...)
			}
		}
		if len(chain) == 0 {
			out = block(g.order[0])
		}
		return out
	}
	var out []string
	for i, id := range g.order {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, block(id)...)
	}
	if len(g.edges) > 0 {
		out = append(out, "", "Arrows:")
		for _, a := range g.edges {
			arrow := " -> "
			if a.both {
				arrow = " <-> "
			}
			line := "  " + strings.SplitN(g.labels[a.from], "\n", 2)[0] + arrow + strings.SplitN(g.labels[a.to], "\n", 2)[0]
			if a.label != "" {
				line += " (" + a.label + ")"
			}
			out = append(out, line)
		}
	}
	return out
}

func (g mermaidGraph) chain() ([]mermaidArrow, bool) {
	if len(g.edges) != len(g.order)-1 {
		return nil, false
	}
	next := map[string]mermaidArrow{}
	in := map[string]int{}
	for _, a := range g.edges {
		if a.both {
			return nil, false
		}
		if _, dup := next[a.from]; dup {
			return nil, false
		}
		next[a.from] = a
		in[a.to]++
	}
	start := ""
	for _, id := range g.order {
		if in[id] > 1 {
			return nil, false
		}
		if in[id] == 0 {
			if start != "" {
				return nil, false
			}
			start = id
		}
	}
	var out []mermaidArrow
	for id := start; ; {
		a, ok := next[id]
		if !ok {
			break
		}
		out = append(out, a)
		id = a.to
	}
	return out, len(out) == len(g.edges)
}
