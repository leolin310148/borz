package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCloseTabsOverLimitClosesOldestAndKeepsCurrent(t *testing.T) {
	tm := NewTabStateManager()
	now := time.Now()
	oldest := tm.AddTab("oldest")
	current := tm.AddTab("current")
	newest := tm.AddTab("newest")
	oldest.CreatedAt = now.Add(-3 * time.Hour)
	current.CreatedAt = now.Add(-2 * time.Hour)
	newest.CreatedAt = now.Add(-time.Hour)

	closer := &fakeCloser{}
	closing := map[string]bool{}
	closed := closeTabsOverLimit(
		tm, closer, 2, "current",
		func(id string) bool { return closing[id] },
		func(id string, value bool) { closing[id] = value },
	)
	if len(closed) != 1 || closed[0] != "oldest" {
		t.Fatalf("closed = %v, want [oldest]", closed)
	}
	if !closing["oldest"] {
		t.Fatal("successful close should remain marked in flight")
	}
}

func TestCloseTabsOverLimitSkipsPendingAndRetriesAfterFailure(t *testing.T) {
	tm := NewTabStateManager()
	now := time.Now()
	for i, id := range []string{"pending", "fails", "closes", "current"} {
		tab := tm.AddTab(id)
		tab.CreatedAt = now.Add(time.Duration(i) * time.Minute)
	}
	closer := &fakeCloser{fail: map[string]error{"fails": errors.New("boom")}}
	closing := map[string]bool{"pending": true}
	closed := closeTabsOverLimit(
		tm, closer, 2, "current",
		func(id string) bool { return closing[id] },
		func(id string, value bool) { closing[id] = value },
	)
	if len(closed) != 1 || closed[0] != "closes" {
		t.Fatalf("closed = %v, want [closes]", closed)
	}
	if closing["fails"] {
		t.Fatal("failed close must clear its in-flight marker")
	}
}

func TestCloseTabsOverLimitDisabledOrWithinLimit(t *testing.T) {
	tm := NewTabStateManager()
	tm.AddTab("one")
	closer := &fakeCloser{}
	if got := closeTabsOverLimit(tm, closer, 0, "", nil, nil); len(got) != 0 {
		t.Fatalf("disabled closed %v", got)
	}
	if got := closeTabsOverLimit(tm, closer, 1, "", nil, nil); len(got) != 0 {
		t.Fatalf("within limit closed %v", got)
	}
}

func TestRunTabLimitEnforcer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	go runTabLimitEnforcer(ctx, time.Millisecond, func() { called <- struct{}{} })
	select {
	case <-called:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("tab limit enforcer did not tick")
	}
	runTabLimitEnforcer(context.Background(), 0, func() { t.Fatal("should not run") })
	runTabLimitEnforcer(context.Background(), time.Millisecond, nil)
}
