package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

var validVirtualAuthenticatorProtocols = map[string]bool{
	"ctap2": true,
	"u2f":   true,
}

var validVirtualAuthenticatorTransports = map[string]bool{
	"ble":      true,
	"internal": true,
	"nfc":      true,
	"usb":      true,
}

// dispatchWebAuthn implements the typed WebAuthn virtual-authenticator
// lifecycle for one target session. Keeping the method mapping here prevents
// the public surface from becoming an arbitrary raw-CDP passthrough.
func dispatchWebAuthn(cdp *CdpConnection, targetID string, req *protocol.Request) (map[string]interface{}, error) {
	command := strings.ToLower(strings.TrimSpace(req.WebAuthnCommand))
	switch command {
	case "enable":
		if _, err := cdp.SessionCommand(targetID, "WebAuthn.enable", nil); err != nil {
			return nil, err
		}
		return map[string]interface{}{"enabled": true}, nil

	case "disable":
		if _, err := cdp.SessionCommand(targetID, "WebAuthn.disable", nil); err != nil {
			return nil, err
		}
		return map[string]interface{}{"enabled": false}, nil

	case "add":
		opts, err := validateVirtualAuthenticatorOptions(req.VirtualAuthenticator)
		if err != nil {
			return nil, err
		}
		raw, err := cdp.SessionCommand(targetID, "WebAuthn.addVirtualAuthenticator", map[string]interface{}{
			"options": opts,
		})
		if err != nil {
			return nil, err
		}
		result, err := webAuthnResultObject(raw)
		if err != nil {
			return nil, fmt.Errorf("decode WebAuthn.addVirtualAuthenticator result: %w", err)
		}
		authenticatorID, _ := result["authenticatorId"].(string)
		if strings.TrimSpace(authenticatorID) == "" {
			return nil, fmt.Errorf("WebAuthn.addVirtualAuthenticator returned no authenticatorId")
		}
		result["options"] = opts
		return result, nil

	case "credentials":
		authenticatorID, err := requireAuthenticatorID(req.AuthenticatorID)
		if err != nil {
			return nil, err
		}
		raw, err := cdp.SessionCommand(targetID, "WebAuthn.getCredentials", map[string]interface{}{
			"authenticatorId": authenticatorID,
		})
		if err != nil {
			return nil, err
		}
		result, err := webAuthnResultObject(raw)
		if err != nil {
			return nil, fmt.Errorf("decode WebAuthn.getCredentials result: %w", err)
		}
		result["authenticatorId"] = authenticatorID
		if _, ok := result["credentials"]; !ok {
			result["credentials"] = []interface{}{}
		}
		return result, nil

	case "remove":
		authenticatorID, err := requireAuthenticatorID(req.AuthenticatorID)
		if err != nil {
			return nil, err
		}
		if _, err := cdp.SessionCommand(targetID, "WebAuthn.removeVirtualAuthenticator", map[string]interface{}{
			"authenticatorId": authenticatorID,
		}); err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"authenticatorId": authenticatorID,
			"removed":         true,
		}, nil

	case "set-user-verified":
		authenticatorID, err := requireAuthenticatorID(req.AuthenticatorID)
		if err != nil {
			return nil, err
		}
		if req.UserVerified == nil {
			return nil, fmt.Errorf("userVerified is required for set-user-verified")
		}
		if _, err := cdp.SessionCommand(targetID, "WebAuthn.setUserVerified", map[string]interface{}{
			"authenticatorId": authenticatorID,
			"isUserVerified":  *req.UserVerified,
		}); err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"authenticatorId": authenticatorID,
			"isUserVerified":  *req.UserVerified,
		}, nil

	case "set-automatic-presence":
		authenticatorID, err := requireAuthenticatorID(req.AuthenticatorID)
		if err != nil {
			return nil, err
		}
		if req.AutomaticPresence == nil {
			return nil, fmt.Errorf("automaticPresence is required for set-automatic-presence")
		}
		if _, err := cdp.SessionCommand(targetID, "WebAuthn.setAutomaticPresenceSimulation", map[string]interface{}{
			"authenticatorId": authenticatorID,
			"enabled":         *req.AutomaticPresence,
		}); err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"authenticatorId":             authenticatorID,
			"automaticPresenceSimulation": *req.AutomaticPresence,
		}, nil

	default:
		return nil, fmt.Errorf("unknown WebAuthn command %q (expected enable, disable, add, credentials, remove, set-user-verified, or set-automatic-presence)", req.WebAuthnCommand)
	}
}

func validateVirtualAuthenticatorOptions(opts *protocol.VirtualAuthenticatorOptions) (*protocol.VirtualAuthenticatorOptions, error) {
	if opts == nil {
		return nil, fmt.Errorf("virtualAuthenticator options are required for add")
	}
	normalized := *opts
	normalized.Protocol = strings.ToLower(strings.TrimSpace(normalized.Protocol))
	normalized.Transport = strings.ToLower(strings.TrimSpace(normalized.Transport))
	if !validVirtualAuthenticatorProtocols[normalized.Protocol] {
		return nil, fmt.Errorf("protocol must be ctap2 or u2f")
	}
	if !validVirtualAuthenticatorTransports[normalized.Transport] {
		return nil, fmt.Errorf("transport must be one of: internal, usb, nfc, ble")
	}
	if normalized.IsUserVerified && !normalized.HasUserVerification {
		return nil, fmt.Errorf("isUserVerified=true requires hasUserVerification=true")
	}
	if normalized.Protocol == "u2f" && (normalized.HasResidentKey || normalized.HasUserVerification || normalized.IsUserVerified) {
		return nil, fmt.Errorf("u2f does not support resident keys or user verification; set hasResidentKey, hasUserVerification, and isUserVerified to false")
	}
	return &normalized, nil
}

func requireAuthenticatorID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", fmt.Errorf("authenticatorId is required")
	}
	return id, nil
}

func webAuthnResultObject(raw json.RawMessage) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	if len(raw) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}
