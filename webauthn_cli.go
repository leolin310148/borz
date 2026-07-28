package main

import (
	"fmt"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

var webAuthnAddFlags = []string{
	"--protocol",
	"--transport",
	"--has-resident-key",
	"--has-user-verification",
	"--is-user-verified",
	"--automatic-presence",
}

func handleWebAuthn(cmdArgs, rawArgs []string, jsonOutput bool, tabID string) {
	req, err := buildWebAuthnRequest(cmdArgs, rawArgs, tabID)
	if err != nil {
		fatal(err.Error())
		return
	}
	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		if resp.Data == nil {
			return
		}
		result, _ := resp.Data.Result.(map[string]interface{})
		switch req.WebAuthnCommand {
		case "enable":
			fmt.Printf("WebAuthn enabled for tab %s\n", resp.Data.Tab)
		case "disable":
			fmt.Printf("WebAuthn disabled for tab %s\n", resp.Data.Tab)
		case "add":
			fmt.Printf("Virtual authenticator added: %s (tab: %s)\n", resultString(result, "authenticatorId"), resp.Data.Tab)
		case "credentials":
			printJSON(result)
		case "remove":
			fmt.Printf("Virtual authenticator removed: %s\n", req.AuthenticatorID)
		case "set-user-verified":
			fmt.Printf("Virtual authenticator %s user verification: %t\n", req.AuthenticatorID, *req.UserVerified)
		case "set-automatic-presence":
			fmt.Printf("Virtual authenticator %s automatic presence: %t\n", req.AuthenticatorID, *req.AutomaticPresence)
		}
	})
}

func buildWebAuthnRequest(cmdArgs, rawArgs []string, tabID string) (*protocol.Request, error) {
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("Usage: borz webauthn <enable|disable|add|credentials|remove|set-user-verified|set-automatic-presence> [options]")
	}

	command := strings.ToLower(strings.TrimSpace(cmdArgs[0]))
	switch command {
	case "add-authenticator":
		command = "add"
	case "list-credentials":
		command = "credentials"
	case "remove-authenticator":
		command = "remove"
	}

	req := &protocol.Request{
		ID:              newID(),
		Action:          protocol.ActionWebAuthn,
		WebAuthnCommand: command,
	}
	setTab(req, tabID)

	switch command {
	case "enable", "disable":
		if len(cmdArgs) != 1 {
			return nil, fmt.Errorf("webauthn %s takes no positional arguments", command)
		}
		if flag := firstPresentFlag(rawArgs, webAuthnAddFlags); flag != "" {
			return nil, fmt.Errorf("%s is only valid with webauthn add", flag)
		}

	case "add":
		if len(cmdArgs) != 1 {
			return nil, fmt.Errorf("webauthn add takes no positional arguments")
		}
		opts := &protocol.VirtualAuthenticatorOptions{
			Protocol:                    "ctap2",
			Transport:                   "internal",
			HasResidentKey:              true,
			HasUserVerification:         true,
			IsUserVerified:              true,
			AutomaticPresenceSimulation: true,
		}
		if value, ok := getArgValueOK(rawArgs, "--protocol"); ok {
			opts.Protocol = strings.ToLower(strings.TrimSpace(value))
			if opts.Protocol != "ctap2" && opts.Protocol != "u2f" {
				return nil, fmt.Errorf("--protocol must be ctap2 or u2f")
			}
		}
		if value, ok := getArgValueOK(rawArgs, "--transport"); ok {
			opts.Transport = strings.ToLower(strings.TrimSpace(value))
			switch opts.Transport {
			case "internal", "usb", "nfc", "ble":
			default:
				return nil, fmt.Errorf("--transport must be one of: internal, usb, nfc, ble")
			}
		}
		var err error
		if opts.HasResidentKey, err = boolFlagValue(rawArgs, "--has-resident-key", true); err != nil {
			return nil, err
		}
		if opts.HasUserVerification, err = boolFlagValue(rawArgs, "--has-user-verification", true); err != nil {
			return nil, err
		}
		if opts.IsUserVerified, err = boolFlagValue(rawArgs, "--is-user-verified", true); err != nil {
			return nil, err
		}
		if opts.AutomaticPresenceSimulation, err = boolFlagValue(rawArgs, "--automatic-presence", true); err != nil {
			return nil, err
		}
		if _, err := validateWebAuthnCLIOptions(opts); err != nil {
			return nil, err
		}
		req.VirtualAuthenticator = opts

	case "credentials", "remove":
		if len(cmdArgs) != 2 || strings.TrimSpace(cmdArgs[1]) == "" {
			return nil, fmt.Errorf("Usage: borz webauthn %s <authenticator-id>", command)
		}
		if flag := firstPresentFlag(rawArgs, webAuthnAddFlags); flag != "" {
			return nil, fmt.Errorf("%s is only valid with webauthn add", flag)
		}
		req.AuthenticatorID = strings.TrimSpace(cmdArgs[1])

	case "set-user-verified", "set-automatic-presence":
		if len(cmdArgs) != 3 || strings.TrimSpace(cmdArgs[1]) == "" {
			return nil, fmt.Errorf("Usage: borz webauthn %s <authenticator-id> <true|false>", command)
		}
		if flag := firstPresentFlag(rawArgs, webAuthnAddFlags); flag != "" {
			return nil, fmt.Errorf("%s is only valid with webauthn add", flag)
		}
		value, err := parseStrictBool(cmdArgs[2])
		if err != nil {
			return nil, fmt.Errorf("webauthn %s value %w", command, err)
		}
		req.AuthenticatorID = strings.TrimSpace(cmdArgs[1])
		if command == "set-user-verified" {
			req.UserVerified = &value
		} else {
			req.AutomaticPresence = &value
		}

	default:
		return nil, fmt.Errorf("unknown webauthn subcommand %q; expected enable, disable, add, credentials, remove, set-user-verified, or set-automatic-presence", cmdArgs[0])
	}
	return req, nil
}

func boolFlagValue(args []string, name string, defaultValue bool) (bool, error) {
	raw, ok := getArgValueOK(args, name)
	if !ok {
		return defaultValue, nil
	}
	if strings.TrimSpace(raw) == "" {
		return false, fmt.Errorf("%s requires true or false", name)
	}
	value, err := parseStrictBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s %w", name, err)
	}
	return value, nil
}

func parseStrictBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("must be true or false")
	}
}

func validateWebAuthnCLIOptions(opts *protocol.VirtualAuthenticatorOptions) (*protocol.VirtualAuthenticatorOptions, error) {
	if opts.IsUserVerified && !opts.HasUserVerification {
		return nil, fmt.Errorf("--is-user-verified=true requires --has-user-verification=true")
	}
	if opts.Protocol == "u2f" && (opts.HasResidentKey || opts.HasUserVerification || opts.IsUserVerified) {
		return nil, fmt.Errorf("u2f requires --has-resident-key=false --has-user-verification=false --is-user-verified=false")
	}
	return opts, nil
}

func firstPresentFlag(args, names []string) string {
	for _, name := range names {
		if _, ok := getArgValueOK(args, name); ok {
			return name
		}
	}
	return ""
}

func resultString(result map[string]interface{}, key string) string {
	if result == nil {
		return ""
	}
	value, _ := result[key].(string)
	return value
}
