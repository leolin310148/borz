package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
)

// TestE2EManagedProfileTitleLabel checks that a borz-launched Chrome for a
// named profile shows "[profile] " in front of page titles (what the tab strip
// displays), keeps it through SPA title changes, navigations, tabs opened
// outside borz, and a daemon restart, while every title borz reports stays
// the page's own.
func TestE2EManagedProfileTitleLabel(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	profile := "e2e-title-label"
	prefix := "[" + profile + "] "
	bin := filepath.Join(t.TempDir(), "borz")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build borz e2e binary: %v\n%s", err, out)
	}
	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() { _ = site.Close(t.Context()) })

	env := e2eEnvWithoutCDPOverride("BORZ_HOME="+home, "BORZ_E2E=1", "BORZ_TAB_IDLE_TIMEOUT=0")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append(args, "--profile", profile)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("borz %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	runJSON := func(args ...string) protocol.Response {
		t.Helper()
		out := run(args...)
		var resp protocol.Response
		if err := json.Unmarshal([]byte(out), &resp); err != nil || !resp.Success || resp.Data == nil {
			t.Fatalf("borz %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return resp
	}

	userData := filepath.Join(home, "profiles", profile, "browser", "user-data")
	portPath := filepath.Join(home, "profiles", profile, "browser", "cdp-port")
	t.Cleanup(func() {
		browserPID := e2eBrowserPIDFromSingletonLock(userData)
		cmd := exec.Command(bin, "daemon", "shutdown", "--profile", profile)
		cmd.Env = env
		_ = cmd.Run()
		if raw, err := os.ReadFile(portPath); err == nil {
			if port, _ := strconv.Atoi(strings.TrimSpace(string(raw))); port > 0 {
				closeE2EBrowser(t, port)
			}
		}
		if browserPID > 0 && !client.WaitForProcessExit(browserPID, 5*time.Second) {
			t.Errorf("isolated browser pid %d still running after cleanup", browserPID)
		}
	})

	opened := runJSON("open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data
	tab := opened.Tab
	if opened.Title != "" && strings.HasPrefix(opened.Title, prefix) {
		t.Fatalf("open reported the labeled title %q", opened.Title)
	}
	portRaw, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("read isolated browser port: %v", err)
	}
	browserPort, _ := strconv.Atoi(strings.TrimSpace(string(portRaw)))

	// chromeTitle is the title Chrome itself shows for a tab (tab strip).
	waitChromeTitle := func(targetID, want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		last := ""
		for time.Now().Before(deadline) {
			last = e2eChromeTargetTitle(t, browserPort, targetID)
			if last == want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("Chrome title for %s = %q, want %q", targetID, last, want)
	}
	tabID := func(short string) string {
		t.Helper()
		for _, info := range runJSON("tab", "list", "--json").Data.Tabs {
			if info.Tab == short {
				return fmt.Sprint(info.TabID)
			}
		}
		t.Fatalf("tab %s not listed", short)
		return ""
	}
	requireBorzTitle := func(short, want string) {
		t.Helper()
		for _, info := range runJSON("tab", "list", "--json").Data.Tabs {
			if info.Tab == short && info.Title != want {
				t.Fatalf("tab list title = %q, want %q", info.Title, want)
			}
		}
		if got := runJSON("get", "title", "--tab", short, "--json").Data.Value; got != want {
			t.Fatalf("get title = %q, want %q", got, want)
		}
	}

	target := tabID(tab)
	waitChromeTitle(target, prefix+"E2E Verify Home")
	requireBorzTitle(tab, "E2E Verify Home")

	// A page changing its own title (SPA unread counters) keeps the label.
	runJSON("eval", `document.title = "(3) Inbox"; true`, "--tab", tab, "--json")
	waitChromeTitle(target, prefix+"(3) Inbox")
	requireBorzTitle(tab, "(3) Inbox")

	// Navigation installs it on the new document.
	runJSON("navigate", site.URL()+"/page2", "--tab", tab, "--wait-for", "#page-two-ready", "--timeout", "10000", "--json")
	waitChromeTitle(target, prefix+"E2E Verify Page Two")
	requireBorzTitle(tab, "E2E Verify Page Two")

	// A tab opened outside borz (user or page) is labeled too.
	outsideID := e2eChromeNewTab(t, browserPort, site.URL()+"/")
	waitChromeTitle(outsideID, prefix+"E2E Verify Home")

	// A restarted daemon re-installs the label without stacking it.
	run("daemon", "restart")
	runJSON("eval", `document.title = "after restart"; true`, "--tab", tab, "--json")
	waitChromeTitle(target, prefix+"after restart")
	requireBorzTitle(tab, "after restart")
}

func e2eChromeTargetTitle(t *testing.T, port int, targetID string) string {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		t.Fatalf("list Chrome targets: %v", err)
	}
	defer resp.Body.Close()
	var targets []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		t.Fatalf("decode Chrome targets: %v", err)
	}
	for _, target := range targets {
		if target.ID == targetID {
			return target.Title
		}
	}
	return ""
}

// e2eChromeNewTab opens a tab through Chrome's own endpoint, bypassing borz.
func e2eChromeNewTab(t *testing.T, port int, rawURL string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/json/new?%s", port, url.QueryEscape(rawURL)), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open tab via Chrome: %v", err)
	}
	defer resp.Body.Close()
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("decode new Chrome tab: %v", err)
	}
	return created.ID
}
