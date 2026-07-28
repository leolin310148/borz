package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestHandleWebAuthnPasskeyDefaultsAndControls(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleWebAuthn(context.Background(), mkReq(map[string]any{
		"command": "add",
		"tab":     "T1",
	}))
	req := cap.req
	if req.Action != protocol.ActionWebAuthn || req.WebAuthnCommand != "add" ||
		req.TabID != "T1" || req.VirtualAuthenticator == nil {
		t.Fatalf("request = %+v", req)
	}
	opts := req.VirtualAuthenticator
	if opts.Protocol != "ctap2" || opts.Transport != "internal" || !opts.HasResidentKey ||
		!opts.HasUserVerification || !opts.IsUserVerified || !opts.AutomaticPresenceSimulation {
		t.Fatalf("defaults = %+v", opts)
	}

	cap = capturingSend(t, ok())
	_, _ = handleWebAuthn(context.Background(), mkReq(map[string]any{
		"command":             "add",
		"protocol":            "u2f",
		"transport":           "usb",
		"hasResidentKey":      false,
		"hasUserVerification": false,
		"isUserVerified":      false,
		"automaticPresence":   false,
	}))
	opts = cap.req.VirtualAuthenticator
	if opts.Protocol != "u2f" || opts.Transport != "usb" || opts.HasResidentKey ||
		opts.HasUserVerification || opts.IsUserVerified || opts.AutomaticPresenceSimulation {
		t.Fatalf("custom options = %+v", opts)
	}

	cap = capturingSend(t, ok())
	_, _ = handleWebAuthn(context.Background(), mkReq(map[string]any{
		"command":         "set-user-verified",
		"authenticatorId": "auth-1",
		"isUserVerified":  false,
	}))
	if cap.req.UserVerified == nil || *cap.req.UserVerified || cap.req.AuthenticatorID != "auth-1" {
		t.Fatalf("set-user-verified request = %+v", cap.req)
	}

	cap = capturingSend(t, ok())
	_, _ = handleWebAuthn(context.Background(), mkReq(map[string]any{
		"command":           "set-automatic-presence",
		"authenticatorId":   "auth-1",
		"automaticPresence": true,
	}))
	if cap.req.AutomaticPresence == nil || !*cap.req.AutomaticPresence {
		t.Fatalf("set-automatic-presence request = %+v", cap.req)
	}
}

func TestHandleWebAuthnValidation(t *testing.T) {
	tests := []map[string]any{
		nil,
		{"command": "raw"},
		{"command": "credentials"},
		{"command": "set-user-verified", "authenticatorId": "auth-1"},
		{"command": "set-automatic-presence", "authenticatorId": "auth-1"},
		{"command": "add", "protocol": "ctap3"},
		{"command": "add", "transport": "hybrid"},
		{"command": "add", "hasUserVerification": false},
		{"command": "add", "protocol": "u2f"},
	}
	for _, args := range tests {
		result, _ := handleWebAuthn(context.Background(), mkReq(args))
		if result == nil || !result.IsError {
			t.Fatalf("args %#v should fail", args)
		}
	}
}

func TestWebAuthnToolSchemaIsTyped(t *testing.T) {
	if webAuthnTool.Name != "browser_webauthn" {
		t.Fatalf("tool name = %q", webAuthnTool.Name)
	}
	command, ok := webAuthnTool.InputSchema.Properties["command"].(map[string]interface{})
	if !ok {
		t.Fatalf("command schema = %#v", webAuthnTool.InputSchema.Properties["command"])
	}
	enum, _ := command["enum"].([]string)
	if !strings.Contains(strings.Join(enum, ","), "set-user-verified") {
		t.Fatalf("command enum = %#v", command["enum"])
	}
	if _, exists := webAuthnTool.InputSchema.Properties["method"]; exists {
		t.Fatal("typed WebAuthn tool must not expose a raw CDP method parameter")
	}
}
