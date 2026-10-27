// Command devserver serves the browser workspace over the in-memory backend
// of webtest, so the page can be worked on without a cluster or Docker:
//
//	go run ./internal/web/devserver
//
// It serves the files of internal/web/static from disk (edits show on
// reload) and prints the link with its token. The terminal runs a local
// sh; "kubectl" in it is not wired. Fix in the page's console, or
// POST /dev/fix, makes the live checks pass.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cauebraz/kubedrill/internal/web"
	"github.com/cauebraz/kubedrill/internal/web/webtest"
)

func main() {
	port := flag.Int("port", 8650, "port on 127.0.0.1")
	static := flag.String("static-dir", filepath.Join("internal", "web", "static"), "directory of the page files")
	delay := flag.Duration("step-delay", 600*time.Millisecond, "pause between the launch steps")
	flag.Parse()
	if err := run(*port, *static, *delay); err != nil {
		fmt.Fprintln(os.Stderr, "devserver:", err)
		os.Exit(1)
	}
}

func run(port int, static string, delay time.Duration) error {
	if _, err := os.Stat(filepath.Join(static, "index.html")); err != nil {
		return fmt.Errorf("%s has no index.html; run from the repository root or pass --static-dir", static)
	}
	fake := webtest.New()
	fake.StepDelay = delay
	data, err := os.MkdirTemp("", "kubedrill-devserver-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(data)
	const token = "dev"
	srv := web.New(web.Deps{Backend: fake, ProgressPath: filepath.Join(data, "progress.json"), Token: token, Log: os.Stderr, StaticDir: static})
	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler())
	mux.HandleFunc("POST /dev/fix", func(w http.ResponseWriter, _ *http.Request) {
		fake.Fix()
		w.WriteHeader(http.StatusNoContent)
	})
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	fmt.Printf("devserver: http://127.0.0.1:%d/?t=%s (fake backend, files from %s)\n", port, token, static)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
