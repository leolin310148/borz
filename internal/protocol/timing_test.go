package protocol

import (
	"testing"
	"time"
)

func TestCommandTimeoutBudget(t *testing.T) {
	base := 30 * time.Second
	waitMs, preMs, postMs := 30_000, 250, 500
	req := &Request{
		Action: ActionOpen, WaitFor: "#ready", TimeoutMs: &waitMs,
		PreDelayMs: &preMs, PostDelayMs: &postMs,
	}
	if got, want := CommandTimeoutBudget(req, base), 60_750*time.Millisecond; got != want {
		t.Fatalf("CommandTimeoutBudget() = %s, want %s", got, want)
	}

	req.TimeoutMs = nil
	if got, want := CommandTimeoutBudget(req, base), 40_750*time.Millisecond; got != want {
		t.Fatalf("default wait budget = %s, want %s", got, want)
	}
}

func TestCommandTimeoutBudgetLongEvalAndWait(t *testing.T) {
	base := 30 * time.Second
	evalMs := 60_000
	if got := CommandTimeoutBudget(&Request{Action: ActionEval, EvalTimeoutMs: &evalMs}, base); got != time.Minute {
		t.Fatalf("long eval budget = %s", got)
	}

	waitMs := 45_000
	if got := CommandTimeoutBudget(&Request{Action: ActionWait, Ms: &waitMs}, base); got != 75*time.Second {
		t.Fatalf("wait action budget = %s", got)
	}
}
