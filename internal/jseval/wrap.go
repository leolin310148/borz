// Package jseval contains helpers for preparing JavaScript scripts before
// they are sent to Runtime.evaluate. Runtime.evaluate refuses top-level
// `await` and only resolves a Promise when the *expression itself* is one,
// so user-written snippets like `await fetch('/x').then(r=>r.json())` need
// to be wrapped in an async IIFE before they will work.
package jseval

import "strings"

// AutoWrapAwait returns the script unchanged unless it has top-level await.
// In that case it wraps the script so Runtime.evaluate (which sets
// awaitPromise:true) can resolve the Promise and return the awaited value.
//
// Wrap shape:
//   - single expression  ->  (async () => { return (<script>) })()
//   - multiple statements -> (async () => { <script> })()
//
// Scripts that already look like an async IIFE (start with `(async`,
// end with `)()`) are left alone so we do not double-wrap.
func AutoWrapAwait(script string) string {
	trimmed := strings.TrimSpace(script)
	if trimmed == "" {
		return script
	}
	if isAsyncIIFE(trimmed) {
		return script
	}
	// Trailing semicolons/whitespace don't make a script multi-statement —
	// strip them before scanning so `await foo();` still gets a `return`.
	scanInput := strings.TrimRight(script, "; \t\r\n")
	hasAwait, hasTopSemi := scanTopLevel(scanInput)
	if !hasAwait {
		return script
	}
	if hasTopSemi {
		return "(async () => { " + script + " })()"
	}
	// Single expression: preserve a return so the caller still gets the value.
	body := strings.TrimRight(strings.TrimSpace(script), ";")
	return "(async () => { return (" + body + ") })()"
}

func isAsyncIIFE(s string) bool {
	if !strings.HasPrefix(s, "(async") {
		return false
	}
	s = strings.TrimRight(s, "; \t\r\n")
	return strings.HasSuffix(s, ")()")
}

// scanTopLevel walks the script ignoring strings, template literals, and
// comments. It reports whether a top-level (depth 0) `await` keyword is
// present and whether any top-level `;` or newline separates statements.
func scanTopLevel(src string) (hasAwait, hasTopSemi bool) {
	const (
		stCode = iota
		stLineComment
		stBlockComment
		stSingle
		stDouble
		stTemplate
	)
	state := stCode
	depth := 0
	i := 0
	for i < len(src) {
		c := src[i]
		switch state {
		case stCode:
			switch {
			case c == '"':
				state = stDouble
				i++
			case c == '\'':
				state = stSingle
				i++
			case c == '`':
				state = stTemplate
				i++
			case c == '/' && i+1 < len(src) && src[i+1] == '/':
				state = stLineComment
				i += 2
			case c == '/' && i+1 < len(src) && src[i+1] == '*':
				state = stBlockComment
				i += 2
			case c == '(' || c == '{' || c == '[':
				depth++
				i++
			case c == ')' || c == '}' || c == ']':
				if depth > 0 {
					depth--
				}
				i++
			case depth == 0 && (c == ';' || c == '\n'):
				hasTopSemi = true
				i++
			case depth == 0 && c == 'a' && matchKeyword(src, i, "await"):
				hasAwait = true
				i += len("await")
			default:
				i++
			}
		case stLineComment:
			if c == '\n' {
				state = stCode
			}
			i++
		case stBlockComment:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				state = stCode
				i += 2
			} else {
				i++
			}
		case stSingle, stDouble:
			quote := byte('\'')
			if state == stDouble {
				quote = '"'
			}
			if c == '\\' && i+1 < len(src) {
				i += 2
			} else if c == quote {
				state = stCode
				i++
			} else {
				i++
			}
		case stTemplate:
			if c == '\\' && i+1 < len(src) {
				i += 2
			} else if c == '`' {
				state = stCode
				i++
			} else {
				// Note: ${...} substitutions can host arbitrary JS, but await
				// inside a template at the top level is exotic enough that we
				// happily ignore it. Returning a false negative here just means
				// we don't auto-wrap; the user can drop --no-auto-await.
				i++
			}
		}
	}
	return
}

// matchKeyword reports whether kw is at src[i:] AND is bounded by
// non-identifier characters on both sides.
func matchKeyword(src string, i int, kw string) bool {
	if i+len(kw) > len(src) {
		return false
	}
	if src[i:i+len(kw)] != kw {
		return false
	}
	if i > 0 && isIdentChar(src[i-1]) {
		return false
	}
	if i+len(kw) < len(src) && isIdentChar(src[i+len(kw)]) {
		return false
	}
	return true
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '$'
}
