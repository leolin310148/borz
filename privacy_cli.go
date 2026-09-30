package main

import (
	"net/url"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

// Redact only the presentation copy, preserving live URLs used for navigation.
func redactDisplayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid URL]"
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	redact := func(query string) string {
		parts := strings.Split(query, "&")
		for i, part := range parts {
			key, _, found := strings.Cut(part, "=")
			name, err := url.QueryUnescape(key)
			if err != nil {
				parts[i] = "REDACTED"
				continue
			}
			name = strings.ToLower(strings.ReplaceAll(name, "-", "_"))
			if found && (name == "code" || name == "state" || name == "auth" || name == "authorization" || name == "password" || name == "assertion" || name == "samlresponse" || name == "samlrequest" || name == "sig" || strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "signature") || strings.Contains(name, "credential") || name == "api_key" || name == "apikey" || name == "sid" || name == "otp" || name == "ticket" || strings.Contains(name, "session") || strings.Contains(name, "jwt")) {
				parts[i] = key + "=REDACTED"
			}
		}
		return strings.Join(parts, "&")
	}
	u.RawQuery = redact(u.RawQuery)
	if prefix, query, ok := strings.Cut(u.Fragment, "?"); ok {
		u.Fragment = prefix + "?" + redact(query)
	} else {
		u.Fragment = redact(u.Fragment)
	}
	u.RawFragment = ""
	return u.String()
}

// redactResponseURL redacts the URL echoed back by open/navigate so one-time
// login links (Salesforce frontdoor sid, OAuth codes) never reach stdout.
func redactResponseURL(resp *protocol.Response) error {
	if resp != nil && resp.Data != nil && resp.Data.URL != "" {
		resp.Data.URL = redactDisplayURL(resp.Data.URL)
	}
	return nil
}
