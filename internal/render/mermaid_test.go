package render

import (
	"strings"
	"testing"
)

func TestMermaidAsText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "chain",
			in: "before\n```mermaid\nflowchart TD\n" +
				"  D[\"<b>Deployment storefront</b><br/>keep 3 pods\"]\n" +
				"  R[\"<b>ReplicaSet</b><br/>creates the pods\"]\n" +
				"  C([\"<b>customers</b><br/>no Ready pod: 503\"])\n" +
				"  D --> R -->|routes| C\n```\nafter",
			want: "before\n```\nDeployment storefront\n  keep 3 pods\n    |\n    v\nReplicaSet\n  creates the pods\n    | routes\n    v\ncustomers\n  no Ready pod: 503\n```\nafter",
		},
		{
			name: "branch lists nodes and arrows",
			in:   "```mermaid\nflowchart LR\n  A[\"<b>API</b>\"] --> B[\"<b>DNS</b>\"]\n  A -- reads --> C[\"<b>Cache</b>\"]\n```",
			want: "```\nAPI\n\nDNS\n\nCache\n\nArrows:\n  API -> DNS\n  API -> Cache (reads)\n```",
		},
		{
			name: "escaped text and subgraph",
			in:   "```mermaid\nflowchart TD\n  subgraph node\n  A[\"<b>kubelet</b><br/>listen &lt;port&gt;\"] --> B[\"<b>runtime</b>\"]\n  end\n```",
			want: "```\nkubelet\n  listen <port>\n    |\n    v\nruntime\n```",
		},
		{
			name: "two-way arrow",
			in:   "```mermaid\nflowchart TD\n  W[\"<b>Waiting</b>\"] <--> R[\"<b>Running</b>\"]\n```",
			want: "```\nWaiting\n\nRunning\n\nArrows:\n  Waiting <-> Running\n```",
		},
		{
			name: "unparsable block stays as source",
			in:   "```mermaid\nsequenceDiagram\n  A->>B: hi\n```",
			want: "```mermaid\nsequenceDiagram\n  A->>B: hi\n```",
		},
		{
			name: "no mermaid",
			in:   "```sh\nkubectl get pods\n```",
			want: "```sh\nkubectl get pods\n```",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mermaidAsText(tc.in); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestMarkdownDrawsMermaidAsText(t *testing.T) {
	p := Printer{Width: 80}
	got := p.Markdown("```mermaid\nflowchart TD\n  A[\"<b>Pod</b>\"] --> B[\"<b>Service</b>\"]\n```\n")
	if strings.Contains(got, "flowchart") || !strings.Contains(got, "Pod\n    |\n    v\nService") {
		t.Errorf("Markdown: got %q, want the chain drawn as text", got)
	}
}
