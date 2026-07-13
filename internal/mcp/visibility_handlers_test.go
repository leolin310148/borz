package mcp

import (
	"context"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestHandlePress_ModifiersAndCommands(t *testing.T) {
	// Malformed arrays are rejected before the request is built.
	res, _ := handlePress(context.Background(), mkReq(map[string]any{"key": "a", "modifiers": "meta"}))
	if !res.IsError {
		t.Error("non-array modifiers should error")
	}
	res, _ = handlePress(context.Background(), mkReq(map[string]any{"key": "a", "commands": []any{42}}))
	if !res.IsError {
		t.Error("non-string command should error")
	}

	cap := capturingSend(t, ok())
	_, _ = handlePress(context.Background(), mkReq(map[string]any{
		"key": "a", "modifiers": []any{"meta"}, "commands": []any{"selectAll", " "},
	}))
	if len(cap.req.Modifiers) != 1 || cap.req.Modifiers[0] != "meta" {
		t.Errorf("modifiers = %v", cap.req.Modifiers)
	}
	if len(cap.req.Commands) != 1 || cap.req.Commands[0] != "selectAll" {
		t.Errorf("commands = %v", cap.req.Commands)
	}
}

func TestHandleTabFront(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleTabFront(context.Background(), mkReq(map[string]any{"tab": "T1"}))
	if cap.req.Action != protocol.ActionTabFront {
		t.Errorf("action = %v", cap.req.Action)
	}
	if cap.req.TabID != "T1" {
		t.Errorf("tab = %v", cap.req.TabID)
	}
}

func TestHandlePageVisibility(t *testing.T) {
	res, _ := handlePageVisibility(context.Background(), mkReq(map[string]any{"state": "sideways"}))
	if !res.IsError {
		t.Error("an unknown state should error")
	}

	cap := capturingSend(t, ok())
	_, _ = handlePageVisibility(context.Background(), mkReq(map[string]any{"state": "VISIBLE", "tab": "T2"}))
	if cap.req.Action != protocol.ActionPageVisibility {
		t.Errorf("action = %v", cap.req.Action)
	}
	if cap.req.Visibility != "visible" {
		t.Errorf("visibility = %q (state should be lowercased)", cap.req.Visibility)
	}
	if cap.req.TabID != "T2" {
		t.Errorf("tab = %v", cap.req.TabID)
	}

	// Omitting state is a status read.
	cap = capturingSend(t, ok())
	_, _ = handlePageVisibility(context.Background(), mkReq(nil))
	if cap.req.Visibility != "" {
		t.Errorf("status read should send no visibility, got %q", cap.req.Visibility)
	}
}

func TestHandleFileChooser(t *testing.T) {
	// accept requires files
	res, _ := handleFileChooser(context.Background(), mkReq(map[string]any{"command": "accept"}))
	if !res.IsError {
		t.Error("accept without files should error")
	}
	res, _ = handleFileChooser(context.Background(), mkReq(map[string]any{"command": "accept", "files": "x"}))
	if !res.IsError {
		t.Error("non-array files should error")
	}

	cap := capturingSend(t, ok())
	_, _ = handleFileChooser(context.Background(), mkReq(map[string]any{
		"command": "accept", "files": []any{"/tmp/a.pdf"}, "tab": "T1",
	}))
	if cap.req.Action != protocol.ActionFileChooser || cap.req.FileChooserCommand != "accept" {
		t.Errorf("request = %+v", cap.req)
	}
	if len(cap.req.Files) != 1 || cap.req.Files[0] != "/tmp/a.pdf" {
		t.Errorf("files = %v", cap.req.Files)
	}

	// No command defaults to status, and status needs no files.
	cap = capturingSend(t, ok())
	_, _ = handleFileChooser(context.Background(), mkReq(nil))
	if cap.req.FileChooserCommand != "status" {
		t.Errorf("default command = %q", cap.req.FileChooserCommand)
	}

	cap = capturingSend(t, ok())
	_, _ = handleFileChooser(context.Background(), mkReq(map[string]any{"command": "cancel"}))
	if cap.req.FileChooserCommand != "cancel" || len(cap.req.Files) != 0 {
		t.Errorf("cancel request = %+v", cap.req)
	}
}
