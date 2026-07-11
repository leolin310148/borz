package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
)

const (
	e2eEnabledEnv       = "BORZ_E2E"
	e2eLegacyEnabledEnv = "BB_BROWSER_E2E"
)

func TestE2ECLIHelper(t *testing.T) {
	if os.Getenv("BORZ_E2E_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"borz"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	fmt.Fprintln(os.Stderr, "missing -- before helper command args")
	os.Exit(2)
}

type e2eDaemonEnv struct {
	home string
}

func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("local Chrome e2e tests are disabled in GitHub Actions")
	}
	if os.Getenv(e2eEnabledEnv) != "1" && os.Getenv(e2eLegacyEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run local Chrome e2e tests", e2eEnabledEnv)
	}
}

func startE2EDaemon(t *testing.T, home string) e2eDaemonEnv {
	t.Helper()

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	port := freeTCPPort(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(os.Args[0],
		"-test.run=TestE2ECLIHelper",
		"--",
		"daemon",
		"--port", strconv.Itoa(port),
		"--cdp-host", ep.Host,
		"--cdp-port", strconv.Itoa(ep.Port),
		"--idle-tab-timeout", "0",
	)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+home,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start borz daemon helper: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		if t.Failed() {
			t.Logf("daemon stdout:\n%s", stdout.String())
			t.Logf("daemon stderr:\n%s", stderr.String())
		}
	})

	deadline := time.Now().Add(15 * time.Second)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			var health struct {
				OK           bool `json:"ok"`
				CDPConnected bool `json:"cdpConnected"`
			}
			if json.NewDecoder(resp.Body).Decode(&health) == nil && health.OK && health.CDPConnected {
				_ = resp.Body.Close()
				return e2eDaemonEnv{home: home}
			}
			_ = resp.Body.Close()
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("daemon did not become ready; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	return e2eDaemonEnv{home: home}
}

func startE2EServer(t *testing.T, home, token string) (e2eDaemonEnv, string) {
	t.Helper()

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	port := freeTCPPort(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(os.Args[0],
		"-test.run=TestE2ECLIHelper",
		"--",
		"server",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--token", token,
		"--cdp-host", ep.Host,
		"--cdp-port", strconv.Itoa(ep.Port),
		"--idle-tab-timeout", "0",
	)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+home,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start borz server helper: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		if t.Failed() {
			t.Logf("server stdout:\n%s", stdout.String())
			t.Logf("server stderr:\n%s", stderr.String())
		}
	})

	deadline := time.Now().Add(15 * time.Second)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			var health struct {
				OK           bool `json:"ok"`
				CDPConnected bool `json:"cdpConnected"`
			}
			if json.NewDecoder(resp.Body).Decode(&health) == nil && health.OK && health.CDPConnected {
				_ = resp.Body.Close()
				return e2eDaemonEnv{home: home}, fmt.Sprintf("http://127.0.0.1:%d", port)
			}
			_ = resp.Body.Close()
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("server did not become ready; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	return e2eDaemonEnv{home: home}, ""
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate local TCP port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func runE2ECLI(t *testing.T, env e2eDaemonEnv, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestE2ECLIHelper", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+env.home,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("borz %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func runE2ECLIError(t *testing.T, env e2eDaemonEnv, args ...string) (error, string) {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestE2ECLIHelper", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+env.home,
	)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("borz %s unexpectedly succeeded:\n%s", strings.Join(args, " "), string(out))
	}
	return err, string(out)
}

func runE2EJSON(t *testing.T, env e2eDaemonEnv, args ...string) protocol.Response {
	t.Helper()
	out := runE2ECLI(t, env, args...)
	var resp protocol.Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
	}
	if !resp.Success {
		t.Fatalf("borz %s returned unsuccessful response: %s\n%s", strings.Join(args, " "), resp.Error, out)
	}
	if resp.Data == nil {
		t.Fatalf("borz %s returned empty data: %s", strings.Join(args, " "), out)
	}
	return resp
}

func runE2EJSONResponse(t *testing.T, env e2eDaemonEnv, args ...string) protocol.Response {
	t.Helper()
	out := runE2ECLI(t, env, args...)
	var resp protocol.Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
	}
	return resp
}

func runE2ERESTJSON(t *testing.T, serverURL, token, path string, body interface{}) protocol.Response {
	t.Helper()
	status, resp := runE2ERESTJSONResponse(t, serverURL, token, path, body)
	if status != http.StatusOK || !resp.Success || resp.ID == "" || resp.Data == nil || resp.Error != "" {
		t.Fatalf("POST %s envelope = status %d, response %+v", path, status, resp)
	}
	return resp
}

func runE2ERESTJSONResponse(t *testing.T, serverURL, token, path string, body interface{}) (int, protocol.Response) {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode REST body for %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build REST request for %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer httpResp.Body.Close()

	var resp protocol.Response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		t.Fatalf("decode POST %s response: %v", path, err)
	}
	return httpResp.StatusCode, resp
}

func refByName(t *testing.T, snapshot *protocol.SnapshotData, name string) string {
	t.Helper()
	for _, el := range snapshot.Elements {
		if el.Name == name {
			return el.Ref
		}
	}
	var got []string
	for _, el := range snapshot.Elements {
		got = append(got, fmt.Sprintf("%s:%s:%s", el.Ref, el.Role, el.Name))
	}
	t.Fatalf("ref %q not found in snapshot elements: %s", name, strings.Join(got, ", "))
	return ""
}

func requireEvalString(t *testing.T, env e2eDaemonEnv, script, want string) {
	t.Helper()
	requireEvalStringWithPrefix(t, env, nil, script, want)
}

func requireEvalStringWithPrefix(t *testing.T, env e2eDaemonEnv, prefix []string, script, want string) {
	t.Helper()
	args := append(append([]string{}, prefix...), "eval", script, "--json")
	resp := runE2EJSON(t, env, args...)
	got, ok := resp.Data.Result.(string)
	if !ok || got != want {
		t.Fatalf("eval %q = %#v, want %q", script, resp.Data.Result, want)
	}
}

func requireEvalBool(t *testing.T, env e2eDaemonEnv, script string, want bool) {
	t.Helper()
	resp := runE2EJSON(t, env, "eval", script, "--json")
	got, ok := resp.Data.Result.(bool)
	if !ok || got != want {
		t.Fatalf("eval %q = %#v, want %v", script, resp.Data.Result, want)
	}
}

func requireContains(t *testing.T, got, want, label string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s missing %q in:\n%s", label, want, got)
	}
}

func requireNotContains(t *testing.T, got, unwanted, label string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("%s unexpectedly contains %q in:\n%s", label, unwanted, got)
	}
}
