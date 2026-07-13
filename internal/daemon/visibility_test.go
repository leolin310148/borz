package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leolin310148/borz/internal/protocol"
)

// errNotSupported mimics the CDP error a Chrome build returns for an
// experimental command it does not implement.
var errNotSupported = errors.New("'Browser.getWindowForTarget' wasn't found")

// callsTo returns every recorded call to a CDP method.
func callsTo(f *fakeCDP, method string) []fakeCall {
	var out []fakeCall
	for _, c := range f.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// evaluatedExpressions returns the `expression` of every Runtime.evaluate call.
func evaluatedExpressions(f *fakeCDP) []string {
	var out []string
	for _, c := range callsTo(f, "Runtime.evaluate") {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(c.Params, &p)
		out = append(out, p.Expression)
	}
	return out
}

func anyContains(xs []string, sub string) bool {
	for _, x := range xs {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

// --- tab front ---

func TestDispatch_TabFront_RestoresMinimizedWindow(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Browser.getWindowForTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"windowId": 42,
			"bounds":   map[string]interface{}{"windowState": "minimized"},
		}, nil
	})
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "string", "value": "visible"},
		}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabFront})
	if !resp.Success {
		t.Fatalf("tab front: %+v", resp)
	}

	bounds := callsTo(f, "Browser.setWindowBounds")
	if len(bounds) != 1 {
		t.Fatalf("expected the minimized window to be restored, setWindowBounds calls: %d", len(bounds))
	}
	var p struct {
		WindowID int `json:"windowId"`
		Bounds   struct {
			WindowState string `json:"windowState"`
		} `json:"bounds"`
	}
	json.Unmarshal(bounds[0].Params, &p)
	if p.WindowID != 42 || p.Bounds.WindowState != "normal" {
		t.Fatalf("setWindowBounds params: %+v", p)
	}

	if !contains(activatedTargetIDs(t, f), "T1") {
		t.Fatal("tab front must activate the target")
	}
	if len(callsTo(f, "Page.bringToFront")) == 0 {
		t.Fatal("tab front must call Page.bringToFront")
	}
	if c.GetCurrentTargetID() != "T1" {
		t.Fatalf("tab front should pin the current target, got %q", c.GetCurrentTargetID())
	}

	result, _ := resp.Data.Result.(map[string]interface{})
	if result["restoredFromMinimized"] != true {
		t.Fatalf("result should report the restore: %+v", result)
	}
	if result["visibilityState"] != "visible" {
		t.Fatalf("result should report the achieved visibilityState: %+v", result)
	}
}

func TestDispatch_TabFront_NormalWindowIsNotResized(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Browser.getWindowForTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"windowId": 7,
			"bounds":   map[string]interface{}{"windowState": "normal"},
		}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabFront})
	if !resp.Success {
		t.Fatalf("tab front: %+v", resp)
	}
	if n := len(callsTo(f, "Browser.setWindowBounds")); n != 0 {
		t.Fatalf("a non-minimized window must not be resized, got %d setWindowBounds calls", n)
	}
	result, _ := resp.Data.Result.(map[string]interface{})
	if result["windowState"] != "normal" {
		t.Fatalf("result windowState: %+v", result)
	}
	if _, ok := result["restoredFromMinimized"]; ok {
		t.Fatalf("restoredFromMinimized must be absent: %+v", result)
	}
}

// Browser.getWindowForTarget is experimental and absent on some endpoints
// (e.g. a bare CDP relay). tab front must still activate + bring to front.
func TestDispatch_TabFront_SurvivesMissingWindowAPI(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Browser.getWindowForTarget", func(json.RawMessage) (interface{}, error) {
		return nil, errNotSupported
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabFront})
	if !resp.Success {
		t.Fatalf("tab front should not fail when the window API is unavailable: %+v", resp)
	}
	if !contains(activatedTargetIDs(t, f), "T1") {
		t.Fatal("tab front must still activate the target")
	}
}

// --- page visibility ---

func TestDispatch_PageVisibility_Visible(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "string", "value": "visible"},
		}, nil
	})
	f.On("Page.addScriptToEvaluateOnNewDocument", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"identifier": "S-1"}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionPageVisibility, Visibility: "visible"})
	if !resp.Success {
		t.Fatalf("page visibility visible: %+v", resp)
	}

	exprs := evaluatedExpressions(f)
	if !anyContains(exprs, "visibilityState") || !anyContains(exprs, "visibilitychange") {
		t.Fatalf("override must redefine visibilityState and fire visibilitychange, got %v", exprs)
	}

	// The override must be reinstalled after navigation, else the page reverts
	// to reporting "hidden" the moment it navigates.
	added := callsTo(f, "Page.addScriptToEvaluateOnNewDocument")
	if len(added) != 1 {
		t.Fatalf("expected a new-document script to persist the override, got %d", len(added))
	}

	tab := c.TabManager.GetTab("T1")
	state, scriptID := tab.GetVisibilityOverride()
	if state != "visible" || scriptID != "S-1" {
		t.Fatalf("override state not tracked: %q %q", state, scriptID)
	}

	result, _ := resp.Data.Result.(map[string]interface{})
	if result["override"] != "visible" || result["persisted"] != true {
		t.Fatalf("result: %+v", result)
	}
}

func TestDispatch_PageVisibility_ReplacingOverrideRemovesPrevScript(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "string", "value": "visible"},
		}, nil
	})
	ids := []string{"S-1", "S-2"}
	var n int
	f.On("Page.addScriptToEvaluateOnNewDocument", func(json.RawMessage) (interface{}, error) {
		id := ids[min(n, len(ids)-1)]
		n++
		return map[string]interface{}{"identifier": id}, nil
	})
	c := connectCdp(t, f)

	for i := 0; i < 2; i++ {
		resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionPageVisibility, Visibility: "visible"})
		if !resp.Success {
			t.Fatalf("page visibility (call %d): %+v", i, resp)
		}
	}

	removed := callsTo(f, "Page.removeScriptToEvaluateOnNewDocument")
	if len(removed) != 1 {
		t.Fatalf("re-arming must drop the previous new-document script, removals: %d", len(removed))
	}
	var p struct {
		Identifier string `json:"identifier"`
	}
	json.Unmarshal(removed[0].Params, &p)
	if p.Identifier != "S-1" {
		t.Fatalf("removed identifier: %q", p.Identifier)
	}
}

func TestDispatch_PageVisibility_Reset(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "string", "value": "hidden"},
		}, nil
	})
	f.On("Page.addScriptToEvaluateOnNewDocument", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"identifier": "S-9"}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "a", Action: protocol.ActionPageVisibility, Visibility: "visible"})
	resp := DispatchRequest(c, &protocol.Request{ID: "b", Action: protocol.ActionPageVisibility, Visibility: "reset"})
	if !resp.Success {
		t.Fatalf("page visibility reset: %+v", resp)
	}

	if len(callsTo(f, "Page.removeScriptToEvaluateOnNewDocument")) != 1 {
		t.Fatal("reset must remove the persisted override script")
	}
	tab := c.TabManager.GetTab("T1")
	if state, scriptID := tab.GetVisibilityOverride(); state != "" || scriptID != "" {
		t.Fatalf("reset must clear the tracked override, got %q %q", state, scriptID)
	}
	result, _ := resp.Data.Result.(map[string]interface{})
	if result["visibilityState"] != "hidden" {
		t.Fatalf("reset should report the native visibilityState: %+v", result)
	}
}

func TestDispatch_PageVisibility_Status(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "string", "value": "hidden"},
		}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionPageVisibility})
	if !resp.Success {
		t.Fatalf("page visibility status: %+v", resp)
	}
	result, _ := resp.Data.Result.(map[string]interface{})
	if result["visibilityState"] != "hidden" || result["overridden"] != false {
		t.Fatalf("status result: %+v", result)
	}
	// Status is read-only: it must not install anything.
	if len(callsTo(f, "Page.addScriptToEvaluateOnNewDocument")) != 0 {
		t.Fatal("status must not install an override")
	}
}

func TestDispatch_PageVisibility_UnknownState(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionPageVisibility, Visibility: "sideways"})
	if resp.Success || !strings.Contains(resp.Error, "unknown visibility state") {
		t.Fatalf("expected an unknown-state error, got %+v", resp)
	}
}

// --- filechooser ---

func TestDispatch_FileChooser_AcceptArmsInterception(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	file := filepath.Join(t.TempDir(), "report.pdf")
	os.WriteFile(file, []byte("pdf"), 0o644)

	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionFileChooser, FileChooserCommand: "accept", Files: []string{file},
	})
	if !resp.Success {
		t.Fatalf("filechooser accept: %+v", resp)
	}

	intercepts := callsTo(f, "Page.setInterceptFileChooserDialog")
	if len(intercepts) != 1 {
		t.Fatalf("arming must enable interception, calls: %d", len(intercepts))
	}
	var p struct {
		Enabled bool `json:"enabled"`
	}
	json.Unmarshal(intercepts[0].Params, &p)
	if !p.Enabled {
		t.Fatal("interception should be enabled")
	}

	handler := c.TabManager.GetTab("T1").PeekFileChooserHandler()
	if handler == nil || !handler.Accept || len(handler.Files) != 1 || handler.Files[0] != file {
		t.Fatalf("armed handler: %+v", handler)
	}
}

func TestDispatch_FileChooser_AcceptRejectsMissingFile(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionFileChooser, FileChooserCommand: "accept",
		Files: []string{filepath.Join(t.TempDir(), "nope.pdf")},
	})
	if resp.Success || !strings.Contains(resp.Error, "stat") {
		t.Fatalf("a nonexistent file must fail before arming, got %+v", resp)
	}
	if c.TabManager.GetTab("T1").PeekFileChooserHandler() != nil {
		t.Fatal("a failed accept must not leave a handler armed")
	}
}

func TestDispatch_FileChooser_AcceptRequiresFiles(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionFileChooser, FileChooserCommand: "accept"})
	if resp.Success || !strings.Contains(resp.Error, "missing files") {
		t.Fatalf("expected a missing-files error, got %+v", resp)
	}
}

func TestDispatch_FileChooser_StatusAndDisarm(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "s0", Action: protocol.ActionFileChooser})
	if result, _ := resp.Data.Result.(map[string]interface{}); result["armed"] != false {
		t.Fatalf("nothing armed yet: %+v", resp.Data.Result)
	}

	DispatchRequest(c, &protocol.Request{ID: "c", Action: protocol.ActionFileChooser, FileChooserCommand: "cancel"})
	resp = DispatchRequest(c, &protocol.Request{ID: "s1", Action: protocol.ActionFileChooser, FileChooserCommand: "status"})
	result, _ := resp.Data.Result.(map[string]interface{})
	if result["armed"] != true || result["action"] != "cancel" {
		t.Fatalf("status after arming cancel: %+v", result)
	}
	// Status must not consume the arming.
	if c.TabManager.GetTab("T1").PeekFileChooserHandler() == nil {
		t.Fatal("status must not consume the armed handler")
	}

	resp = DispatchRequest(c, &protocol.Request{ID: "d", Action: protocol.ActionFileChooser, FileChooserCommand: "disarm"})
	if !resp.Success {
		t.Fatalf("disarm: %+v", resp)
	}
	if c.TabManager.GetTab("T1").PeekFileChooserHandler() != nil {
		t.Fatal("disarm must drop the handler")
	}
	// The last interception toggle should be a disable.
	intercepts := callsTo(f, "Page.setInterceptFileChooserDialog")
	var last struct {
		Enabled bool `json:"enabled"`
	}
	json.Unmarshal(intercepts[len(intercepts)-1].Params, &last)
	if last.Enabled {
		t.Fatal("disarm must disable file chooser interception")
	}
}

func TestDispatch_FileChooser_UnknownSubcommand(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionFileChooser, FileChooserCommand: "yolo"})
	if resp.Success || !strings.Contains(resp.Error, "unknown filechooser subcommand") {
		t.Fatalf("expected unknown-subcommand error, got %+v", resp)
	}
}

// The armed handler is consumed by Page.fileChooserOpened, which fulfills the
// dialog with DOM.setFileInputFiles and stops intercepting.
func TestFileChooserOpened_FulfillsArmedHandler(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	file := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(file, []byte("png"), 0o644)
	if resp := DispatchRequest(c, &protocol.Request{
		ID: "arm", Action: protocol.ActionFileChooser, FileChooserCommand: "accept", Files: []string{file},
	}); !resp.Success {
		t.Fatalf("arm: %+v", resp)
	}

	emitSessionEvent(t, f, c, "T1", "Page.fileChooserOpened", map[string]interface{}{
		"mode":          "selectSingle",
		"backendNodeId": 77,
	})

	waitFor(t, func() bool { return len(callsTo(f, "DOM.setFileInputFiles")) > 0 })

	var p struct {
		Files         []string `json:"files"`
		BackendNodeID int      `json:"backendNodeId"`
	}
	json.Unmarshal(callsTo(f, "DOM.setFileInputFiles")[0].Params, &p)
	if p.BackendNodeID != 77 || len(p.Files) != 1 || p.Files[0] != file {
		t.Fatalf("setFileInputFiles params: %+v", p)
	}

	// Arming is one-shot: the handler is consumed and interception disabled.
	if c.TabManager.GetTab("T1").PeekFileChooserHandler() != nil {
		t.Fatal("the handler must be consumed by the dialog")
	}
	waitFor(t, func() bool {
		intercepts := callsTo(f, "Page.setInterceptFileChooserDialog")
		var last struct {
			Enabled bool `json:"enabled"`
		}
		json.Unmarshal(intercepts[len(intercepts)-1].Params, &last)
		return !last.Enabled
	})
}

func TestFileChooserOpened_CancelDoesNotSetFiles(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "arm", Action: protocol.ActionFileChooser, FileChooserCommand: "cancel"})
	emitSessionEvent(t, f, c, "T1", "Page.fileChooserOpened", map[string]interface{}{
		"mode":          "selectSingle",
		"backendNodeId": 5,
	})

	waitFor(t, func() bool { return c.TabManager.GetTab("T1").PeekFileChooserHandler() == nil })
	if n := len(callsTo(f, "DOM.setFileInputFiles")); n != 0 {
		t.Fatalf("cancel must not attach files, got %d setFileInputFiles calls", n)
	}
}

// A dialog that opens with nothing armed is left alone (the page's own
// interception state decides) — no panic, no stray CDP calls.
func TestFileChooserOpened_NoHandlerArmed(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)
	// Attach so the tab exists in the manager.
	DispatchRequest(c, &protocol.Request{ID: "s", Action: protocol.ActionFileChooser, FileChooserCommand: "status"})

	emitSessionEvent(t, f, c, "T1", "Page.fileChooserOpened", map[string]interface{}{"mode": "selectSingle"})
	time.Sleep(50 * time.Millisecond)
	if n := len(callsTo(f, "DOM.setFileInputFiles")); n != 0 {
		t.Fatalf("no handler armed must mean no file attach, got %d", n)
	}
}

// --- press: editing commands ---

func TestDispatch_Press_AutoMapsMetaComboToEditingCommand(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionPress, Key: "a", Modifiers: []string{"meta"},
	})
	if !resp.Success {
		t.Fatalf("press: %+v", resp)
	}

	keyDown := keyEventParams(t, f, "keyDown")
	if len(keyDown) != 1 {
		t.Fatalf("expected one keyDown, got %d", len(keyDown))
	}
	if got := keyDown[0].Commands; len(got) != 1 || got[0] != "selectAll" {
		t.Fatalf("Cmd+A must carry the selectAll editing command, got %v", got)
	}
	// Only keyDown carries commands — keyUp must not repeat the command.
	for _, ev := range keyEventParams(t, f, "keyUp") {
		if len(ev.Commands) != 0 {
			t.Fatalf("keyUp must not carry commands, got %v", ev.Commands)
		}
	}
}

// ctrl combos already work through the renderer; auto-mapping them too would
// execute the command twice.
func TestDispatch_Press_CtrlComboIsNotAutoMapped(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionPress, Key: "a", Modifiers: []string{"ctrl"},
	})
	for _, ev := range keyEventParams(t, f, "keyDown") {
		if len(ev.Commands) != 0 {
			t.Fatalf("ctrl combos must not be auto-mapped, got %v", ev.Commands)
		}
	}
}

func TestDispatch_Press_ExplicitCommandsOverrideAutoMapping(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionPress, Key: "a", Modifiers: []string{"meta"},
		Commands: []string{"selectAll", "copy"},
	})
	keyDown := keyEventParams(t, f, "keyDown")
	if got := keyDown[0].Commands; len(got) != 2 || got[0] != "selectAll" || got[1] != "copy" {
		t.Fatalf("explicit commands should win, got %v", got)
	}
}

func TestDispatch_Key_AutoMapsMetaCombo(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionKey, KeyType: "press", Key: "v", Modifiers: []string{"meta"},
	})
	keyDown := keyEventParams(t, f, "keyDown")
	if len(keyDown) == 0 || len(keyDown[0].Commands) != 1 || keyDown[0].Commands[0] != "paste" {
		t.Fatalf("Cmd+V must carry the paste editing command, got %+v", keyDown)
	}
}

func TestEditingCommandsFor(t *testing.T) {
	for _, tc := range []struct {
		key  string
		mods []string
		want string
	}{
		{"a", []string{"meta"}, "selectAll"},
		{"c", []string{"meta"}, "copy"},
		{"x", []string{"meta"}, "cut"},
		{"v", []string{"meta"}, "paste"},
		{"z", []string{"meta"}, "undo"},
		{"z", []string{"meta", "shift"}, "redo"},
		{"A", []string{"cmd"}, "selectAll"},
		{"a", []string{"ctrl"}, ""},
		{"a", nil, ""},
		{"a", []string{"meta", "alt"}, ""},
		{"c", []string{"meta", "shift"}, ""},
		{"Enter", []string{"meta"}, ""},
	} {
		got := editingCommandsFor(tc.key, tc.mods)
		if tc.want == "" {
			if len(got) != 0 {
				t.Errorf("editingCommandsFor(%q, %v) = %v, want none", tc.key, tc.mods, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("editingCommandsFor(%q, %v) = %v, want [%s]", tc.key, tc.mods, got, tc.want)
		}
	}
}

// --- test plumbing ---

type keyEvent struct {
	Type     string   `json:"type"`
	Key      string   `json:"key"`
	Commands []string `json:"commands"`
}

func keyEventParams(t *testing.T, f *fakeCDP, eventType string) []keyEvent {
	t.Helper()
	var out []keyEvent
	for _, c := range callsTo(f, "Input.dispatchKeyEvent") {
		var ev keyEvent
		if json.Unmarshal(c.Params, &ev) != nil || ev.Type != eventType {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// emitSessionEvent pushes an unsolicited flat-protocol session event from the
// fake browser to the daemon, the way Chrome delivers Page.* events.
func emitSessionEvent(t *testing.T, f *fakeCDP, c *CdpConnection, targetID, method string, params map[string]interface{}) {
	t.Helper()
	sessionIDVal, ok := c.sessions.Load(targetID)
	if !ok {
		t.Fatalf("no session attached for target %s", targetID)
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"method":    method,
		"sessionId": sessionIDVal.(string),
		"params":    params,
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("emit %s: %v", method, err)
	}
}

// waitFor polls cond until it holds or the deadline passes. The daemon handles
// CDP events asynchronously, so assertions on their side effects must wait.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
