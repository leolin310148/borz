package daemon

import (
	"strings"
	"testing"
)

func TestLabelTargetErrorExplainsMissingAndAmbiguous(t *testing.T) {
	missing := labelTargetError("Retry", 0, nil).Error()
	if !strings.Contains(missing, `no visible element is labeled exactly "Retry"`) || !strings.Contains(missing, "case-sensitive") {
		t.Fatalf("missing-label error = %q", missing)
	}
	ambiguous := labelTargetError("Dismiss", 2, []string{`<span> "Dismiss"`, `<button> "Dismiss"`}).Error()
	for _, want := range []string{`2 visible controls are labeled exactly "Dismiss"`, `<button> "Dismiss"`, "Refusing to guess"} {
		if !strings.Contains(ambiguous, want) {
			t.Fatalf("ambiguous-label error %q missing %q", ambiguous, want)
		}
	}
}

func TestResolveBackendNodeIDByLabelRejectsBlankLabel(t *testing.T) {
	if _, err := resolveBackendNodeIDByLabel(nil, "tab", "   "); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("blank label err = %v", err)
	}
}
