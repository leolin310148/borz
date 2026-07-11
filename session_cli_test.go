package main

import "testing"

func TestCLISessionID(t *testing.T) {
	tests := []struct {
		name                     string
		explicit, pane, terminal string
		parentPID                int
		want                     string
	}{
		{name: "explicit wins", explicit: "agent-turn-7", pane: "%9", terminal: "secret", parentPID: 42, want: "agent-turn-7"},
		{name: "tmux pane", pane: "%99", terminal: "secret", parentPID: 42, want: "tmux-99"},
		{name: "invalid pane falls through", pane: "%work", parentPID: 42, want: "cli-42"},
		{name: "parent process", parentPID: 42, want: "cli-42"},
		{name: "last resort", want: "cli"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cliSessionID(tt.explicit, tt.pane, tt.terminal, tt.parentPID); got != tt.want {
				t.Fatalf("cliSessionID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCLISessionIDHashesTerminalIdentifier(t *testing.T) {
	got := cliSessionID("", "", "opaque-sensitive-terminal-id", 42)
	if got != "terminal-689101ad0134" {
		t.Fatalf("terminal session id = %q", got)
	}
	if got == cliSessionID("", "", "another-terminal-id", 42) {
		t.Fatal("distinct terminal identifiers should not share a correlation id")
	}
}
