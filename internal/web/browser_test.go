//go:build browser

package web_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/cauebraz/kubedrill/internal/web"
	"github.com/cauebraz/kubedrill/internal/web/webtest"
)

// The browser test drives headless Chrome through the DevTools protocol
// against the workspace on the in-memory backend:
//
//	go test -tags browser ./internal/web -run TestBrowser
//
// CHROME names the browser binary (default: Google Chrome on macOS, then
// google-chrome and chromium on PATH); KUBEDRILL_SHOTS, when set, is a
// directory that receives a screenshot of each screen in both themes.

type browser struct {
	t      *testing.T
	ws     *websocket.Conn
	id     int
	dir    string
	errors []string
}

func chromePath() string {
	if p := os.Getenv("CHROME"); p != "" {
		return p
	}
	if p := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; fileExists(p) {
		return p
	}
	for _, n := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func newBrowser(t *testing.T, start string) *browser {
	t.Helper()
	bin := chromePath()
	if bin == "" {
		t.Skip("no Chrome found; set CHROME")
	}
	port := 9400 + os.Getpid()%400
	cmd := exec.Command(bin, "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir="+t.TempDir(), fmt.Sprintf("--remote-debugging-port=%d", port), "--remote-allow-origins=*", "--window-size=1440,900", "about:blank")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start chrome: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var target struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	for i := 0; i < 100 && target.WS == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("http://127.0.0.1:%d/json/new?%s", port, url.QueryEscape(start)), nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		_ = json.NewDecoder(res.Body).Decode(&target)
		res.Body.Close()
	}
	if target.WS == "" {
		t.Fatal("chrome did not open a target")
	}
	ws, err := websocket.Dial(target.WS, "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("devtools: %v", err)
	}
	b := &browser{t: t, ws: ws, dir: os.Getenv("KUBEDRILL_SHOTS")}
	b.call("Runtime.enable", nil)
	b.call("Page.enable", nil)
	return b
}

func (b *browser) call(method string, params any) json.RawMessage {
	b.t.Helper()
	b.id++
	if params == nil {
		params = map[string]any{}
	}
	msg, _ := json.Marshal(map[string]any{"id": b.id, "method": method, "params": params})
	if err := websocket.Message.Send(b.ws, string(msg)); err != nil {
		b.t.Fatalf("%s: %v", method, err)
	}
	_ = b.ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		var raw string
		if err := websocket.Message.Receive(b.ws, &raw); err != nil {
			b.t.Fatalf("%s: %v", method, err)
		}
		var r struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(raw), &r)
		if r.Method == "Runtime.exceptionThrown" {
			var p struct {
				Details struct {
					Text      string `json:"text"`
					Exception struct {
						Description string `json:"description"`
					} `json:"exception"`
				} `json:"exceptionDetails"`
			}
			_ = json.Unmarshal(r.Params, &p)
			b.errors = append(b.errors, p.Details.Text+" "+p.Details.Exception.Description)
		}
		if r.ID == b.id {
			if r.Error != nil {
				b.t.Fatalf("%s: %s", method, r.Error.Message)
			}
			return r.Result
		}
	}
}

// eval runs expr (awaiting a promise) and returns its value as a string.
func (b *browser) eval(expr string) string {
	b.t.Helper()
	res := b.call("Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true})
	var v struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(res, &v)
	if v.Exception != nil {
		b.t.Fatalf("eval %s: %s %s", expr, v.Exception.Text, v.Exception.Exception.Description)
	}
	switch x := v.Result.Value.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		out, _ := json.Marshal(x)
		return string(out)
	}
}

// wait polls expr until it is truthy.
func (b *browser) wait(what, expr string) {
	b.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if v := b.eval("!!(" + expr + ")"); v == "true" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	b.t.Fatalf("waiting for %s: %s stayed false\nerrors: %v\npage: %s", what, expr, b.errors, b.eval("(document.querySelector('.stage') || document.querySelector('#app')).innerText.slice(-900)"))
}

func (b *browser) text(sel string) string {
	return b.eval(fmt.Sprintf("(document.querySelector(%q) || {}).innerText || ''", sel))
}

func (b *browser) click(sel string) {
	b.t.Helper()
	if b.eval(fmt.Sprintf("(() => { const e = document.querySelector(%q); if (!e) return 'missing'; e.click(); return 'ok'; })()", sel)) != "ok" {
		b.t.Fatalf("click %s: no such element\npage: %s", sel, b.text("#app"))
	}
}

// clickText clicks the first button or link whose text contains s.
func (b *browser) clickText(s string) {
	b.t.Helper()
	js := fmt.Sprintf("(() => { const e = [...document.querySelectorAll('button, a')].find((x) => x.textContent.includes(%q) && !x.disabled); if (!e) return 'missing'; e.click(); return 'ok'; })()", s)
	if b.eval(js) != "ok" {
		b.t.Fatalf("click %q: no such button\npage: %s", s, b.text("body"))
	}
}

// key dispatches a real key press; mods is 0, 2 (ctrl), 4 (meta) or 8 (shift).
func (b *browser) key(key string, mods int) {
	b.t.Helper()
	text := key
	code := 0
	switch key {
	case "Enter":
		text, code = "\r", 13
	case "Escape":
		text, code = "", 27
	}
	params := map[string]any{"type": "keyDown", "key": key, "modifiers": mods, "windowsVirtualKeyCode": code, "text": text}
	if mods&(2|4) != 0 {
		delete(params, "text")
	}
	b.call("Input.dispatchKeyEvent", params)
	b.call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "modifiers": mods, "windowsVirtualKeyCode": code})
}

func (b *browser) go_(hash string) { b.eval(fmt.Sprintf("location.hash = %q", hash)) }

func (b *browser) shot(name string) {
	if b.dir == "" {
		return
	}
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		b.t.Fatal(err)
	}
	for _, theme := range []string{"dark", "light"} {
		if theme == "light" {
			b.eval("document.documentElement.setAttribute('data-theme', 'light')")
		} else {
			b.eval("document.documentElement.removeAttribute('data-theme')")
		}
		time.Sleep(150 * time.Millisecond)
		res := b.call("Page.captureScreenshot", map[string]any{"format": "png"})
		var s struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(res, &s)
		data, _ := base64.StdEncoding.DecodeString(s.Data)
		_ = os.WriteFile(filepath.Join(b.dir, name+"-"+theme+".png"), data, 0o644)
	}
	b.eval("document.documentElement.removeAttribute('data-theme')")
}

func modKey() int {
	return 2
}

func TestBrowser(t *testing.T) {
	fake := webtest.New()
	fake.TerminalArgv = []string{"/bin/sh", "-i"}
	s := web.New(web.Deps{Backend: fake, ProgressPath: filepath.Join(t.TempDir(), "progress.json"), Token: testToken, Log: testWriter{t}, StaticDir: "static"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	b := newBrowser(t, srv.URL+"/?t="+testToken+"#/")
	b.wait("welcome gate", "location.hash === '#/welcome' && document.querySelector('.welcome h1')")
	if v := b.eval("String(document.body.classList.contains('onboarding') && getComputedStyle(document.querySelector('.nav')).display === 'none')"); v != "true" {
		t.Errorf("nav during the welcome flow: hidden=%s, want true", v)
	}
	b.shot("00-welcome")
	b.key("Enter", 0)
	b.wait("step 1", "document.querySelector('.step-num') && document.querySelector('.step-num').textContent === 'Step 1 of 5'")
	b.key("1", 0)
	b.wait("retry feedback", "document.querySelector('.feedback.retry')")
	if v := b.eval("String(!!document.querySelector('.welcome-actions .btn.primary').hidden)"); v != "true" {
		t.Errorf("next step offered after a wrong pick: hidden=%s, want true", v)
	}
	b.key("2", 0)
	b.wait("good feedback", "document.querySelector('.feedback.good') && !document.querySelector('.welcome-actions .btn.primary').hidden")
	b.shot("00b-welcome-step1")
	for n, right := range []string{"1", "1", "1", "1"} {
		b.key("Enter", 0)
		b.wait(fmt.Sprintf("step %d", n+2), fmt.Sprintf("document.querySelector('.step-num') && document.querySelector('.step-num').textContent === 'Step %d of 5'", n+2))
		b.key(right, 0)
		b.wait("good feedback", "document.querySelector('.feedback.good')")
		if n == 1 {
			b.wait("reveal", "document.querySelector('.reveal pre')")
			b.shot("00c-welcome-step3")
		}
	}
	b.key("Enter", 0)
	b.wait("close", "document.querySelector('.welcome .map')")
	b.shot("00d-welcome-close")
	b.key("Enter", 0)
	b.wait("home", "location.hash === '#/' && document.querySelector('.continue h2')")
	if v := b.eval("String(document.body.classList.contains('onboarding'))"); v != "false" {
		t.Error("the onboarding class stayed on the body after the flow")
	}
	b.go_("#/welcome")
	b.wait("welcome replays", "document.querySelector('.welcome h1')")
	b.go_("#/")
	b.wait("home", "document.querySelector('.continue h2')")
	if got := b.text(".continue"); !strings.Contains(got, "Recommended next") || !strings.Contains(got, "Storefront is down") {
		t.Errorf("continue card: got %q", got)
	}
	b.shot("01-home")

	sub := func(name string, fn func(t *testing.T)) {
		t.Run(name, func(t *testing.T) {
			parent := b.t
			b.t = t
			defer func() { b.t = parent }()
			fn(t)
		})
	}
	sub("catalog filters and collapses", func(t *testing.T) {
		b.go_("#/catalog")
		b.wait("catalog", "document.querySelector('.filters input')")
		if v := b.eval("document.querySelector('.empty-level details, details.empty-level') ? document.querySelector('details.empty-level').open : 'none'"); v != "false" {
			t.Errorf("empty L2 level: open=%s, want collapsed", v)
		}
		b.eval("(() => { const i = document.querySelector('.filters input'); i.value = 'crash'; i.dispatchEvent(new Event('input')); })()")
		b.wait("search applied", "document.querySelectorAll('.assign-row').length === 1")
		if !strings.Contains(b.eval("location.hash"), "q=crash") {
			t.Errorf("search not in the URL: %s", b.eval("location.hash"))
		}
		b.shot("02-catalog-search")
		b.clickText("Clear filters")
		b.wait("filters cleared", "document.querySelectorAll('.assign-row').length === 5")
		if b.eval("document.querySelectorAll('.assign-row .lock').length") != "1" {
			t.Errorf("lock on 004 (requires 003): got %s locks", b.eval("document.querySelectorAll('.assign-row .lock').length"))
		}
	})

	sub("exams", func(t *testing.T) {
		b.go_("#/exams")
		b.wait("exams", "document.querySelectorAll('.domain').length === 4")
		b.shot("03-exams")
	})

	sub("meeting by keyboard", func(t *testing.T) {
		b.go_("#/a/001-storefront-down/meeting")
		b.wait("meeting", "document.querySelectorAll('.choices .choice').length === 2")
		b.shot("04-meeting")
		b.key("2", 0)
		b.wait("retry feedback", "document.querySelector('.feedback.retry')")
		b.key("1", 0)
		b.wait("turn 2", "document.querySelector('.board li:not(.empty)')")
		b.key("2", 0)
		b.wait("meeting complete", "document.querySelector('.choices').textContent.includes('Meeting complete: 1 of 2')")
		b.key("Enter", 0)
		b.wait("quiz", "location.hash.endsWith('/quiz')")
	})

	sub("quiz resumes after a reload", func(t *testing.T) {
		b.wait("question 1", "document.querySelectorAll('.quiz-card .choice').length === 2")
		b.key("2", 0)
		b.wait("wrong", "document.querySelector('.choice.wrong')")
		b.key("1", 0)
		b.wait("right", "document.querySelector('.choice.right')")
		b.eval("location.reload()")
		b.wait("restored answer", "document.querySelector('.choice.right')")
		b.key("Enter", 0)
		b.wait("question 2", "document.querySelector('.quiz-card .q').textContent.includes('Where does the fix')")
		b.key("2", 0)
		b.key("Enter", 0)
		b.wait("quiz complete", "document.querySelector('.quiz-card').textContent.includes('1 of 2 right')")
		b.shot("05-quiz-done")
		b.key("Enter", 0)
		b.wait("learn", "location.hash.endsWith('/learn')")
	})

	sub("learn and concepts", func(t *testing.T) {
		b.wait("diagram", "document.querySelector('.diagram-wrap svg')")
		b.clickText("Pod lifecycle")
		b.wait("concept drawer", "document.querySelector('.drawer .prose strong')")
		b.shot("06-concept")
		b.key("Escape", 0)
		b.wait("drawer closed", "!document.querySelector('.drawer')")
		b.key("Enter", 0)
		b.wait("practice launch", "document.querySelector('.launch')")
	})

	sub("practice run", func(t *testing.T) {
		b.shot("07-launch")
		b.key("Enter", 0)
		b.wait("workspace", "document.querySelector('.lab .xterm')")
		b.wait("terminal connected", "document.querySelector('.term-bar .state.on')")
		b.wait("prompt", "document.querySelector('.xterm-rows').textContent.includes('learner@fake')")
		b.wait("live checks", "document.querySelector('.checks .sum') && document.querySelector('.checks .sum').textContent === 'Checks 1/3'")
		b.clickText("Type it")
		b.wait("command typed", "document.querySelector('.xterm-rows').textContent.includes('kubectl get pods')")
		b.shot("08-practice")
		b.key("=", modKey())
		b.wait("terminal zoom stored", "localStorage.getItem('kd:term-font') === '14'")
		b.key("-", modKey())
		b.wait("terminal zoom back", "localStorage.getItem('kd:term-font') === '13'")
		b.click(".checks button.chk.fail")
		b.wait("check popover", "document.querySelector('.popover') && document.querySelector('.popover').textContent.includes('expected')")
		b.key("Escape", 0)
		b.wait("popover closed", "!document.querySelector('.popover')")
		b.key(".", modKey())
		b.wait("mentor", "document.querySelector('.drawer .mentor-msg.note')")
		b.clickText("Ask for hint 1")
		b.wait("hint", "document.querySelectorAll('.drawer .mentor-msg').length === 3")
		b.shot("09-mentor")
		b.key("Escape", 0)
		b.wait("hint pill", "!document.querySelector('.foot-actions .pill').hidden")
		b.go_("#/")
		b.wait("resume card", "document.querySelector('.continue.live') && document.querySelector('.continue').textContent.includes('Resume')")
		b.shot("09b-home-live")
		b.clickText("Resume")
		b.wait("back in the lab", "document.querySelector('.lab .xterm') && document.querySelector('.term-bar .state.on')")
		b.key("Enter", modKey())
		b.wait("failure modal", "document.querySelector('.modal h2') && document.querySelector('.modal h2').textContent === 'Not there yet'")
		b.shot("10-not-yet")
		b.key("Escape", 0)
		fake.Fix()
		b.wait("checks pass", "document.querySelector('.checks .sum').textContent === 'Checks 3/3'")
		b.wait("guide green", "document.querySelectorAll('.gstep.done').length === 4")
		b.key("Enter", modKey())
		b.wait("debrief", "location.hash.includes('/debrief/')")
	})

	sub("debrief", func(t *testing.T) {
		b.wait("verdict", "document.querySelector('.facts .pill.good')")
		b.shot("11-debrief")
		b.eval("document.querySelectorAll('.reflection textarea')[0].value = 'the Failed to pull image event'")
		b.clickText("Save and continue")
		b.wait("diy launch", "location.hash.endsWith('/diy') && document.querySelector('.launch')")
	})

	sub("diy run and badge", func(t *testing.T) {
		b.key("Enter", 0)
		b.wait("diy workspace", "document.querySelector('.lab .side-tabs')")
		if tabs := b.text(".side-tabs"); strings.Contains(tabs, "Steps") || !strings.Contains(tabs, "Ticket") {
			t.Errorf("DIY tabs: got %q, want Ticket and Story only", tabs)
		}
		b.key("Enter", modKey())
		b.wait("diy note", "document.querySelector('.modal .callout')")
		b.key("Escape", 0)
		fake.Fix()
		b.wait("checks pass", "document.querySelector('.checks .sum').textContent === 'Checks 3/3'")
		b.key("Enter", modKey())
		b.wait("debrief", "location.hash.includes('/debrief/')")
		b.clickText("Skip for now")
		b.wait("result", "document.querySelector('.done-card .badge:not(.locked)')")
		b.shot("12-result")
		b.go_("#/")
		b.wait("home updated", "document.querySelector('.profile').textContent.includes('1 of 5')")
	})

	sub("exam rehearsal", func(t *testing.T) {
		b.go_("#/a/001-storefront-down/exam")
		b.wait("exam launch", "document.querySelector('.launch') && document.querySelector('.launch').textContent.includes('against the clock')")
		b.shot("16-exam-launch")
		b.key("Enter", 0)
		b.wait("exam workspace", "document.querySelector('.lab .xterm') && document.querySelector('.term-bar .timer').textContent.includes('left')")
		if v := b.eval("document.querySelector('.foot-actions .btn:not(.primary)').hidden"); v != "true" {
			t.Errorf("mentor button in an exam: hidden=%s, want true", v)
		}
		if got := b.text(".side-tabs"); strings.Contains(got, "Steps") || strings.Contains(got, "Story") {
			t.Errorf("exam tabs: got %q, want the ticket only", got)
		}
		b.wait("exam checks note", "document.querySelector('.checks').textContent.includes('run once, when you submit')")
		b.shot("17-exam")
		b.key("Enter", modKey())
		b.wait("confirm", "document.querySelector('.modal-title') && document.querySelector('.modal-title').textContent === 'Submit now?'")
		b.clickText("Submit and end the exam")
		b.wait("exam debrief", "location.hash.includes('/debrief/') && document.querySelector('.debrief-hero h1')")
		if got := b.text(".debrief-hero h1"); got != "Exam not passed" {
			t.Errorf("exam debrief title: got %q", got)
		}
		b.wait("walkthrough", "document.querySelector('.walkthrough')")
		b.shot("18-exam-debrief")
		b.clickText("Skip for now")
		b.wait("result", "location.hash.endsWith('/done')")
	})

	sub("a stray Enter keeps the exam and the deadline submits it", func(t *testing.T) {
		fake.Lock()
		fake.ExamTime = 10 * time.Second
		fake.Unlock()
		defer func() { fake.Lock(); fake.ExamTime = 0; fake.Unlock() }()
		b.go_("#/a/001-storefront-down/exam")
		b.wait("exam launch", "document.querySelector('.launch')")
		b.key("Enter", 0)
		b.wait("exam workspace", "document.querySelector('.term-bar .timer[role=timer]')")
		b.key("Enter", modKey())
		b.wait("confirm", "document.querySelector('.modal-title')")
		b.key("Enter", modKey())
		if n := b.eval("String(document.querySelectorAll('.modal-title').length)"); n != "1" {
			t.Errorf("confirm dialogs after a second Cmd/Ctrl+Enter: got %s, want 1", n)
		}
		if got := b.eval("document.activeElement.textContent"); got != "Keep working" {
			t.Errorf("focus in the confirm: got %q, want Keep working", got)
		}
		b.key("Enter", 0)
		b.wait("confirm closed by Enter", "!document.querySelector('.modal-title')")
		b.eval("document.activeElement.blur()")
		b.key("Enter", 0)
		if b.eval("String(!!document.querySelector('.lab .xterm'))") != "true" {
			t.Error("Enter outside the terminal left the workspace")
		}
		b.key("Enter", modKey())
		b.wait("confirm again", "document.querySelector('.modal-title')")
		b.wait("auto-submit", "location.hash.includes('/debrief/') && document.querySelector('.debrief-hero h1')")
		if got := b.text(".debrief-hero h1"); got != "Time is up" {
			t.Errorf("debrief after the deadline: got %q, want Time is up", got)
		}
		if b.eval("String(!!document.querySelector('.modal-back'))") != "false" {
			t.Error("the confirm dialog stayed open over the debrief")
		}
	})

	sub("a run ended in the terminal opens its debrief", func(t *testing.T) {
		b.go_("#/a/001-storefront-down/practice")
		b.wait("practice launch", "document.querySelector('.launch')")
		b.key("Enter", 0)
		b.wait("workspace", "document.querySelector('.lab .xterm')")
		fake.Fix()
		fake.Finish()
		b.wait("debrief", "location.hash.includes('/debrief/') && document.querySelector('.debrief-hero h1')")
		if got := b.text(".debrief-hero h1"); got != "The customer is happy" {
			t.Errorf("debrief title: got %q", got)
		}
		b.go_("#/a/001-storefront-down/exam")
		b.wait("exam launch", "document.querySelector('.launch')")
		b.key("Enter", 0)
		b.wait("exam workspace", "document.querySelector('.lab .xterm')")
		fake.Finish()
		b.wait("exam debrief", "location.hash.includes('/debrief/') && document.querySelector('.debrief-hero h1')")
		b.go_("#/a/001-storefront-down/exam")
		b.wait("a fresh exam launch, not the old debrief", "location.hash.endsWith('/exam') && document.querySelector('.launch')")
	})

	sub("navigation shortcuts", func(t *testing.T) {
		b.go_("#/")
		b.wait("home", "document.querySelector('.continue')")
		b.key("c", 0)
		b.wait("catalog by c", "location.hash.startsWith('#/catalog') && document.querySelector('.filters input')")
		b.key("/", 0)
		b.wait("search focused", "document.activeElement === document.querySelector('.filters input')")
		b.go_("#/a/001-storefront-down/quiz")
		b.wait("quiz", "document.querySelector('.quiz-card')")
		b.key("ArrowRight", modKey())
		b.wait("next stage", "location.hash.endsWith('/learn') && document.querySelector('.diagram-wrap svg')")
		b.key("ArrowLeft", modKey())
		b.wait("previous stage", "location.hash.endsWith('/quiz')")
	})

	sub("narrow windows", func(t *testing.T) {
		for _, w := range []int{390, 900} {
			b.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": 844, "deviceScaleFactor": 1, "mobile": w < 600})
			for _, h := range []string{"#/", "#/catalog", "#/a/001-storefront-down/meeting", "#/welcome"} {
				b.go_(h)
				b.wait("render "+h, "document.querySelector('.page-inner, .stage-inner, .welcome-inner') && !document.querySelector('.skeleton')")
				if over := b.eval("document.documentElement.scrollWidth > window.innerWidth + 1"); over != "false" {
					t.Errorf("%s at %dpx scrolls horizontally (scrollWidth %s)", h, w, b.eval("String(document.documentElement.scrollWidth)"))
				}
				clipped := b.eval(`[...document.querySelectorAll('.card, .topbar button, .topbar a')].filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > window.innerWidth + 1; }).map((e) => e.className).join(', ')`)
				if clipped != "" {
					t.Errorf("%s at %dpx: elements past the right edge: %s", h, w, clipped)
				}
			}
			b.go_("#/catalog")
			b.wait("catalog", "document.querySelector('.filters')")
			b.shot(fmt.Sprintf("15-catalog-%dpx", w))
		}
		b.call("Emulation.clearDeviceMetricsOverride", nil)
	})

	sub("shortcuts help and kit", func(t *testing.T) {
		b.key("?", 0)
		b.wait("help", "document.querySelector('.shortcuts')")
		b.key("Escape", 0)
		b.go_("#/kit")
		b.wait("kit", "document.querySelector('.kit-section')")
		b.shot("13-kit")
	})

	sub("a stopped server shows a banner", func(t *testing.T) {
		b.go_("#/")
		b.wait("home", "document.querySelector('.continue')")
		srv.CloseClientConnections()
		srv.Close()
		b.go_("#/catalog")
		b.eval("location.hash = '#/'")
		b.wait("banner", "!document.querySelector('#banner').hidden && document.querySelector('#banner').textContent.includes('stopped answering')")
		b.shot("14-server-down")
	})
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
