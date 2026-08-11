package protocol

import "time"

const defaultWaitForTimeout = 10 * time.Second

// CommandTimeoutBudget returns the transport/server deadline needed for a
// request. The base duration covers the browser action itself; explicitly
// requested waits and delays are additive so they cannot race the generic
// command timeout and hide a more useful action-specific error.
func CommandTimeoutBudget(req *Request, base time.Duration) time.Duration {
	if req == nil {
		return base
	}

	actionBudget := base
	if req.Action == ActionEval && req.EvalTimeoutMs != nil && *req.EvalTimeoutMs > 0 {
		actionBudget = maxDuration(actionBudget, milliseconds(*req.EvalTimeoutMs))
	}
	if req.Action == ActionWait && req.Ms != nil && *req.Ms > 0 {
		actionBudget = saturatingAdd(actionBudget, milliseconds(*req.Ms))
	}

	budget := actionBudget
	if req.WaitFor != "" {
		waitBudget := defaultWaitForTimeout
		if req.TimeoutMs != nil && *req.TimeoutMs >= 0 {
			waitBudget = milliseconds(*req.TimeoutMs)
		}
		budget = saturatingAdd(budget, waitBudget)
	}
	if req.PreDelayMs != nil && *req.PreDelayMs > 0 {
		budget = saturatingAdd(budget, milliseconds(*req.PreDelayMs))
	}
	if req.PostDelayMs != nil && *req.PostDelayMs > 0 {
		budget = saturatingAdd(budget, milliseconds(*req.PostDelayMs))
	}
	return budget
}

func milliseconds(ms int) time.Duration {
	const maxMilliseconds = int64(^uint64(0)>>1) / int64(time.Millisecond)
	if int64(ms) > maxMilliseconds {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(ms) * time.Millisecond
}

func saturatingAdd(a, b time.Duration) time.Duration {
	const maxDurationValue = time.Duration(1<<63 - 1)
	if b > 0 && a > maxDurationValue-b {
		return maxDurationValue
	}
	return a + b
}

func maxDuration(a, b time.Duration) time.Duration {
	if b > a {
		return b
	}
	return a
}
