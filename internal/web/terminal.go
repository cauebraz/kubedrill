package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/websocket"
)

// terminalMessage is a message from the page: t "i" carries input in d,
// t "r" a new size in c (columns) and r (rows).
type terminalMessage struct {
	T string `json:"t"`
	D string `json:"d,omitempty"`
	C uint16 `json:"c,omitempty"`
	R uint16 `json:"r,omitempty"`
}

// terminal upgrades to a websocket and runs one workstation shell in a
// pseudo-terminal for it: pty output goes to the page as binary frames,
// page messages go to the pty. The session ends when either side closes;
// the shell's process group is killed and the backend cleanup removes the
// container.
func (s *Server) terminal() http.Handler {
	return websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) error {
			if origin := r.Header.Get("Origin"); origin != "http://"+r.Host {
				return fmt.Errorf("terminal: origin %q is not this server", origin)
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			defer ws.Close()
			r := ws.Request()
			cols := parseSize(r.URL.Query().Get("cols"), 120)
			rows := parseSize(r.URL.Query().Get("rows"), 32)
			if err := s.runTerminal(r.Context(), ws, cols, rows); err != nil {
				fmt.Fprintf(s.d.Log, "web: terminal: %v\n", err)
				_ = websocket.Message.Send(ws, []byte("\r\n[kubedrill] "+err.Error()+"\r\n"))
			}
		},
	}
}

func parseSize(v string, def uint16) uint16 {
	n, err := strconv.Atoi(v)
	if err != nil || n < 10 || n > 1000 {
		return def
	}
	return uint16(n)
}

func (s *Server) runTerminal(ctx context.Context, ws *websocket.Conn, cols, rows uint16) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	cmd, cleanup, err := s.d.Backend.Terminal(ctx, containerName())
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	cmd.Cancel = nil
	master, err := startInPTY(cmd, cols, rows)
	if err != nil {
		return err
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	stop := func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		select {
		case <-waited:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-waited
		}
	}

	out := make(chan error, 1)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				if werr := websocket.Message.Send(ws, buf[:n]); werr != nil {
					out <- werr
					return
				}
			}
			if err != nil {
				out <- err
				return
			}
		}
	}()
	in := make(chan error, 1)
	go func() { in <- readInput(ws, master) }()

	outDone, inDone := false, false
	select {
	case <-out:
		outDone = true
	case <-in:
		inDone = true
	case <-ctx.Done():
	}
	stop()
	master.Close()
	ws.Close()
	if !outDone {
		<-out
	}
	if !inDone {
		<-in
	}
	return nil
}

// readInput copies page messages into the pty until the websocket closes.
func readInput(ws *websocket.Conn, master *os.File) error {
	for {
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var m terminalMessage
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		switch m.T {
		case "i":
			if _, err := master.Write([]byte(m.D)); err != nil {
				return err
			}
		case "r":
			_ = setSize(master, m.C, m.R)
		}
	}
}

// containerName is a fresh workstation container name for one terminal.
func containerName() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "kubedrill-web-" + hex.EncodeToString(b)
}
