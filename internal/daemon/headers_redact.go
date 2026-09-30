package daemon

import "strings"

// redactedHeaderValue replaces credential-bearing header values in network
// output. Names are kept so callers can still see which headers were sent.
const redactedHeaderValue = "REDACTED"

// redactSensitiveHeaders returns a copy of headers with authentication,
// session, cookie, and CSRF values replaced. The captured ring buffer keeps the
// originals; only the copy handed to clients is redacted.
func redactSensitiveHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		if isSensitiveHeader(name) {
			value = redactedHeaderValue
		}
		out[name] = value
	}
	return out
}

func isSensitiveHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "apikey":
		return true
	}
	for _, marker := range []string{"token", "csrf", "xsrf", "secret", "session", "password", "credential", "signature"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}
