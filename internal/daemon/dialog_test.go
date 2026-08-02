package daemon

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

func dialogOpeningMsg(t *testing.T, kind, message, defaultPrompt string) map[string]json.RawMessage {
	t.Helper()
	return rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"type":              kind,
			"message":           message,
			"url":               "https://dialogs.test/",
			"defaultPrompt":     defaultPrompt,
			"hasBrowserHandler": true,
		},
	})
}

// An unarmed dialog must still be recorded — that recording is the only thing
// distinguishing "the page is blocked" from "the command timed out".
func TestDialog_UnarmedDialogIsRecordedAsPending(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")

	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", dialogOpeningMsg(t, "confirm", "Delete this record?", ""))

	pending := tab.PeekPendingDialog()
	if pending == nil {
		t.Fatal("dialog opening did not record a pending dialog")
	}
	if pending.Type != "confirm" || pending.Message != "Delete this record?" {
		t.Fatalf("pending dialog = %+v", pending)
	}
	if pending.URL != "https://dialogs.test/" || !pending.HasBrowserHandler {
		t.Fatalf("pending dialog lost event metadata: %+v", pending)
	}
	if pending.AutoHandled {
		t.Fatal("no handler was armed, so AutoHandled must be false")
	}
	if pending.OpenedAt == 0 {
		t.Fatal("pending dialog missing OpenedAt")
	}
}

func TestDialog_ArmedHandlerMarksAutoHandled(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")
	tab.SetDialogHandler(&DialogHandler{Accept: true, PromptText: "Leo"})

	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", dialogOpeningMsg(t, "prompt", "Your name?", "anon"))

	if handler := tab.PeekDialogHandler(); handler != nil {
		t.Fatalf("armed handler was not consumed: %+v", handler)
	}
	pending := tab.PeekPendingDialog()
	if pending == nil {
		t.Fatal("auto-handled dialog should still be recorded while open")
	}
	if !pending.AutoHandled || pending.HandledAs != "accept" || pending.PromptText != "Leo" {
		t.Fatalf("pending dialog = %+v", pending)
	}
	if pending.DefaultPrompt != "anon" {
		t.Fatalf("defaultPrompt = %q", pending.DefaultPrompt)
	}
}

func TestDialog_ClosingMovesDialogIntoHistory(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")

	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", dialogOpeningMsg(t, "prompt", "Your name?", ""))
	c.handleSessionEvent("T1", "Page.javascriptDialogClosed", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"result": true, "userInput": "Leo"},
	}))

	if pending := tab.PeekPendingDialog(); pending != nil {
		t.Fatalf("closing must clear the pending dialog, got %+v", pending)
	}
	history := tab.DialogHistory(0)
	if len(history) != 1 {
		t.Fatalf("history = %+v", history)
	}
	ev := history[0]
	if ev.Result == nil || !*ev.Result || ev.UserInput != "Leo" || ev.ClosedAt == 0 {
		t.Fatalf("history entry = %+v", ev)
	}
}

// borz can hold several CDP sessions on the same target, so Chrome delivers the
// same dialog event once per session. The redelivery must not clobber the
// AutoHandled record with an unhandled one — that would make every armed dialog
// look like it is blocking the page.
func TestDialog_DuplicateOpeningDeliveryIsIgnored(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")
	tab.SetDialogHandler(&DialogHandler{Accept: true})

	msg := dialogOpeningMsg(t, "confirm", "Delete this record?", "")
	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", msg)
	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", msg)

	pending := tab.PeekPendingDialog()
	if pending == nil {
		t.Fatal("pending dialog disappeared")
	}
	if !pending.AutoHandled || pending.HandledAs != "accept" {
		t.Fatalf("redelivery clobbered the handled record: %+v", pending)
	}
	if c.unhandledDialog("T1") != nil {
		t.Fatal("an armed dialog must never be reported as blocking")
	}

	// Both sessions also report the close; only the first has anything to
	// resolve, and the copy must not double-count into the history.
	closing := rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"result": true, "userInput": ""},
	})
	c.handleSessionEvent("T1", "Page.javascriptDialogClosed", closing)
	c.handleSessionEvent("T1", "Page.javascriptDialogClosed", closing)
	if history := tab.DialogHistory(0); len(history) != 1 {
		t.Fatalf("history = %+v, want exactly one entry", history)
	}

	// After the close, an identical dialog is a genuinely new one.
	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", msg)
	if pending := tab.PeekPendingDialog(); pending == nil || pending.AutoHandled {
		t.Fatalf("second real dialog = %+v, want a fresh unhandled record", pending)
	}
}

// A closing event with no matching opening (e.g. a dialog that was already up
// when borz attached) must not synthesize a phantom history entry.
func TestDialog_ClosingWithoutOpeningIsIgnored(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")

	c.handleSessionEvent("T1", "Page.javascriptDialogClosed", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"result": false},
	}))

	if got := tab.DialogHistory(0); len(got) != 0 {
		t.Fatalf("closing with no open dialog should record nothing, got %+v", got)
	}
}

func TestDialog_HistoryIsCappedByLimit(t *testing.T) {
	tab := newTabState("T1", "t1", func() int { return 0 })
	for i := 0; i < 5; i++ {
		tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "alert", Message: string(rune('a' + i))})
		tab.ResolvePendingDialog(true, "", time.Now())
	}
	if got := tab.DialogHistory(2); len(got) != 2 || got[1].Message != "e" {
		t.Fatalf("DialogHistory(2) = %+v", got)
	}
	if got := tab.DialogHistory(0); len(got) != 5 {
		t.Fatalf("DialogHistory(0) should return all, got %d", len(got))
	}
}

func TestDialog_MarkPendingHandled(t *testing.T) {
	tab := newTabState("T1", "t1", func() int { return 0 })

	// No pending dialog: must be a no-op, not a panic.
	tab.MarkPendingDialogHandled(true, "x")

	tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "confirm", Message: "sure?"})
	tab.MarkPendingDialogHandled(false, "")
	pending := tab.PeekPendingDialog()
	if !pending.AutoHandled || pending.HandledAs != "dismiss" {
		t.Fatalf("pending = %+v", pending)
	}
}

// PeekPendingDialog hands out a copy so callers can't mutate tab state.
func TestDialog_PeekReturnsCopy(t *testing.T) {
	tab := newTabState("T1", "t1", func() int { return 0 })
	tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "alert", Message: "hi"})

	got := tab.PeekPendingDialog()
	got.Message = "mutated"
	if again := tab.PeekPendingDialog(); again.Message != "hi" {
		t.Fatalf("PeekPendingDialog leaked a mutable pointer: %q", again.Message)
	}
}

func TestDialog_BlockedByDialogMethods(t *testing.T) {
	blocked := []string{"Runtime.evaluate", "DOM.getDocument", "Accessibility.getFullAXTree", "Page.captureScreenshot"}
	for _, method := range blocked {
		if !blockedByDialog(method) {
			t.Errorf("%s should be treated as renderer-blocking", method)
		}
	}
	// Page.handleJavaScriptDialog is how a dialog gets resolved; blocking it
	// would make an open dialog unrecoverable.
	allowed := []string{"Page.handleJavaScriptDialog", "Page.enable", "Page.navigate", "Input.dispatchKeyEvent", "Target.activateTarget"}
	for _, method := range allowed {
		if blockedByDialog(method) {
			t.Errorf("%s must not be blocked by an open dialog", method)
		}
	}
}

func TestDialog_UnhandledDialogLookup(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")

	if ev := c.unhandledDialog("T1"); ev != nil {
		t.Fatalf("no dialog open, got %+v", ev)
	}
	if ev := c.unhandledDialog("missing"); ev != nil {
		t.Fatalf("unknown tab should report no dialog, got %+v", ev)
	}

	// An auto-handled dialog is resolving already — reporting it would fail
	// commands that raced a correctly armed handler.
	tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "alert", Message: "hi", AutoHandled: true})
	if ev := c.unhandledDialog("T1"); ev != nil {
		t.Fatalf("auto-handled dialog must not count as blocking, got %+v", ev)
	}

	tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "confirm", Message: "hi"})
	if ev := c.unhandledDialog("T1"); ev == nil {
		t.Fatal("unhandled dialog should be reported as blocking")
	}
}

func TestDialog_BlockedErrorNamesTheDialog(t *testing.T) {
	err := dialogBlockedError("Runtime.evaluate", &protocol.DialogEventInfo{Type: "confirm", Message: "Delete this record?"})
	for _, want := range []string{"Runtime.evaluate", "confirm", "Delete this record?", "borz dialog accept"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}

	// A dialog with no type still produces a usable message.
	err = dialogBlockedError("DOM.getDocument", &protocol.DialogEventInfo{Message: "x"})
	if !strings.Contains(err.Error(), "javascript dialog") {
		t.Errorf("untyped dialog error = %q", err)
	}
}

func TestDialog_TruncateMessage(t *testing.T) {
	if got := truncateDialogMessage("short"); got != "short" {
		t.Fatalf("short message was altered: %q", got)
	}
	long := strings.Repeat("x", 500)
	got := truncateDialogMessage(long)
	if len(got) >= len(long) || !strings.HasSuffix(got, "…") {
		t.Fatalf("long message not truncated: len=%d", len(got))
	}
}

// The whole point of the fail-fast path: a blocked command must return an
// actionable error immediately instead of sitting out the command timeout.
func TestDialog_SessionCommandFailsFastWhenBlocked(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)
	c.AttachAndEnable("T1")

	tab := c.TabManager.GetTab("T1")
	if tab == nil {
		t.Fatal("tab state missing")
	}
	tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "confirm", Message: "Delete this record?"})

	start := time.Now()
	_, err := c.SessionCommandWithTimeout("T1", "Runtime.evaluate", map[string]interface{}{"expression": "1"}, 5*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Runtime.evaluate should fail while a dialog blocks the tab")
	}
	if !strings.Contains(err.Error(), "Delete this record?") {
		t.Fatalf("error should name the dialog: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("fail-fast took %v — it waited on the timeout instead", elapsed)
	}

	// Resolving the dialog unblocks the tab again.
	tab.ResolvePendingDialog(false, "", time.Now())
	if _, err := c.SessionCommand("T1", "Runtime.evaluate", map[string]interface{}{"expression": "1"}); err != nil {
		t.Fatalf("Runtime.evaluate after resolving: %v", err)
	}
}

func TestDialog_HandleJavaScriptDialogIsNeverBlocked(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)
	c.AttachAndEnable("T1")
	c.TabManager.GetTab("T1").SetPendingDialog(&protocol.DialogEventInfo{Type: "confirm", Message: "stuck"})

	if _, err := c.SessionCommand("T1", "Page.handleJavaScriptDialog", map[string]interface{}{"accept": true}); err != nil {
		t.Fatalf("Page.handleJavaScriptDialog must work while a dialog is open: %v", err)
	}
}

// --- dispatch-level behavior ---

func dispatchDialog(t *testing.T, c *CdpConnection, command, promptText string) *protocol.Response {
	t.Helper()
	return DispatchRequest(c, &protocol.Request{
		ID: "d", Action: protocol.ActionDialog, DialogResponse: command, PromptText: promptText,
	})
}

func dialogInfo(t *testing.T, resp *protocol.Response) map[string]interface{} {
	t.Helper()
	if resp.Data == nil {
		t.Fatalf("response has no data: %+v", resp)
	}
	info, ok := resp.Data.DialogInfo.(map[string]interface{})
	if !ok {
		t.Fatalf("dialogInfo is %T: %+v", resp.Data.DialogInfo, resp.Data.DialogInfo)
	}
	return info
}

func TestDispatchDialog_ArmsWhenNothingIsOpen(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	info := dialogInfo(t, dispatchDialog(t, c, "dismiss", ""))
	if info["type"] != "armed" || info["action"] != "dismiss" {
		t.Fatalf("dialogInfo = %+v", info)
	}
	handler := c.TabManager.GetTab("T1").PeekDialogHandler()
	if handler == nil || handler.Accept {
		t.Fatalf("handler = %+v", handler)
	}
}

// Empty DialogResponse keeps the historical default of "accept".
func TestDispatchDialog_DefaultsToAccept(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	info := dialogInfo(t, dispatchDialog(t, c, "", ""))
	if info["action"] != "accept" {
		t.Fatalf("dialogInfo = %+v", info)
	}
}

// A dialog that is already open must be answered, not queued behind an arm —
// arming would leave the page blocked forever.
func TestDispatchDialog_AnswersAlreadyOpenDialog(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	var handled atomic.Value
	f.On("Page.handleJavaScriptDialog", func(params json.RawMessage) (interface{}, error) {
		handled.Store(string(params))
		return map[string]interface{}{}, nil
	})
	c := connectCdp(t, f)
	c.AttachAndEnable("T1")
	tab := c.TabManager.GetTab("T1")
	tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "prompt", Message: "Your name?"})

	info := dialogInfo(t, dispatchDialog(t, c, "accept", "Leo"))
	if info["type"] != "handled" || info["handled"] != true {
		t.Fatalf("dialogInfo = %+v", info)
	}
	raw, _ := handled.Load().(string)
	if !strings.Contains(raw, `"accept":true`) || !strings.Contains(raw, `"promptText":"Leo"`) {
		t.Fatalf("Page.handleJavaScriptDialog params = %s", raw)
	}
	// Nothing was armed — the request answered the open dialog instead.
	if h := tab.PeekDialogHandler(); h != nil {
		t.Fatalf("answering an open dialog must not arm a handler: %+v", h)
	}
	// And the tab stops reporting as blocked right away, without waiting for
	// the closing event.
	if ev := c.unhandledDialog("T1"); ev != nil {
		t.Fatalf("tab still reports as blocked: %+v", ev)
	}
}

func TestDispatchDialog_StatusReportsOpenDialogAndHistory(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)
	c.AttachAndEnable("T1")
	tab := c.TabManager.GetTab("T1")

	// Clean state.
	info := dialogInfo(t, dispatchDialog(t, c, "status", ""))
	if info["armed"] != false || info["blocked"] != false || info["message"] != "No dialog open" {
		t.Fatalf("clean status = %+v", info)
	}
	if _, ok := info["history"]; ok {
		t.Fatalf("clean status should have no history: %+v", info)
	}

	// Armed + blocked.
	dispatchDialog(t, c, "accept", "Leo")
	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", dialogOpeningMsg(t, "confirm", "Delete this record?", ""))
	tab.SetPendingDialog(&protocol.DialogEventInfo{Type: "confirm", Message: "Delete this record?"})

	info = dialogInfo(t, dispatchDialog(t, c, "status", ""))
	if info["blocked"] != true {
		t.Fatalf("status should report the tab as blocked: %+v", info)
	}
	pending, ok := info["pending"].(*protocol.DialogEventInfo)
	if !ok || pending.Message != "Delete this record?" {
		t.Fatalf("status pending = %+v", info["pending"])
	}

	// Status is read-only: it must not consume the pending dialog.
	if tab.PeekPendingDialog() == nil {
		t.Fatal("status consumed the pending dialog")
	}

	// History appears once a dialog closes.
	c.handleSessionEvent("T1", "Page.javascriptDialogClosed", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"result": true},
	}))
	info = dialogInfo(t, dispatchDialog(t, c, "status", ""))
	history, ok := info["history"].([]protocol.DialogEventInfo)
	if !ok || len(history) != 1 || history[0].Message != "Delete this record?" {
		t.Fatalf("status history = %+v", info["history"])
	}
}

func TestDispatchDialog_Disarm(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	dispatchDialog(t, c, "accept", "")
	info := dialogInfo(t, dispatchDialog(t, c, "disarm", ""))
	if info["type"] != "disarmed" || info["armed"] != false {
		t.Fatalf("dialogInfo = %+v", info)
	}
	if h := c.TabManager.GetTab("T1").PeekDialogHandler(); h != nil {
		t.Fatalf("handler survived disarm: %+v", h)
	}
}

// A typo used to silently arm "accept" — on a destructive confirm() that is
// the opposite of what the caller asked for.
func TestDispatchDialog_UnknownSubcommandIsRejected(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	for _, bad := range []string{"dismis", "cancel", "disarmed", "no"} {
		resp := dispatchDialog(t, c, bad, "")
		if resp.Success {
			t.Fatalf("%q should be rejected, got %+v", bad, resp)
		}
		if !strings.Contains(resp.Error, "unknown dialog subcommand") {
			t.Fatalf("%q error = %q", bad, resp.Error)
		}
		if h := c.TabManager.GetTab("T1").PeekDialogHandler(); h != nil {
			t.Fatalf("%q armed a handler anyway: %+v", bad, h)
		}
	}
}
