package web

import "testing"

func TestLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1:8642": true, "localhost:8642": true, "[::1]:8642": true, "localhost": true,
		"evil.example:8642": false, "127.0.0.1.nip.io:8642": false, "": false,
	} {
		if got := loopbackHost(host); got != want {
			t.Errorf("loopbackHost(%q): got %v, want %v", host, got, want)
		}
	}
}

func TestToHTMLDropsRawHTML(t *testing.T) {
	got := toHTML("# T\n\nrun `kubectl get pods`\n\n<script>alert(1)</script>\n")
	if want := "<h1>T</h1>"; !contains(got, want) || contains(got, "<script>") || !contains(got, "<code>kubectl get pods</code>") {
		t.Errorf("toHTML: got %q, want %s and code, no script", got, want)
	}
}

func TestToHTMLLinksBareURLs(t *testing.T) {
	got := toHTML("## Go deeper\n\n- https://kubernetes.io/docs/concepts/services-networking/service/#defining-a-service : how spec.selector picks endpoints\n")
	if want := `<a href="https://kubernetes.io/docs/concepts/services-networking/service/#defining-a-service">`; !contains(got, want) {
		t.Errorf("toHTML: got %q, want a link %s", got, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
