package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestDispatchWebAuthnVirtualAuthenticatorLifecycle(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T-WA", "https://passkey.test", "Passkey")

	f.On("WebAuthn.enable", emptyCDPSuccess)
	f.On("WebAuthn.disable", emptyCDPSuccess)
	f.On("WebAuthn.addVirtualAuthenticator", func(params json.RawMessage) (interface{}, error) {
		var payload struct {
			Options protocol.VirtualAuthenticatorOptions `json:"options"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, err
		}
		if payload.Options.Protocol != "ctap2" || payload.Options.Transport != "internal" ||
			!payload.Options.HasResidentKey || !payload.Options.HasUserVerification ||
			!payload.Options.IsUserVerified || !payload.Options.AutomaticPresenceSimulation {
			t.Fatalf("unexpected add options: %+v", payload.Options)
		}
		return map[string]interface{}{"authenticatorId": "auth-1"}, nil
	})
	f.On("WebAuthn.getCredentials", func(params json.RawMessage) (interface{}, error) {
		assertAuthenticatorIDParam(t, params, "auth-1")
		return map[string]interface{}{"credentials": []interface{}{
			map[string]interface{}{"credentialId": "cred-1", "rpId": "passkey.test", "signCount": 0},
		}}, nil
	})
	f.On("WebAuthn.setUserVerified", func(params json.RawMessage) (interface{}, error) {
		var payload struct {
			AuthenticatorID string `json:"authenticatorId"`
			IsUserVerified  bool   `json:"isUserVerified"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, err
		}
		if payload.AuthenticatorID != "auth-1" || payload.IsUserVerified {
			t.Fatalf("unexpected setUserVerified params: %+v", payload)
		}
		return map[string]interface{}{}, nil
	})
	f.On("WebAuthn.setAutomaticPresenceSimulation", func(params json.RawMessage) (interface{}, error) {
		var payload struct {
			AuthenticatorID string `json:"authenticatorId"`
			Enabled         bool   `json:"enabled"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, err
		}
		if payload.AuthenticatorID != "auth-1" || payload.Enabled {
			t.Fatalf("unexpected setAutomaticPresenceSimulation params: %+v", payload)
		}
		return map[string]interface{}{}, nil
	})
	f.On("WebAuthn.removeVirtualAuthenticator", func(params json.RawMessage) (interface{}, error) {
		assertAuthenticatorIDParam(t, params, "auth-1")
		return map[string]interface{}{}, nil
	})

	cdp := connectCdp(t, f)
	tab := "T-WA"
	requests := []*protocol.Request{
		{ID: "enable", Action: protocol.ActionWebAuthn, WebAuthnCommand: "enable", TabID: tab},
		{ID: "add", Action: protocol.ActionWebAuthn, WebAuthnCommand: "add", TabID: tab, VirtualAuthenticator: passkeyOptions()},
		{ID: "credentials", Action: protocol.ActionWebAuthn, WebAuthnCommand: "credentials", TabID: tab, AuthenticatorID: "auth-1"},
		{ID: "uv", Action: protocol.ActionWebAuthn, WebAuthnCommand: "set-user-verified", TabID: tab, AuthenticatorID: "auth-1", UserVerified: boolPtr(false)},
		{ID: "presence", Action: protocol.ActionWebAuthn, WebAuthnCommand: "set-automatic-presence", TabID: tab, AuthenticatorID: "auth-1", AutomaticPresence: boolPtr(false)},
		{ID: "remove", Action: protocol.ActionWebAuthn, WebAuthnCommand: "remove", TabID: tab, AuthenticatorID: "auth-1"},
		{ID: "disable", Action: protocol.ActionWebAuthn, WebAuthnCommand: "disable", TabID: tab},
	}
	for _, req := range requests {
		resp := DispatchRequest(cdp, req)
		if !resp.Success {
			t.Fatalf("%s failed: %+v", req.WebAuthnCommand, resp)
		}
		if resp.Data == nil || resp.Data.TabID != tab || resp.Data.Tab == "" {
			t.Fatalf("%s missing target identity: %+v", req.WebAuthnCommand, resp.Data)
		}
		if req.WebAuthnCommand == "add" {
			result := resp.Data.Result.(map[string]interface{})
			if result["authenticatorId"] != "auth-1" {
				t.Fatalf("add result: %#v", result)
			}
		}
		if req.WebAuthnCommand == "credentials" {
			result := resp.Data.Result.(map[string]interface{})
			credentials, ok := result["credentials"].([]interface{})
			if !ok || len(credentials) != 1 || result["authenticatorId"] != "auth-1" {
				t.Fatalf("credentials result: %#v", result)
			}
		}
	}

	var methods []string
	for _, call := range f.Calls() {
		if strings.HasPrefix(call.Method, "WebAuthn.") {
			if call.SessionID == "" {
				t.Fatalf("%s was not target-session scoped", call.Method)
			}
			methods = append(methods, call.Method)
		}
	}
	want := []string{
		"WebAuthn.enable",
		"WebAuthn.addVirtualAuthenticator",
		"WebAuthn.getCredentials",
		"WebAuthn.setUserVerified",
		"WebAuthn.setAutomaticPresenceSimulation",
		"WebAuthn.removeVirtualAuthenticator",
		"WebAuthn.disable",
	}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Fatalf("WebAuthn lifecycle methods = %v, want %v", methods, want)
	}
}

func TestDispatchWebAuthnValidationAndCDPError(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://passkey.test", "Passkey")
	f.On("WebAuthn.removeVirtualAuthenticator", func(json.RawMessage) (interface{}, error) {
		return nil, errors.New("invalid authenticator id")
	})
	cdp := connectCdp(t, f)

	invalid := []*protocol.Request{
		{ID: "missing-options", Action: protocol.ActionWebAuthn, WebAuthnCommand: "add"},
		{ID: "missing-id", Action: protocol.ActionWebAuthn, WebAuthnCommand: "credentials"},
		{ID: "missing-value", Action: protocol.ActionWebAuthn, WebAuthnCommand: "set-user-verified", AuthenticatorID: "auth-1"},
		{ID: "unknown", Action: protocol.ActionWebAuthn, WebAuthnCommand: "raw-cdp"},
	}
	for _, req := range invalid {
		resp := DispatchRequest(cdp, req)
		if resp.Success || strings.TrimSpace(resp.Error) == "" {
			t.Fatalf("%s should fail validation: %+v", req.ID, resp)
		}
	}

	resp := DispatchRequest(cdp, &protocol.Request{
		ID: "cdp-error", Action: protocol.ActionWebAuthn, WebAuthnCommand: "remove", AuthenticatorID: "bad",
	})
	if resp.Success || !strings.Contains(resp.Error, "invalid authenticator id") {
		t.Fatalf("CDP error was not preserved: %+v", resp)
	}
}

func passkeyOptions() *protocol.VirtualAuthenticatorOptions {
	return &protocol.VirtualAuthenticatorOptions{
		Protocol:                    "ctap2",
		Transport:                   "internal",
		HasResidentKey:              true,
		HasUserVerification:         true,
		IsUserVerified:              true,
		AutomaticPresenceSimulation: true,
	}
}

func emptyCDPSuccess(json.RawMessage) (interface{}, error) {
	return map[string]interface{}{}, nil
}

func assertAuthenticatorIDParam(t *testing.T, params json.RawMessage, want string) {
	t.Helper()
	var payload struct {
		AuthenticatorID string `json:"authenticatorId"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AuthenticatorID != want {
		t.Fatalf("authenticatorId = %q, want %q", payload.AuthenticatorID, want)
	}
}
