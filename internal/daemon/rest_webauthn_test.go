package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestRestBodyWebAuthnDefaultsAndControls(t *testing.T) {
	req, err := (restBody{Command: "add", Tab: "T1"}).webAuthnRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.Action != protocol.ActionWebAuthn || req.TabID != "T1" || req.VirtualAuthenticator == nil {
		t.Fatalf("request = %+v", req)
	}
	opts := req.VirtualAuthenticator
	if opts.Protocol != "ctap2" || opts.Transport != "internal" || !opts.HasResidentKey ||
		!opts.HasUserVerification || !opts.IsUserVerified || !opts.AutomaticPresenceSimulation {
		t.Fatalf("defaults = %+v", opts)
	}

	no := false
	req, err = (restBody{
		Command: "add", Protocol: "u2f", Transport: "usb",
		HasResidentKey: &no, HasUserVerification: &no, IsUserVerified: &no, AutomaticPresence: &no,
	}).webAuthnRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.VirtualAuthenticator.Protocol != "u2f" || req.VirtualAuthenticator.AutomaticPresenceSimulation {
		t.Fatalf("custom add = %+v", req.VirtualAuthenticator)
	}

	req, err = (restBody{
		Command: "set-user-verified", AuthenticatorID: "auth-1", IsUserVerified: &no,
	}).webAuthnRequest()
	if err != nil || req.UserVerified == nil || *req.UserVerified || req.AuthenticatorID != "auth-1" {
		t.Fatalf("set-user-verified = %+v, err=%v", req, err)
	}
}

func TestRestBodyWebAuthnValidation(t *testing.T) {
	tests := []restBody{
		{},
		{Command: "credentials"},
		{Command: "set-user-verified", AuthenticatorID: "auth-1"},
		{Command: "set-automatic-presence", AuthenticatorID: "auth-1"},
		{Command: "add", Protocol: "ctap3"},
	}
	for _, body := range tests {
		if _, err := body.webAuthnRequest(); err == nil {
			t.Fatalf("expected validation error for %+v", body)
		}
	}
}

func TestRestWebAuthnRejectsInvalidBodyBeforeDispatch(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/webauthn",
		strings.NewReader(`{"command":"set-user-verified","authenticatorId":"auth-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	assertRESTErrorEnvelope(t, rec, "isUserVerified is required")
}
