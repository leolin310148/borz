package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/recorder"
)

func TestE2ECLILocalRecording(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	baseURL := site.URL()
	openResp := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "eval", `document.querySelector("#click-button").style.cursor = "pointer"`, "--json")
	bundlePath := filepath.Join(t.TempDir(), "local-recording.borzrec")
	const recordingID = "e2e-local-recording"
	startOut := runE2ECLI(t, env, "record", "start", "--id", recordingID, "--tab", tab,
		"--out", bundlePath, "--fps", "10", "--lossless", "--mask-selectors", "#text-input", "--json")
	var started recordInfo
	if err := json.Unmarshal([]byte(startOut), &started); err != nil {
		t.Fatalf("decode record start response: %v\n%s", err, startOut)
	}
	if started.ID != recordingID || started.Status != "recording" || started.Path != bundlePath {
		t.Fatalf("record start response = %+v", started)
	}

	runE2EJSON(t, env, "wait", "300", "--json")
	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("recording snapshot returned no snapshot data: %+v", snapshot.Data)
	}
	runE2EJSON(t, env, "click", refByName(t, snapshot.Data.SnapshotData, "Click counter"), "--json")
	runE2EJSON(t, env, "click", refByName(t, snapshot.Data.SnapshotData, "E2E text input"), "--json")
	runE2EJSON(t, env, "press", "z", "--json")
	runE2EJSON(t, env, "wait", "400", "--json")

	stopOut := runE2ECLI(t, env, "record", "stop", recordingID, "--json")
	var stopped recordInfo
	if err := json.Unmarshal([]byte(stopOut), &stopped); err != nil {
		t.Fatalf("decode record stop response: %v\n%s", err, stopOut)
	}
	if stopped.Status != "stopped" || stopped.FrameCount == 0 || stopped.EventCount == 0 {
		t.Fatalf("record stop response = %+v", stopped)
	}

	bundle, err := recorder.Verify(bundlePath)
	if err != nil {
		t.Fatalf("verify recording bundle: %v", err)
	}
	if bundle.Manifest.SchemaVersion != recorder.SchemaVersion || bundle.Manifest.Partial || bundle.Manifest.FinalizedAt == nil {
		t.Fatalf("recording manifest finalization/schema = %+v", bundle.Manifest)
	}
	if bundle.Manifest.CaptureMode != "cdp" || bundle.Manifest.Options.Tab != tab ||
		!slices.Contains(bundle.Manifest.Options.MaskSelectors, "#text-input") {
		t.Fatalf("recording manifest options = %+v", bundle.Manifest)
	}
	if len(bundle.Frames) == 0 || bundle.Frames[0].Width <= 0 || bundle.Frames[0].Height <= 0 {
		t.Fatalf("recording frames = %+v", bundle.Frames)
	}

	var foundClick, foundRedactedKey bool
	for _, event := range bundle.Events {
		if event.Type == "click" && event.Selector == "button#click-button" &&
			event.X != nil && event.Y != nil && event.Cursor == "pointer" && !event.Redacted {
			foundClick = true
		}
		if (event.Type == "keydown" || event.Type == "keyup") && event.Selector == "input#text-input" &&
			event.Redacted && event.Key == "<redacted>" && event.Text == "<redacted>" {
			foundRedactedKey = true
		}
	}
	if !foundClick {
		t.Fatalf("recording events missing click coordinates/cursor metadata: %+v", bundle.Events)
	}
	if !foundRedactedKey {
		t.Fatalf("recording events missing redacted input metadata: %+v", bundle.Events)
	}
}

func TestE2ECLITracingArtifact(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		status := runE2EJSONResponse(t, env, "trace", "status", "--tab", tab, "--json")
		if status.Success && status.Data != nil && status.Data.TraceStatus != nil && status.Data.TraceStatus.Recording {
			runE2EJSON(t, env, "trace", "stop", "--tab", tab, "--json")
		}
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "trace", "start", "--tab", tab, "--json")
	repeatedStart := runE2EJSONResponse(t, env, "trace", "start", "--tab", tab, "--json")
	if repeatedStart.Success || !strings.Contains(repeatedStart.Error, "already recording") {
		t.Fatalf("repeated trace start response = %+v", repeatedStart)
	}

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("trace snapshot returned no snapshot data: %+v", snapshot.Data)
	}
	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	inputRef := refByName(t, snapshot.Data.SnapshotData, "E2E text input")
	runE2EJSON(t, env, "click", clickRef, "--tab", tab, "--json")
	runE2EJSON(t, env, "fill", inputRef, "trace artifact text", "--tab", tab, "--json")

	artifactPath := filepath.Join(t.TempDir(), "trace.json")
	artifact, err := os.Create(artifactPath)
	if err != nil {
		t.Fatalf("create trace artifact: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestE2ECLIHelper", "--", "trace", "stop", "--tab", tab, "--json")
	cmd.Env = append(os.Environ(), "BORZ_E2E_HELPER=1", "BORZ_HOME="+env.home)
	var stderr bytes.Buffer
	cmd.Stdout = artifact
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := artifact.Close()
	if runErr != nil {
		t.Fatalf("stop trace to artifact: %v\n%s", runErr, stderr.String())
	}
	if closeErr != nil {
		t.Fatalf("close trace artifact: %v", closeErr)
	}

	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		t.Fatalf("stat trace artifact: %v", err)
	}
	if artifactInfo.Size() == 0 {
		t.Fatal("trace artifact is empty")
	}
	artifactData, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read trace artifact: %v", err)
	}
	var stopped protocol.Response
	if err := json.Unmarshal(artifactData, &stopped); err != nil {
		t.Fatalf("decode trace artifact: %v\n%s", err, artifactData)
	}
	if !stopped.Success || stopped.Data == nil || stopped.Data.TraceStatus == nil ||
		stopped.Data.TraceStatus.Recording || stopped.Data.TraceStatus.EventCount != 2 ||
		len(stopped.Data.TraceEvents) != 2 {
		t.Fatalf("trace artifact response = %+v", stopped)
	}
	clickEvent, fillEvent := stopped.Data.TraceEvents[0], stopped.Data.TraceEvents[1]
	if clickEvent.Type != "click" || clickEvent.Timestamp <= 0 || clickEvent.Ref == nil {
		t.Fatalf("trace click event = %+v", clickEvent)
	}
	if fillEvent.Type != "fill" || fillEvent.Timestamp <= 0 || fillEvent.Ref == nil ||
		fillEvent.Value != "trace artifact text" {
		t.Fatalf("trace fill event = %+v", fillEvent)
	}

	repeatedStop := runE2EJSONResponse(t, env, "trace", "stop", "--tab", tab, "--json")
	if repeatedStop.Success || !strings.Contains(repeatedStop.Error, "not recording") {
		t.Fatalf("repeated trace stop response = %+v", repeatedStop)
	}
}

func TestE2ECLIOperationalLogsPrivacy(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	t.Setenv("BORZ_SESSION_ID", "e2e-operational-logs")
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	const (
		sessionID       = "e2e-operational-logs"
		querySecret     = "query-secret-e2e47"
		scriptSecret    = "script-secret-e2e47"
		formSecret      = "form-secret-e2e47"
		headerName      = "X-E2E47-Secret"
		headerSecret    = "header-secret-e2e47"
		clipboardSecret = "clipboard-secret-e2e47"
	)
	baseURL := site.URL()
	openResp := runE2EJSON(t, env, "open", baseURL+"/?private_query="+querySecret,
		"--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("operational-log snapshot returned no snapshot data: %+v", snapshot.Data)
	}
	inputRef := refByName(t, snapshot.Data.SnapshotData, "E2E text input")
	runE2EJSON(t, env, "fill", inputRef, formSecret, "--tab", tab, "--json")
	evalScript := `window.__e2e47Secret = "` + scriptSecret + `"; true`
	runE2EJSON(t, env, "eval", evalScript, "--tab", tab, "--json")
	runE2EJSON(t, env, "fetch", baseURL+"/api/ping?private_fetch="+querySecret,
		"--header", headerName+": "+headerSecret, "--tab", tab, "--json")
	runE2EJSON(t, env, "clipboard-write", clipboardSecret, "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "40", "--tab", tab, "--json")

	failed := runE2EJSONResponse(t, env, "click", "e999999", "--tab", tab, "--json")
	if failed.Success {
		t.Fatalf("missing-ref click unexpectedly succeeded: %+v", failed)
	}

	tail := runE2ECLI(t, env, "logs", "tail", "--lines", "500", "--json")
	statsOut := runE2ECLI(t, env, "logs", "stats", "--since", "1h", "--json")
	for label, secret := range map[string]string{
		"URL query name": "private_query",
		"URL query":      querySecret,
		"eval script":    scriptSecret,
		"form text":      formSecret,
		"header name":    headerName,
		"header value":   headerSecret,
		"clipboard text": clipboardSecret,
	} {
		requireNotContains(t, tail+statsOut, secret, label+" in operational logs")
	}

	type logEntry struct {
		Event       string `json:"event"`
		SessionID   string `json:"session_id"`
		RequestID   string `json:"request_id"`
		Surface     string `json:"surface"`
		Action      string `json:"action"`
		DurationMS  int64  `json:"duration_ms"`
		Success     *bool  `json:"success"`
		ErrorCode   string `json:"error_code"`
		TextBytes   int    `json:"text_bytes"`
		ScriptBytes int    `json:"script_bytes"`
	}
	var entries []logEntry
	if err := json.Unmarshal([]byte(tail), &entries); err != nil {
		t.Fatalf("decode operational log tail: %v\n%s", err, tail)
	}
	actionCounts := map[string]int{}
	actionFailures := map[string]int{}
	requestIDs := map[string]bool{}
	var foundFill, foundFailedClick, foundWait, foundEval, foundClipboard bool
	var waitMaxMS int64
	for _, entry := range entries {
		if entry.Event != "command_completed" {
			continue
		}
		if entry.SessionID != sessionID || entry.Surface != "cli" || entry.RequestID == "" {
			t.Fatalf("command correlation metadata = %+v", entry)
		}
		if requestIDs[entry.RequestID] {
			t.Fatalf("duplicate operational-log request id %q", entry.RequestID)
		}
		requestIDs[entry.RequestID] = true
		actionCounts[entry.Action]++
		if entry.Success != nil && !*entry.Success {
			actionFailures[entry.Action]++
		}
		switch entry.Action {
		case string(protocol.ActionFill):
			foundFill = entry.Success != nil && *entry.Success && entry.TextBytes == len(formSecret)
		case string(protocol.ActionClick):
			foundFailedClick = entry.Success != nil && !*entry.Success && entry.ErrorCode == "stale_ref"
		case string(protocol.ActionWait):
			if entry.DurationMS > waitMaxMS {
				waitMaxMS = entry.DurationMS
			}
			foundWait = entry.Success != nil && *entry.Success && entry.DurationMS >= 30
		case string(protocol.ActionEval):
			foundEval = foundEval || entry.ScriptBytes == len(evalScript)
		case string(protocol.ActionClipboardWrite):
			foundClipboard = entry.Success != nil && *entry.Success && entry.TextBytes == len(clipboardSecret)
		}
	}
	if !foundFill || !foundFailedClick || !foundWait || !foundEval || !foundClipboard {
		t.Fatalf("missing expected operational metadata: fill=%t failedClick=%t wait=%t eval=%t clipboard=%t\n%s",
			foundFill, foundFailedClick, foundWait, foundEval, foundClipboard, tail)
	}

	var stats logStats
	if err := json.Unmarshal([]byte(statsOut), &stats); err != nil {
		t.Fatalf("decode operational log stats: %v\n%s", err, statsOut)
	}
	if stats.Commands != len(requestIDs) || stats.CommandFailures != actionFailures[string(protocol.ActionClick)] {
		t.Fatalf("tail/stats totals differ: tail commands=%d failures=%d stats=%+v",
			len(requestIDs), actionFailures[string(protocol.ActionClick)], stats)
	}
	for action, count := range actionCounts {
		if stats.ByAction[action] != count {
			t.Fatalf("tail/stats count for %s: tail=%d stats=%d", action, count, stats.ByAction[action])
		}
	}
	clickStats := stats.ActionStats[string(protocol.ActionClick)]
	waitStats := stats.ActionStats[string(protocol.ActionWait)]
	if clickStats.Failures != 1 || waitStats.Count != actionCounts[string(protocol.ActionWait)] ||
		waitStats.MaxMS != waitMaxMS || waitStats.P95MS <= 0 || stats.CommandP95MS <= 0 {
		t.Fatalf("latency/failure stats do not match tail: click=%+v wait=%+v commandP95=%d tailWaitMax=%d",
			clickStats, waitStats, stats.CommandP95MS, waitMaxMS)
	}
}

func TestE2ELegacyCompatibility(t *testing.T) {
	skipUnlessE2E(t)

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	binDir := t.TempDir()
	borzBin := filepath.Join(binDir, "borz")
	shimBin := filepath.Join(binDir, "bb-browser")
	for _, build := range []struct {
		output string
		pkg    string
	}{
		{output: borzBin, pkg: "."},
		{output: shimBin, pkg: "./cmd/bb-browser-shim"},
	} {
		cmd := exec.Command("go", "build", "-o", build.output, build.pkg)
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			t.Fatalf("build %s: %v\n%s", build.pkg, buildErr, out)
		}
	}

	userHome := t.TempDir()
	legacyHome := filepath.Join(userHome, ".bb-browser")
	currentHome := filepath.Join(userHome, ".borz")
	if err := os.Mkdir(legacyHome, 0o755); err != nil {
		t.Fatalf("create legacy config directory: %v", err)
	}
	const markerContents = "legacy config survived migration\n"
	if err := os.WriteFile(filepath.Join(legacyHome, "migration-marker"), []byte(markerContents), 0o600); err != nil {
		t.Fatalf("write legacy migration marker: %v", err)
	}

	profile := "e2e-legacy-compat"
	legacyEnv := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "PATH=") ||
			strings.HasPrefix(entry, "BORZ_HOME=") || strings.HasPrefix(entry, "BB_BROWSER_HOME=") ||
			strings.HasPrefix(entry, "BORZ_CDP_URL=") || strings.HasPrefix(entry, "BB_BROWSER_CDP_URL=") ||
			strings.HasPrefix(entry, "BORZ_PROFILE=") || strings.HasPrefix(entry, "BB_BROWSER_PROFILE=") ||
			strings.HasPrefix(entry, "BORZ_TAB_IDLE_TIMEOUT=") || strings.HasPrefix(entry, "BB_BROWSER_TAB_IDLE_TIMEOUT=") {
			continue
		}
		legacyEnv = append(legacyEnv, entry)
	}
	legacyEnv = append(legacyEnv,
		"HOME="+userHome,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		fmt.Sprintf("BB_BROWSER_CDP_URL=http://%s:%d", ep.Host, ep.Port),
		"BB_BROWSER_PROFILE="+profile,
		"BB_BROWSER_TAB_IDLE_TIMEOUT=0",
		"BB_BROWSER_E2E=1",
	)

	run := func(binary string, args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = legacyEnv
		cmd.Stdin = strings.NewReader("")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s %s failed: %v\nstdout:\n%s\nstderr:\n%s", binary, strings.Join(args, " "), err, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}

	daemonPath := filepath.Join(currentHome, "profiles", profile, "daemon.json")
	var tab string
	t.Cleanup(func() {
		if tab != "" {
			cmd := exec.Command(borzBin, "close", "--tab", tab, "--json")
			cmd.Env = legacyEnv
			_ = cmd.Run()
		}
		if raw, readErr := os.ReadFile(daemonPath); readErr == nil {
			var info protocol.DaemonInfo
			_ = json.Unmarshal(raw, &info)
			cmd := exec.Command(borzBin, "daemon", "shutdown")
			cmd.Env = legacyEnv
			_ = cmd.Run()
			if info.PID > 0 && !client.WaitForProcessExit(info.PID, 3*time.Second) {
				if process, findErr := os.FindProcess(info.PID); findErr == nil {
					_ = process.Kill()
				}
			}
		}
	})

	stdout, stderr := run(shimBin, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	requireContains(t, stderr, "bb-browser is deprecated", "shim stderr")
	requireNotContains(t, stdout, "deprecated", "shim JSON stdout")
	var openResp protocol.Response
	if err := json.Unmarshal([]byte(stdout), &openResp); err != nil {
		t.Fatalf("shim stdout is not a JSON response: %v\n%s", err, stdout)
	}
	if !openResp.Success || openResp.Data == nil || openResp.Data.Tab == "" {
		t.Fatalf("shim open response = %+v", openResp)
	}
	tab = openResp.Data.Tab

	migratedMarker, err := os.ReadFile(filepath.Join(currentHome, "migration-marker"))
	if err != nil || string(migratedMarker) != markerContents {
		t.Fatalf("migrated config marker = %q, %v", migratedMarker, err)
	}
	if _, err := os.Stat(legacyHome); !os.IsNotExist(err) {
		t.Fatalf("legacy config directory still exists after migration: %v", err)
	}
	rawDaemon, err := os.ReadFile(daemonPath)
	if err != nil {
		t.Fatalf("legacy profile did not select isolated daemon path: %v", err)
	}
	var info protocol.DaemonInfo
	if err := json.Unmarshal(rawDaemon, &info); err != nil || info.PID <= 0 {
		t.Fatalf("decode legacy profile daemon state: info=%+v err=%v", info, err)
	}

	run(borzBin, "close", "--tab", tab, "--json")
	tab = ""
	run(borzBin, "daemon", "shutdown")
	if !client.WaitForProcessExit(info.PID, 5*time.Second) {
		t.Fatalf("legacy profile daemon pid %d leaked after shutdown", info.PID)
	}
}
