package daemon

import "testing"

func TestRedactSensitiveHeaders(t *testing.T) {
	in := map[string]string{
		"Authorization":      "Bearer abc",
		"X-CSRF-Token":       "csrf-value",
		"Cookie":             "sid=1",
		"set-cookie":         "sid=2",
		"X-Session-Id":       "s",
		"x-api-key":          "k",
		"Content-Type":       "application/json",
		"X-JCO-Execution-Id": "run-1",
	}
	out := redactSensitiveHeaders(in)
	for _, name := range []string{"Authorization", "X-CSRF-Token", "Cookie", "set-cookie", "X-Session-Id", "x-api-key"} {
		if out[name] != redactedHeaderValue {
			t.Fatalf("%s = %q, want redacted", name, out[name])
		}
	}
	if out["Content-Type"] != "application/json" || out["X-JCO-Execution-Id"] != "run-1" {
		t.Fatalf("non-sensitive headers changed: %+v", out)
	}
	if in["Authorization"] != "Bearer abc" {
		t.Fatal("redaction mutated the captured headers")
	}
	if got := redactSensitiveHeaders(nil); got != nil {
		t.Fatalf("nil headers = %+v", got)
	}
}
