package main

import (
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestBuildWebAuthnRequestPasskeyDefaultsAndControls(t *testing.T) {
	req, err := buildWebAuthnRequest([]string{"add"}, []string{"webauthn", "add", "--tab", "T1"}, "T1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Action != protocol.ActionWebAuthn || req.WebAuthnCommand != "add" || req.TabID != "T1" {
		t.Fatalf("request routing = %+v", req)
	}
	opts := req.VirtualAuthenticator
	if opts == nil || opts.Protocol != "ctap2" || opts.Transport != "internal" ||
		!opts.HasResidentKey || !opts.HasUserVerification || !opts.IsUserVerified ||
		!opts.AutomaticPresenceSimulation {
		t.Fatalf("Passkey defaults = %+v", opts)
	}

	req, err = buildWebAuthnRequest(
		[]string{"add"},
		[]string{
			"webauthn", "add",
			"--protocol", "u2f",
			"--transport=usb",
			"--has-resident-key=false",
			"--has-user-verification", "false",
			"--is-user-verified=false",
			"--automatic-presence", "false",
		},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	opts = req.VirtualAuthenticator
	if opts.Protocol != "u2f" || opts.Transport != "usb" || opts.HasResidentKey ||
		opts.HasUserVerification || opts.IsUserVerified || opts.AutomaticPresenceSimulation {
		t.Fatalf("custom U2F options = %+v", opts)
	}

	req, err = buildWebAuthnRequest(
		[]string{"set-user-verified", "auth-1", "false"},
		[]string{"webauthn", "set-user-verified", "auth-1", "false"},
		"T2",
	)
	if err != nil || req.UserVerified == nil || *req.UserVerified || req.AuthenticatorID != "auth-1" {
		t.Fatalf("set-user-verified request = %+v, err=%v", req, err)
	}
	req, err = buildWebAuthnRequest(
		[]string{"set-automatic-presence", "auth-1", "true"},
		[]string{"webauthn", "set-automatic-presence", "auth-1", "true"},
		"T2",
	)
	if err != nil || req.AutomaticPresence == nil || !*req.AutomaticPresence {
		t.Fatalf("set-automatic-presence request = %+v, err=%v", req, err)
	}
}

func TestBuildWebAuthnRequestAliasesAndValidation(t *testing.T) {
	for alias, canonical := range map[string]string{
		"add-authenticator":    "add",
		"list-credentials":     "credentials",
		"remove-authenticator": "remove",
	} {
		args := []string{alias}
		if canonical != "add" {
			args = append(args, "auth-1")
		}
		req, err := buildWebAuthnRequest(args, append([]string{"webauthn"}, args...), "")
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if req.WebAuthnCommand != canonical {
			t.Fatalf("%s canonicalized to %q", alias, req.WebAuthnCommand)
		}
	}

	tests := []struct {
		name string
		cmd  []string
		raw  []string
		want string
	}{
		{"missing subcommand", nil, []string{"webauthn"}, "Usage"},
		{"unknown subcommand", []string{"raw"}, []string{"webauthn", "raw"}, "unknown webauthn subcommand"},
		{"bad protocol", []string{"add"}, []string{"webauthn", "add", "--protocol", "ctap3"}, "--protocol"},
		{"missing bool", []string{"add"}, []string{"webauthn", "add", "--automatic-presence"}, "requires true or false"},
		{"bad bool", []string{"add"}, []string{"webauthn", "add", "--automatic-presence", "yes"}, "must be true or false"},
		{"inconsistent uv", []string{"add"}, []string{"webauthn", "add", "--has-user-verification=false"}, "requires --has-user-verification=true"},
		{"u2f passkey defaults", []string{"add"}, []string{"webauthn", "add", "--protocol=u2f"}, "u2f requires"},
		{"missing id", []string{"credentials"}, []string{"webauthn", "credentials"}, "authenticator-id"},
		{"missing control value", []string{"set-user-verified", "auth-1"}, []string{"webauthn", "set-user-verified", "auth-1"}, "true|false"},
		{"bad control value", []string{"set-user-verified", "auth-1", "1"}, []string{"webauthn", "set-user-verified", "auth-1", "1"}, "must be true or false"},
		{"add flag on enable", []string{"enable"}, []string{"webauthn", "enable", "--protocol", "ctap2"}, "only valid with webauthn add"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildWebAuthnRequest(tc.cmd, tc.raw, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestMainDispatchesWebAuthnCommand(t *testing.T) {
	out, requests := runMainWithFakeDaemon(t,
		"webauthn", "add", "--tab", "passkey-tab", "--automatic-presence=false", "--json",
	)
	if len(requests) != 1 {
		t.Fatalf("requests = %d, output=%q", len(requests), out)
	}
	req := requests[0]
	if req.Action != protocol.ActionWebAuthn || req.WebAuthnCommand != "add" ||
		req.TabID != "passkey-tab" || req.VirtualAuthenticator == nil ||
		req.VirtualAuthenticator.AutomaticPresenceSimulation {
		t.Fatalf("dispatched request = %+v", req)
	}
	if !strings.Contains(out, `"action"`) && !strings.Contains(out, `"success": true`) {
		t.Fatalf("JSON output = %q", out)
	}
}

func TestWebAuthnCommandSpecificHelp(t *testing.T) {
	for subcommand, wants := range map[string][]string{
		"enable":                 {"target-session scoped", "before adding"},
		"add":                    {"--has-resident-key <true|false>", "--automatic-presence <true|false>", "data.result.authenticatorId"},
		"credentials":            {"data.result.credentials", "list-credentials"},
		"remove":                 {"remove-authenticator", "webauthn disable"},
		"set-user-verified":      {"<true|false>", "failed/not-completed"},
		"set-automatic-presence": {"<true|false>", "remain pending"},
		"disable":                {"clears virtual-authenticator state"},
	} {
		t.Run(subcommand, func(t *testing.T) {
			key := resolveHelpKey("webauthn", []string{subcommand})
			if key != "webauthn."+subcommand {
				t.Fatalf("resolved help key = %q", key)
			}
			out := captureStdout(t, func() {
				if !printCommandHelp(key) {
					t.Fatalf("missing help entry %s", key)
				}
			})
			for _, want := range wants {
				if !strings.Contains(out, want) {
					t.Errorf("%s help missing %q:\n%s", subcommand, want, out)
				}
			}
		})
	}
}
