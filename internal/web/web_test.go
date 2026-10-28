package web_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/cauebraz/kubedrill/internal/web"
	"github.com/cauebraz/kubedrill/internal/web/webtest"
)

const testToken = "0123456789abcdef"

var update = flag.Bool("update", false, "rewrite the JSON snapshots under testdata")

type client struct {
	t      *testing.T
	srv    *httptest.Server
	cookie *http.Cookie
}

func newClient(t *testing.T, b web.Backend) *client {
	t.Helper()
	s := web.New(web.Deps{Backend: b, ProgressPath: filepath.Join(t.TempDir(), "progress.json"), Token: testToken, Now: fixedNow})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &client{t: t, srv: srv, cookie: &http.Cookie{Name: "kubedrill_token", Value: testToken}}
}

func fixedNow() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }

func newFake() *webtest.Fake {
	f := webtest.New()
	f.Now = fixedNow
	return f
}

func (c *client) do(method, path, body string, header bool) (*http.Response, string) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.srv.URL+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if header {
		req.Header.Set("X-Kubedrill", "1")
	}
	res, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func (c *client) json(method, path, body string, out any) int {
	c.t.Helper()
	res, text := c.do(method, path, body, true)
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			c.t.Fatalf("%s %s: decode %q: %v", method, path, text, err)
		}
	}
	return res.StatusCode
}

// start posts /api/start and reads the NDJSON stream: the steps, then the
// session or the error line.
func (c *client) start(body string) (steps []string, sess web.Session, errLine string, status int) {
	c.t.Helper()
	res, text := c.do("POST", "/api/start", body, true)
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		var line struct {
			Step    string       `json:"step"`
			Session *web.Session `json:"session"`
			Error   string       `json:"error"`
			Status  int          `json:"status"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			c.t.Fatalf("start: line %q: %v", sc.Text(), err)
		}
		switch {
		case line.Step != "":
			steps = append(steps, line.Step)
		case line.Session != nil:
			sess = *line.Session
		case line.Error != "":
			errLine, status = line.Error, line.Status
		}
	}
	if status == 0 {
		status = res.StatusCode
	}
	return steps, sess, errLine, status
}

func TestGuard(t *testing.T) {
	c := newClient(t, newFake())
	noRedirect := c.srv.Client()
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	t.Run("no cookie is refused with a pointer to the link", func(t *testing.T) {
		c2 := *c
		c2.cookie = nil
		res, body := c2.do("GET", "/api/catalog", "", false)
		if res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Open the link") {
			t.Errorf("got %d %q, want 401 with the link hint", res.StatusCode, body)
		}
	})
	t.Run("the token link sets the cookie", func(t *testing.T) {
		res, err := noRedirect.Get(c.srv.URL + "/?t=" + testToken)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		var got *http.Cookie
		for _, ck := range res.Cookies() {
			if ck.Name == "kubedrill_token" {
				got = ck
			}
		}
		if res.StatusCode != http.StatusSeeOther || got == nil || got.Value != testToken || !got.HttpOnly || got.SameSite != http.SameSiteStrictMode {
			t.Errorf("got %d cookie %+v, want 303 with an HttpOnly SameSite=Strict token cookie", res.StatusCode, got)
		}
	})
	t.Run("cross-site requests are refused and pages carry a CSP", func(t *testing.T) {
		for site, want := range map[string]int{"same-site": http.StatusForbidden, "cross-site": http.StatusForbidden, "same-origin": http.StatusOK, "none": http.StatusOK} {
			req, _ := http.NewRequest("GET", c.srv.URL+"/api/catalog", nil)
			req.AddCookie(c.cookie)
			req.Header.Set("Sec-Fetch-Site", site)
			res, err := c.srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != want {
				t.Errorf("Sec-Fetch-Site %s: got %d, want %d", site, res.StatusCode, want)
			}
		}
		res, _ := c.do("GET", "/", "", false)
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("CSP: got %q", csp)
		}
	})
	t.Run("a wrong token link is refused", func(t *testing.T) {
		res, err := noRedirect.Get(c.srv.URL + "/?t=nope")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("got %d, want 403", res.StatusCode)
		}
	})
	t.Run("a foreign host is refused", func(t *testing.T) {
		req, _ := http.NewRequest("GET", c.srv.URL+"/api/catalog", nil)
		req.Host = "evil.example:8642"
		req.AddCookie(c.cookie)
		res, err := c.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMisdirectedRequest {
			t.Errorf("got %d, want 421", res.StatusCode)
		}
	})
	t.Run("a POST without the header is refused", func(t *testing.T) {
		res, _ := c.do("POST", "/api/hint", "{}", false)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("got %d, want 403", res.StatusCode)
		}
	})
	t.Run("static files are served with module types", func(t *testing.T) {
		for _, p := range []string{"/", "/app.js", "/app.css", "/js/api.js", "/js/lab.js", "/vendor/xterm/xterm.js", "/vendor/xterm/addon-fit.js"} {
			res, _ := c.do("GET", p, "", false)
			if res.StatusCode != http.StatusOK {
				t.Errorf("GET %s: got %d, want 200", p, res.StatusCode)
			}
			if strings.HasSuffix(p, ".js") && !strings.Contains(res.Header.Get("Content-Type"), "javascript") {
				t.Errorf("GET %s: Content-Type %q, want javascript (ES modules need it)", p, res.Header.Get("Content-Type"))
			}
		}
	})
}

func TestStaticDirServesFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("from disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := web.New(web.Deps{Backend: newFake(), Token: testToken, StaticDir: dir, Preview: "001"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := &client{t: t, srv: srv, cookie: &http.Cookie{Name: "kubedrill_token", Value: testToken}}
	if _, body := c.do("GET", "/", "", false); body != "from disk" {
		t.Errorf("index: got %q, want the file from --static-dir", body)
	}
	var meta map[string]any
	c.json("GET", "/api/meta", "", &meta)
	if meta["preview"] != "001" || meta["dev"] != true {
		t.Errorf("meta: got %v, want preview 001 and dev true", meta)
	}
}

func TestAssignmentRendersMarkdownWithoutRawHTML(t *testing.T) {
	c := newClient(t, newFake())
	var a web.Assignment
	if code := c.json("GET", "/api/assignment/001", "", &a); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !strings.Contains(a.Ticket, "<h1>Storefront is down") || strings.Contains(a.Ticket, "<script>") {
		t.Errorf("ticket HTML: got %q", a.Ticket)
	}
	if len(a.Concepts) != 1 || !strings.Contains(a.Concepts[0].Body, "<strong>Pending</strong>") {
		t.Errorf("concept body: got %+v, want HTML", a.Concepts)
	}
	if code := c.json("GET", "/api/assignment/999", "", nil); code != 500 {
		t.Errorf("unknown assignment: got %d, want 500", code)
	}
}

func TestStartStreamsSteps(t *testing.T) {
	f := newFake()
	c := newClient(t, f)
	if _, _, errLine, status := c.start(`{"id":"001","stage":"quiz"}`); status != 400 || !strings.Contains(errLine, "does not run an episode") {
		t.Errorf("start of a page stage: got %d %q, want 400", status, errLine)
	}
	steps, sess, errLine, _ := c.start(`{"id":"001","stage":"practice"}`)
	if errLine != "" {
		t.Fatalf("start: %s", errLine)
	}
	if want := []string{"creating namespace shop-abcde", "applying 3 setup manifests", "recording the episode"}; strings.Join(steps, "|") != strings.Join(want, "|") {
		t.Errorf("steps: got %q, want %q", steps, want)
	}
	if sess.Active == nil || sess.Active.Stage != web.StagePractice || sess.Active.Ticket == "" || !strings.HasPrefix(sess.Active.Ticket, "<h1>") {
		t.Fatalf("session: got %+v", sess.Active)
	}
	if _, _, errLine, status := c.start(`{"id":"001","stage":"diy"}`); status != http.StatusConflict || errLine == "" {
		t.Errorf("start over an active episode: got %d %q, want 409", status, errLine)
	}
}

func TestStageFlowAndDebrief(t *testing.T) {
	f := newFake()
	c := newClient(t, f)

	if code := c.json("POST", "/api/progress", `{"id":"001-storefront-down","stage":"practice","score":1,"total":1}`, nil); code != 400 {
		t.Errorf("page-recorded practice: got %d, want 400", code)
	}
	var sp web.StageProgress
	if code := c.json("POST", "/api/progress", `{"id":"001-storefront-down","stage":"meeting","score":1,"total":2}`, &sp); code != 200 || !sp.Meeting.Done {
		t.Fatalf("meeting progress: got %d %+v", code, sp)
	}

	c.start(`{"id":"001","stage":"practice"}`)
	var v web.Validation
	if code := c.json("POST", "/api/validate", "", &v); code != 200 || v.Passed {
		t.Fatalf("failing validate: got %d %+v", code, v)
	}
	if code := c.json("POST", "/api/diagnose", `{"text":"short"}`, nil); code != 400 {
		t.Errorf("short diagnosis: got %d, want 400", code)
	}
	if code := c.json("POST", "/api/diagnose", `{"text":"the image does not exist"}`, nil); code != 200 {
		t.Errorf("diagnosis: got %d", code)
	}
	var h web.Hint
	if code := c.json("POST", "/api/hint", "", &h); code != 200 || !strings.Contains(h.HTML, "<code>0/3 ready</code>") {
		t.Errorf("hint: got %d %+v", code, h)
	}
	f.Fix()
	c.json("POST", "/api/validate", "", &v)
	if !v.Passed || !strings.Contains(v.Solution, "<h2>Change</h2>") {
		t.Fatalf("passing validate: got %+v", v)
	}

	var d web.Debrief
	if code := c.json("GET", "/api/debrief/"+v.EpisodeID, "", &d); code != 200 || d.Verdict != "root-cause" || d.FirstWriteKind != "fix-target" {
		t.Fatalf("debrief: got %d %+v", code, d)
	}
	if code := c.json("POST", "/api/debrief/"+v.EpisodeID, `{"matched":true,"ruledOutEarlier":"the Events","nextTimeFirst":"describe pod"}`, &d); code != 200 {
		t.Fatalf("record debrief: got %d", code)
	}
	if d.Answers.RuledOutEarlier != "the Events" || d.Matched == nil || !*d.Matched {
		t.Errorf("recorded debrief: got %+v", d)
	}
	if code := c.json("POST", "/api/debrief/"+v.EpisodeID, `{"ruledOutEarlier":"`+strings.Repeat("x", 501)+`"}`, nil); code != 400 {
		t.Errorf("long answer: got %d, want 400", code)
	}
	if code := c.json("GET", "/api/debrief/nope", "", nil); code != 400 {
		t.Errorf("unknown episode: got %d, want 400", code)
	}

	var cat web.Catalog
	c.json("GET", "/api/catalog", "", &cat)
	if p := cat.Levels[0].Modules[0].Assignments[0].Progress; !p.Practice.Done || !p.Meeting.Done {
		t.Errorf("progress after the practice: got %+v", p)
	}
}

func TestBadgesFollowCleanSolves(t *testing.T) {
	f := newFake()
	f.Passed = true
	c := newClient(t, f)
	badge := func() (web.Badge, web.Badge) {
		var cat web.Catalog
		c.json("GET", "/api/catalog", "", &cat)
		return cat.Levels[0].Modules[0].Badge, cat.Levels[0].Badge
	}
	if m, lv := badge(); m.State != "in-progress" || m.Lit != 3 || m.Caption != "1 of 2 assignments solved clean" || m.Code != "L0.1" || lv.State != "in-progress" {
		t.Fatalf("before: module %+v, level %+v; want L0.1 in progress with 3 lit arcs", m, lv)
	}
	c.start(`{"id":"001","stage":"practice","replace":true}`)
	c.json("POST", "/api/validate", "", nil)
	if m, _ := badge(); m.State != "in-progress" {
		t.Errorf("after a guided solve: got %+v, want still in progress", m)
	}
	c.start(`{"id":"001","stage":"diy","replace":true}`)
	c.json("POST", "/api/validate", "", nil)
	m, lv := badge()
	if m.State != "earned" || m.Lit != 5 || m.EarnedAt == nil || m.Caption != "Every assignment solved clean" {
		t.Errorf("after a clean DIY solve: got %+v, want earned", m)
	}
	if lv.State != "earned" || lv.Caption != "Every module badge earned" {
		t.Errorf("level after every module earned: got %+v", lv)
	}
	var a web.Assignment
	c.json("GET", "/api/assignment/001", "", &a)
	if a.ModuleBadge.State != "earned" || a.ModuleBadge.Code != "L0.1" {
		t.Errorf("assignment module badge: got %+v", a.ModuleBadge)
	}
}

func TestNextOffersActiveThenRecommendation(t *testing.T) {
	c := newClient(t, newFake())
	var n web.Next
	c.json("GET", "/api/next", "", &n)
	if n.Active != nil || n.ScenarioID != webtest.FullID || n.Reason == "" {
		t.Errorf("next without an episode: got %+v", n)
	}
	c.start(`{"id":"001","stage":"practice"}`)
	c.json("GET", "/api/next", "", &n)
	if n.Active == nil || n.Active.Stage != web.StagePractice {
		t.Errorf("next with an episode: got %+v", n)
	}
}

// TestSnapshots pins the JSON the page reads; run with -update after a
// deliberate change and review the diff.
func TestSnapshots(t *testing.T) {
	f := newFake()
	c := newClient(t, f)
	snap := func(name, path string) {
		t.Helper()
		_, body := c.do("GET", path, "", false)
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		got := buf.Bytes()
		file := filepath.Join("testdata", name+".json")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, got, 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v (run go test ./internal/web -run TestSnapshots -update)", file, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s changed; review and run with -update:\n%s", file, got)
		}
	}
	snap("catalog", "/api/catalog")
	snap("assignment", "/api/assignment/001")
	snap("next-idle", "/api/next")
	c.start(`{"id":"001","stage":"practice"}`)
	snap("session-live", "/api/session?live=1")
}

func TestTerminal(t *testing.T) {
	f := newFake()
	f.TerminalArgv = []string{"sh", "-c", `printf 'ready\n'; stty -echo; read line; echo "you said $line"; stty size; sleep 30`}
	c := newClient(t, f)
	u := strings.Replace(c.srv.URL, "http://", "ws://", 1) + "/ws/terminal?cols=91&rows=27"

	t.Run("a foreign origin is refused", func(t *testing.T) {
		cfg, _ := websocket.NewConfig(u, "http://evil.example")
		cfg.Header.Add("Cookie", "kubedrill_token="+testToken)
		if ws, err := websocket.DialConfig(cfg); err == nil {
			ws.Close()
			t.Fatal("dial with a foreign origin succeeded")
		}
	})

	cfg, err := websocket.NewConfig(u, c.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header.Add("Cookie", "kubedrill_token="+testToken)
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	var out strings.Builder
	read := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(out.String(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("terminal output: got %q, want %q", out.String(), want)
			}
			_ = ws.SetReadDeadline(deadline)
			var b []byte
			if err := websocket.Message.Receive(ws, &b); err != nil {
				t.Fatalf("receive: %v (so far %q)", err, out.String())
			}
			out.Write(b)
		}
	}
	read("ready")
	_ = websocket.Message.Send(ws, `{"t":"r","c":100,"r":40}`)
	_ = websocket.Message.Send(ws, `{"t":"i","d":"hi\r"}`)
	read("you said hi")
	read("40 100")
	ws.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.Lock()
		n := len(f.Cleaned)
		var name string
		if n > 0 {
			name = f.Cleaned[0]
		}
		f.Unlock()
		if n == 1 {
			if !strings.HasPrefix(name, "kubedrill-web-") {
				t.Errorf("container name: got %q, want kubedrill-web-<hex>", name)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup not called after the page closed the terminal")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHintErrorIsABadRequest(t *testing.T) {
	f := newFake()
	f.HintErr = errors.New("commit to a cause first")
	c := newClient(t, f)
	c.start(`{"id":"001","stage":"practice"}`)
	if code := c.json("POST", "/api/hint", "", nil); code != 400 {
		t.Errorf("hint error: got %d, want 400", code)
	}
}

func TestExamSubmitRecordsOnlyAPass(t *testing.T) {
	examDone := func(c *client) bool {
		var cat web.Catalog
		c.json("GET", "/api/catalog", "", &cat)
		return cat.Levels[0].Modules[0].Assignments[0].Progress.Exam.Done
	}
	f := newFake()
	c := newClient(t, f)
	if code := c.json("POST", "/api/submit", "", nil); code != 400 {
		t.Errorf("submit without an exam: got %d, want 400", code)
	}
	c.start(`{"id":"001","stage":"exam"}`)
	var v web.Validation
	if code := c.json("POST", "/api/submit", "", &v); code != 200 || v.Passed || v.Outcome != "failed" {
		t.Fatalf("failing submit: got %d %+v", code, v)
	}
	if examDone(c) {
		t.Error("exam recorded after a failed submit")
	}
	c.start(`{"id":"001","stage":"exam"}`)
	f.Fix()
	if code := c.json("POST", "/api/submit", "", &v); code != 200 || !v.Passed || v.Outcome != "solved" {
		t.Fatalf("passing submit: got %d %+v", code, v)
	}
	if !examDone(c) {
		t.Error("exam not recorded after a passing submit")
	}
}

func TestDebriefRecordsARunEndedOutsideThePage(t *testing.T) {
	f := newFake()
	c := newClient(t, f)
	_, sess, _, _ := c.start(`{"id":"001","stage":"exam"}`)
	id := sess.Active.EpisodeID
	if code := c.json("GET", "/api/debrief/"+id, "", nil); code != 400 {
		t.Errorf("debrief of a running exam: got %d, want 400", code)
	}
	f.Fix()
	f.Finish()
	var d web.Debrief
	if code := c.json("GET", "/api/debrief/"+id, "", &d); code != 200 || d.Outcome != "solved" || d.Stage != web.StageExam {
		t.Fatalf("debrief: got %d %+v", code, d)
	}
	var cat web.Catalog
	c.json("GET", "/api/catalog", "", &cat)
	if p := cat.Levels[0].Modules[0].Assignments[0].Progress; !p.Exam.Done {
		t.Errorf("exam progress after a solve outside the page: got %+v", p.Exam)
	}
}

func TestWelcomeGate(t *testing.T) {
	c := newClient(t, newFake())
	var m struct {
		Onboarded bool `json:"onboarded"`
	}
	if code := c.json("GET", "/api/meta", "", &m); code != 200 || m.Onboarded {
		t.Fatalf("meta before the welcome flow: got %d onboarded=%v, want false", code, m.Onboarded)
	}
	if code := c.json("POST", "/api/onboarded", "", nil); code != 200 {
		t.Fatalf("onboarded: got %d", code)
	}
	if c.json("GET", "/api/meta", "", &m); !m.Onboarded {
		t.Error("meta after the welcome flow: onboarded=false, want true")
	}
	s := web.New(web.Deps{Backend: newFake(), ProgressPath: filepath.Join(t.TempDir(), "progress.json"), Token: testToken, Preview: "001"})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	p := &client{t: t, srv: srv, cookie: &http.Cookie{Name: "kubedrill_token", Value: testToken}}
	if p.json("GET", "/api/meta", "", &m); !m.Onboarded {
		t.Error("meta in preview: onboarded=false, want true (no welcome flow for authors)")
	}
}

func TestServeClosesTerminalsOnShutdown(t *testing.T) {
	f := newFake()
	f.TerminalArgv = []string{"sh", "-c", `printf 'ready\n'; sleep 60`}
	s := web.New(web.Deps{Backend: f, ProgressPath: filepath.Join(t.TempDir(), "progress.json"), Token: testToken, Now: fixedNow})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, ln) }()
	cfg, err := websocket.NewConfig("ws://"+ln.Addr().String()+"/ws/terminal?cols=80&rows=24", "http://"+ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header.Add("Cookie", "kubedrill_token="+testToken)
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var b []byte
	if err := websocket.Message.Receive(ws, &b); err != nil {
		t.Fatalf("receive: %v", err)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	f.Lock()
	n := len(f.Cleaned)
	f.Unlock()
	if n != 1 {
		t.Errorf("terminal cleanups after shutdown: got %d, want 1", n)
	}
}
