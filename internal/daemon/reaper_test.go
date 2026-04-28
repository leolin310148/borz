package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeCloser struct {
	mu     sync.Mutex
	closed []string
	fail   map[string]error
}

func (f *fakeCloser) BrowserCommand(method string, params interface{}) (json.RawMessage, error) {
	if method != "Target.closeTarget" {
		return nil, errors.New("unexpected method: " + method)
	}
	p, _ := params.(map[string]interface{})
	id, _ := p["targetId"].(string)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.fail[id]; ok {
		return nil, err
	}
	f.closed = append(f.closed, id)
	return json.RawMessage("{}"), nil
}

func (f *fakeCloser) closedSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.closed))
	copy(out, f.closed)
	return out
}

func TestReapOnce_ClosesIdleNonActiveTabs(t *testing.T) {
	tm := NewTabStateManager()
	idle := tm.AddTab("idle-tab")
	fresh := tm.AddTab("fresh-tab")
	active := tm.AddTab("active-tab")

	// Manually backdate idle, leave fresh and active recent.
	now := time.Now()
	idle.CreatedAt = now.Add(-2 * time.Hour)
	idle.lastActionUnixNano.Store(now.Add(-90 * time.Minute).UnixNano())
	fresh.CreatedAt = now.Add(-1 * time.Minute)
	active.CreatedAt = now.Add(-2 * time.Hour)
	active.lastActionUnixNano.Store(now.Add(-90 * time.Minute).UnixNano())

	closer := &fakeCloser{}
	closed := reapOnce(tm, closer, 30*time.Minute, "active-tab", now)

	if len(closed) != 1 || closed[0] != "idle-tab" {
		t.Fatalf("closed=%v want [idle-tab]", closed)
	}
	if got := closer.closedSnapshot(); len(got) != 1 || got[0] != "idle-tab" {
		t.Fatalf("BrowserCommand calls=%v want [idle-tab]", got)
	}
}

func TestReapOnce_NoClosesUnderThreshold(t *testing.T) {
	tm := NewTabStateManager()
	tab := tm.AddTab("t1")
	now := time.Now()
	tab.CreatedAt = now.Add(-29 * time.Minute)

	closer := &fakeCloser{}
	closed := reapOnce(tm, closer, 30*time.Minute, "", now)

	if len(closed) != 0 {
		t.Fatalf("closed=%v want none", closed)
	}
}

func TestReapOnce_UsesLastActionOverCreatedAt(t *testing.T) {
	tm := NewTabStateManager()
	tab := tm.AddTab("t1")
	now := time.Now()
	// Created an hour ago, but acted on 5 min ago — must NOT be closed.
	tab.CreatedAt = now.Add(-1 * time.Hour)
	tab.lastActionUnixNano.Store(now.Add(-5 * time.Minute).UnixNano())

	closer := &fakeCloser{}
	closed := reapOnce(tm, closer, 30*time.Minute, "", now)

	if len(closed) != 0 {
		t.Fatalf("closed=%v want none (recent action), idleSince=%v", closed, tab.IdleSince())
	}
}

func TestReapOnce_NoActionFallsBackToCreatedAt(t *testing.T) {
	tm := NewTabStateManager()
	tab := tm.AddTab("t1")
	now := time.Now()
	tab.CreatedAt = now.Add(-45 * time.Minute) // never acted on, stale by creation alone

	closer := &fakeCloser{}
	closed := reapOnce(tm, closer, 30*time.Minute, "", now)

	if len(closed) != 1 || closed[0] != "t1" {
		t.Fatalf("closed=%v want [t1]", closed)
	}
}

func TestReapOnce_CloserErrorDoesNotPanic(t *testing.T) {
	tm := NewTabStateManager()
	tm.AddTab("t1").CreatedAt = time.Now().Add(-1 * time.Hour)

	closer := &fakeCloser{fail: map[string]error{"t1": errors.New("boom")}}
	closed := reapOnce(tm, closer, 30*time.Minute, "", time.Now())

	if len(closed) != 0 {
		t.Fatalf("expected empty closed slice on error, got %v", closed)
	}
}

func TestRecordAction_StampsLastActionAt(t *testing.T) {
	tm := NewTabStateManager()
	tab := tm.AddTab("t1")
	if !tab.IdleSince().Equal(tab.CreatedAt) {
		t.Fatalf("IdleSince before action: got %v want CreatedAt %v", tab.IdleSince(), tab.CreatedAt)
	}

	before := time.Now()
	tab.RecordAction()
	after := time.Now()

	got := tab.IdleSince()
	if got.Before(before) || got.After(after) {
		t.Fatalf("IdleSince after RecordAction: got %v, want in [%v, %v]", got, before, after)
	}
}

func TestRunIdleTabReaperLoop(t *testing.T) {
	tm := NewTabStateManager()
	tab := tm.AddTab("loop-tab")
	now := time.Now()
	tab.CreatedAt = now.Add(-time.Hour)

	closer := &fakeCloser{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runIdleTabReaper(ctx, tm, closer, 30*time.Minute, time.Millisecond, func() string { return "" }, func() time.Time { return now })
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		if len(closer.closedSnapshot()) > 0 {
			cancel()
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("reaper did not close idle tab")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reaper did not stop after cancellation")
	}
}

func TestRunIdleTabReaperDisabled(t *testing.T) {
	done := make(chan struct{})
	go func() {
		runIdleTabReaper(context.Background(), NewTabStateManager(), &fakeCloser{}, 0, time.Millisecond, func() string { return "" }, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabled reaper should return immediately")
	}
}
