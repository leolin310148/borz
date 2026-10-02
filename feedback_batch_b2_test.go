package main

import (
	"strings"
	"testing"
)

func TestJQMissingFieldHint(t *testing.T) {
	resp := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"tab": "ab12", "cursor": 3.0, "requestCount": 0.0, "pendingCount": 0.0,
		},
	}
	// #128: wrong field name printed a bare null.
	hint := jqMissingFieldHint(resp, ".data.requests[-20:]")
	if !strings.Contains(hint, `"requests" is not in .data`) || !strings.Contains(hint, "requestCount") {
		t.Fatalf("hint = %q", hint)
	}
	// An omitted empty list is not a typo; point at the safe default.
	hint = jqMissingFieldHint(resp, "[.data.networkRequests[] | .url]")
	if !strings.Contains(hint, "requestCount is 0") || !strings.Contains(hint, "// []") {
		t.Fatalf("empty-list hint = %q", hint)
	}
	if hint := jqMissingFieldHint(resp, ".data.tab"); hint != "" {
		t.Fatalf("existing field must not produce a hint: %q", hint)
	}
	if hint := jqMissingFieldHint(map[string]interface{}{"result": map[string]interface{}{}}, ".result.missing"); hint != "" {
		t.Fatalf("caller-shaped eval results must not produce a hint: %q", hint)
	}
	if hint := jqMissingFieldHint(resp, "keys"); hint != "" {
		t.Fatalf("non-path expressions must not produce a hint: %q", hint)
	}
}

func TestAllNull(t *testing.T) {
	if allNull(nil) || !allNull([]interface{}{nil}) || allNull([]interface{}{nil, "x"}) {
		t.Fatal("allNull mismatch")
	}
}
