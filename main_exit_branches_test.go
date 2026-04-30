package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/daemon"
	"github.com/leolin310148/borz/internal/protocol"
)

type exitPanic struct{ code int }

func expectExit(t *testing.T, want int, fn func()) {
	t.Helper()
	old := exitFunc
	exitFunc = func(code int) { panic(exitPanic{code: code}) }
	defer func() { exitFunc = old }()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected exit %d", want)
		}
		if got, ok := r.(exitPanic); !ok || got.code != want {
			t.Fatalf("exit panic = %#v, want code %d", r, want)
		}
	}()
	fn()
}

func TestExitBackedFatalBranches(t *testing.T) {
	expectExit(t, 0, func() {
		oldArgs := os.Args
		os.Args = []string{"borz"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	expectExit(t, 0, func() {
		oldArgs := os.Args
		os.Args = []string{"borz", "--json"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	expectExit(t, 1, func() {
		oldArgs := os.Args
		os.Args = []string{"borz", "opne"}
		defer func() { os.Args = oldArgs }()
		main()
	})

	for _, tc := range []struct {
		name string
		fn   func()
	}{
		{"open missing", func() { runMainArgsForExit("open") }},
		{"fill missing", func() { runMainArgsForExit("fill", "ref") }},
		{"type missing", func() { runMainArgsForExit("type", "ref") }},
		{"select missing", func() { runMainArgsForExit("select", "ref") }},
		{"eval missing", func() { runMainArgsForExit("eval") }},
		{"eval file missing", func() { runMainArgsForExit("eval", "--file", filepath.Join(t.TempDir(), "missing.js")) }},
		{"eval file with inline", func() {
			p := filepath.Join(t.TempDir(), "script.js")
			if err := os.WriteFile(p, []byte("1+1"), 0o644); err != nil {
				t.Fatal(err)
			}
			runMainArgsForExit("eval", "--file", p, "1+1")
		}},
		{"eval bad json arg", func() { runMainArgsForExit("eval", "--json-arg", "bad", "1+1") }},
		{"get missing", func() { runMainArgsForExit("get") }},
		{"press missing", func() { runMainArgsForExit("press") }},
		{"record info missing", func() { handleRecord([]string{"info"}, []string{"record", "info"}, false) }},
		{"record render missing", func() { handleRecord([]string{"render"}, []string{"record", "render"}, false) }},
		{"record export missing", func() { handleRecord([]string{"export"}, []string{"record", "export"}, false) }},
		{"record redact missing", func() { handleRecord([]string{"redact"}, []string{"record", "redact"}, false) }},
		{"record export bad format", func() {
			handleRecord([]string{"export", "x.borzrec"}, []string{"record", "export", "x.borzrec", "--format", "mp4"}, false)
		}},
		{"record redact no action", func() {
			handleRecord([]string{"redact", "x.borzrec"}, []string{"record", "redact", "x.borzrec"}, false)
		}},
		{"record unknown", func() { handleRecord([]string{"bogus"}, []string{"record", "bogus"}, false) }},
		{"site search missing", func() { handleSite([]string{"search"}, false, "") }},
		{"site info missing", func() { handleSite([]string{"info"}, false, "") }},
		{"site info not found", func() { handleSite([]string{"info", "missing/site"}, false, "") }},
		{"site new missing", func() { handleSite([]string{"new"}, false, "") }},
		{"site lint missing", func() { handleSite([]string{"lint"}, false, "") }},
		{"site trust missing", func() { handleSite([]string{"trust"}, false, "") }},
		{"site run missing", func() { handleSite([]string{"run"}, false, "") }},
		{"site unknown", func() { handleSite([]string{"bogus"}, false, "") }},
		{"site run not found", func() { handleSiteRun("missing/site", nil, false, "") }},
		{"tab unknown", func() { handleTab([]string{"bogus"}, false, "", []string{"tab", "bogus"}) }},
		{"fetch missing", func() {
			oldArgs := os.Args
			os.Args = []string{"borz", "fetch"}
			defer func() { os.Args = oldArgs }()
			main()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectExit(t, 1, tc.fn)
		})
	}
}

func runMainArgsForExit(args ...string) {
	oldArgs := os.Args
	os.Args = append([]string{"borz"}, args...)
	defer func() { os.Args = oldArgs }()
	main()
}

func TestServiceAndServerExitBranches(t *testing.T) {
	for _, args := range [][]string{
		{"install"}, {"uninstall"}, {"remove"}, {"start"}, {"stop"}, {"status"}, {"run"}, {"bogus"},
	} {
		t.Run("service "+strings.Join(args, "-"), func(t *testing.T) {
			expectExit(t, 1, func() { handleService(args, append([]string{"service"}, args...)) })
		})
	}
	client.ResetForTests()
	t.Setenv("BORZ_HOME", t.TempDir())
	expectExit(t, 1, func() { handleDaemon([]string{"stop"}, []string{"daemon", "stop"}) })
	expectExit(t, 1, func() { handleServer([]string{"stop"}, []string{"server", "stop"}) })
	expectExit(t, 1, func() { handleServer(nil, []string{"server", "--host", "0.0.0.0"}) })

	old := newDaemonServer
	newDaemonServer = func(daemon.ServerOptions) daemonRunner {
		return stubDaemonRunner{run: func() error { return errors.New("run failed") }}
	}
	t.Cleanup(func() { newDaemonServer = old })
	expectExit(t, 1, func() { startDaemonForeground([]string{"--port", "22222"}) })
	expectExit(t, 1, func() { handleServer(nil, []string{"server", "--host", "127.0.0.1", "--port", "22223"}) })
}

func TestPrintExitBranches(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/command":
			w.Write([]byte(`{"id":"x","success":false,"error":"command failed"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	req := &protocol.Request{ID: "x", Action: protocol.ActionGet}
	expectExit(t, 1, func() { sendAndPrint(req, false, nil) })
	expectExit(t, 1, func() { printEval(req, false, false) })

	newFakeDaemon(t)
	expectExit(t, 1, func() {
		sendPrepareAndPrint(req, false, func(*protocol.Response) error {
			return fmt.Errorf("prepare failed")
		}, nil)
	})
}
