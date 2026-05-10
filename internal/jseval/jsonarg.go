package jseval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSONArg is a CLI-supplied (name, value) pair that should be exposed to the
// evaluated script as a top-level `const`.
type JSONArg struct {
	Name string
	// RawValue is the unparsed RHS as supplied by the user.
	RawValue string
}

// ParseJSONArgs takes a list of `name=value` strings and returns the parsed
// args. The value must be valid JSON; it is parsed eagerly so a malformed
// argument fails fast on the CLI side instead of at Runtime.evaluate time.
func ParseJSONArgs(specs []string) ([]JSONArg, error) {
	out := make([]JSONArg, 0, len(specs))
	seen := make(map[string]bool, len(specs))
	for _, raw := range specs {
		eq := strings.IndexByte(raw, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("--json-arg %q: expected name=value", raw)
		}
		name := strings.TrimSpace(raw[:eq])
		value := strings.TrimSpace(raw[eq+1:])
		if !isValidIdent(name) {
			if isReservedWord(name) {
				return nil, fmt.Errorf("--json-arg %q: %q cannot be used as a JSON arg name", raw, name)
			}
			return nil, fmt.Errorf("--json-arg %q: %q is not a valid identifier", raw, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("--json-arg %q: duplicate name", raw)
		}
		var probe interface{}
		if err := json.Unmarshal([]byte(value), &probe); err != nil {
			return nil, fmt.Errorf("--json-arg %s: value is not valid JSON (%v)", name, err)
		}
		seen[name] = true
		out = append(out, JSONArg{Name: name, RawValue: value})
	}
	return out, nil
}

// PrefixJSONArgs builds the `const NAME = <json>;` lines that should be
// prepended to the evaluated script. Returns an empty string for no args.
func PrefixJSONArgs(args []JSONArg) string {
	if len(args) == 0 {
		return ""
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteString("const ")
		b.WriteString(a.Name)
		b.WriteString(" = ")
		b.WriteString(a.RawValue)
		b.WriteString(";\n")
	}
	return b.String()
}

func isValidIdent(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if i == 0 {
			if !(c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
			continue
		}
		if !isIdentChar(c) {
			return false
		}
	}
	return !isReservedWord(name)
}

// isReservedWord rejects a small set of obviously-bad names. We do not aim
// for a full ECMAScript reserved-word table — Runtime.evaluate would reject
// the generated `const await = ...` itself — but flagging the common ones
// up front gives a clearer error message.
func isReservedWord(name string) bool {
	switch name {
	case "true", "false", "null", "undefined",
		"break", "case", "catch", "class", "const", "continue",
		"debugger", "default", "delete", "do", "else", "export",
		"extends", "finally", "for", "function", "if", "import",
		"in", "instanceof", "new", "return", "super", "switch",
		"this", "throw", "try", "typeof", "var", "void", "while",
		"with", "let", "static", "yield", "await", "enum",
		"implements", "interface", "package", "private", "protected",
		"public", "async", "from":
		return true
	}
	return false
}
