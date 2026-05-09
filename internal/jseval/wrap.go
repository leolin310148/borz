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
	if isStatementLike(trimmed) {
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

func isStatementLike(s string) bool {
	for _, prefix := range []string{
		"const ", "let ", "var ",
		"function ", "async function ", "class ",
		"if ", "if(", "for ", "for(", "while ", "while(",
		"switch ", "switch(", "try", "throw ", "return ",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// scanTopLevel walks the script ignoring strings, template literals, and
// comments. It reports whether an `await` appears at depth 0 or nested only
// inside parentheses/brackets, and whether any top-level `;` or
// statement-separating newline is present.
func scanTopLevel(src string) (hasAwait, hasTopSemi bool) {
	const (
		stCode = iota
		stLineComment
		stBlockComment
		stSingle
		stDouble
		stTemplate
		stRegex
	)
	state := stCode
	depth := 0
	braceDepth := 0
	regexClass := false
	var templateExprBase []int
	i := 0
	for i < len(src) {
		c := src[i]
		switch state {
		case stCode:
			switch {
			case c == '}' && len(templateExprBase) > 0 && depth == templateExprBase[len(templateExprBase)-1]:
				templateExprBase = templateExprBase[:len(templateExprBase)-1]
				state = stTemplate
				i++
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
			case c == '/' && slashStartsRegex(src, i):
				state = stRegex
				regexClass = false
				i++
			case c == '(' || c == '{' || c == '[':
				if c == '{' {
					braceDepth++
				}
				depth++
				i++
			case c == ')' || c == '}' || c == ']':
				if depth > 0 {
					depth--
				}
				if c == '}' && braceDepth > 0 {
					braceDepth--
				}
				i++
			case depth == 0 && c == ';':
				hasTopSemi = true
				i++
			case depth == 0 && c == '\n':
				if isStatementNewline(src, i) {
					hasTopSemi = true
				}
				i++
			case braceDepth == 0 && c == 'a' && matchKeyword(src, i, "await"):
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
			} else if c == '$' && i+1 < len(src) && src[i+1] == '{' {
				templateExprBase = append(templateExprBase, depth)
				state = stCode
				i += 2
			} else if c == '`' {
				state = stCode
				i++
			} else {
				i++
			}
		case stRegex:
			switch {
			case c == '\\' && i+1 < len(src):
				i += 2
			case c == '[':
				regexClass = true
				i++
			case c == ']' && regexClass:
				regexClass = false
				i++
			case c == '/' && !regexClass:
				state = stCode
				i++
				for i < len(src) && isIdentChar(src[i]) {
					i++
				}
			default:
				i++
			}
		}
	}
	return
}

func slashStartsRegex(src string, i int) bool {
	prev, ok := prevSignificantByte(src, i-1)
	if !ok {
		return true
	}
	if isIdentChar(prev) {
		switch previousIdentifier(src, i-1) {
		case "case", "delete", "in", "new", "of", "return", "throw", "typeof", "void", "yield":
			return true
		}
	}
	return strings.ContainsRune("([{=,:;!&|?+-*%^~<>", rune(prev))
}

func previousIdentifier(src string, i int) string {
	for i >= 0 && !isIdentChar(src[i]) {
		i--
	}
	end := i + 1
	for i >= 0 && isIdentChar(src[i]) {
		i--
	}
	return src[i+1 : end]
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
	if i > 0 && src[i-1] == '.' {
		return false
	}
	if i+len(kw) < len(src) && isIdentChar(src[i+len(kw)]) {
		return false
	}
	return true
}

func isStatementNewline(src string, i int) bool {
	prev, ok := prevSignificantByte(src, i-1)
	if !ok {
		return false
	}
	next, ok := nextSignificantByte(src, i+1)
	if !ok {
		return false
	}
	if strings.ContainsRune("+-*/%&|^!<>?:.,", rune(prev)) {
		return false
	}
	if strings.ContainsRune(".[+-*/%&|^!<>?:,)]}", rune(next)) {
		return false
	}
	return true
}

func prevSignificantByte(src string, i int) (byte, bool) {
	for i >= 0 {
		switch src[i] {
		case ' ', '\t', '\r', '\n':
			i--
		default:
			return src[i], true
		}
	}
	return 0, false
}

func nextSignificantByte(src string, i int) (byte, bool) {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return src[i], true
		}
	}
	return 0, false
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '$'
}
