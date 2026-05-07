// Package jq implements a mini jq-compatible expression filter.
package jq

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	selectExprRegexp = regexp.MustCompile(`^select\((.+?)\s*(==|!=|>=|<=|>|<)\s*(.+)\)$`)
	arrayIndexRegexp = regexp.MustCompile(`^\[(-?\d+)\]`)
	fieldNameRegexp  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)`)
)

// Apply applies a jq expression to data and returns matching results.
func Apply(data interface{}, expression string) []interface{} {
	results := applyExpression([]interface{}{data}, expression)
	var filtered []interface{}
	for _, r := range results {
		if r != nil {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func applyExpression(inputs []interface{}, expression string) []interface{} {
	segments := splitTopLevel(strings.TrimSpace(expression), "|")
	current := inputs
	for _, segment := range segments {
		current = applySegment(current, strings.TrimSpace(segment))
	}
	return current
}

func applySegment(inputs []interface{}, expr string) []interface{} {
	if expr == "." {
		return inputs
	}

	// select(...)
	if strings.HasPrefix(expr, "select(") {
		matches := selectExprRegexp.FindStringSubmatch(expr)
		if matches == nil {
			return inputs
		}
		leftExpr := matches[1]
		op := matches[2]
		rightExpr := matches[3]
		expected := parseLiteral(rightExpr)

		var results []interface{}
		for _, item := range inputs {
			leftVals := applyExpression([]interface{}{item}, leftExpr)
			if len(leftVals) == 0 {
				continue
			}
			left := leftVals[0]
			if compareValues(left, op, expected) {
				results = append(results, item)
			}
		}
		return results
	}

	// Object projection { ... }
	if strings.HasPrefix(expr, "{") && strings.HasSuffix(expr, "}") {
		body := strings.TrimSpace(expr[1 : len(expr)-1])
		if body == "" {
			results := make([]interface{}, len(inputs))
			for i := range results {
				results[i] = map[string]interface{}{}
			}
			return results
		}

		entries := splitTopLevel(body, ",")
		var results []interface{}
		for _, item := range inputs {
			obj := map[string]interface{}{}
			for _, entry := range entries {
				entry = strings.TrimSpace(entry)
				colonIdx := indexTopLevel(entry, ':')
				if colonIdx == -1 {
					key := projectionKeyFromExpression(entry)
					vals := applyExpression([]interface{}{item}, ensureFieldExpression(entry))
					if len(vals) > 0 {
						obj[key] = vals[0]
					}
				} else {
					key := projectionKey(entry[:colonIdx])
					valueExpr := strings.TrimSpace(entry[colonIdx+1:])
					vals := applyExpression([]interface{}{item}, valueExpr)
					if len(vals) > 0 {
						obj[key] = vals[0]
					}
				}
			}
			results = append(results, obj)
		}
		return results
	}

	// keys
	if expr == "keys" {
		var results []interface{}
		for _, item := range inputs {
			if m, ok := item.(map[string]interface{}); ok {
				names := make([]string, 0, len(m))
				for k := range m {
					names = append(names, k)
				}
				sort.Strings(names)

				keys := make([]interface{}, len(names))
				for i, k := range names {
					keys[i] = k
				}
				results = append(results, keys)
			}
		}
		return results
	}

	// length
	if expr == "length" {
		var results []interface{}
		for _, item := range inputs {
			switch v := item.(type) {
			case []interface{}:
				results = append(results, float64(len(v)))
			case map[string]interface{}:
				results = append(results, float64(len(v)))
			case string:
				results = append(results, float64(len(v)))
			default:
				results = append(results, float64(0))
			}
		}
		return results
	}

	// Path expression starting with .
	if !strings.HasPrefix(expr, ".") {
		return inputs
	}

	current := inputs
	remaining := expr[1:]

	for len(remaining) > 0 {
		if strings.HasPrefix(remaining, "[]") {
			// Array spread
			var spread []interface{}
			for _, item := range current {
				if arr, ok := item.([]interface{}); ok {
					spread = append(spread, arr...)
				}
			}
			current = spread
			remaining = remaining[2:]
		} else if strings.HasPrefix(remaining, `["`) {
			field, consumed, ok := parseBracketField(remaining)
			if !ok {
				break
			}
			current = applyField(current, field)
			remaining = remaining[consumed:]
		} else if strings.HasPrefix(remaining, "[") {
			// Array index
			matches := arrayIndexRegexp.FindStringSubmatch(remaining)
			if matches == nil {
				break
			}
			idx, _ := strconv.Atoi(matches[1])
			var results []interface{}
			for _, item := range current {
				if arr, ok := item.([]interface{}); ok {
					actualIdx := idx
					if actualIdx < 0 {
						actualIdx = len(arr) + actualIdx
					}
					if actualIdx >= 0 && actualIdx < len(arr) {
						results = append(results, arr[actualIdx])
					}
				}
			}
			current = results
			remaining = remaining[len(matches[0]):]
		} else if strings.HasPrefix(remaining, ".") {
			remaining = remaining[1:]
		} else if strings.HasPrefix(remaining, `"`) {
			field, consumed, ok := parseQuotedField(remaining)
			if !ok {
				break
			}
			current = applyField(current, field)
			remaining = remaining[consumed:]
		} else {
			// Field access
			matches := fieldNameRegexp.FindStringSubmatch(remaining)
			if matches == nil {
				break
			}
			field := matches[1]
			current = applyField(current, field)
			remaining = remaining[len(field):]
		}
	}

	return current
}

func applyField(inputs []interface{}, field string) []interface{} {
	var results []interface{}
	for _, item := range inputs {
		results = append(results, getField(item, field))
	}
	return results
}

func ensureFieldExpression(expr string) string {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, ".") {
		return expr
	}
	return "." + expr
}

func projectionKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if field, consumed, ok := parseQuotedField(raw); ok && consumed == len(raw) {
		return field
	}
	return raw
}

func projectionKeyFromExpression(expr string) string {
	key := strings.TrimPrefix(strings.TrimSpace(expr), ".")
	if field, consumed, ok := parseQuotedField(key); ok && consumed == len(key) {
		return field
	}
	if field, consumed, ok := parseBracketField(key); ok && consumed == len(key) {
		return field
	}
	return key
}

func getField(value interface{}, field string) interface{} {
	if m, ok := value.(map[string]interface{}); ok {
		return m[field]
	}
	return nil
}

func parseLiteral(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`) {
		var s string
		json.Unmarshal([]byte(trimmed), &s)
		return s
	}
	if trimmed == "true" {
		return true
	}
	if trimmed == "false" {
		return false
	}
	if trimmed == "null" {
		return nil
	}
	if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return n
	}
	return trimmed
}

func compareValues(left interface{}, op string, right interface{}) bool {
	switch op {
	case "==":
		return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right)
	case "!=":
		return fmt.Sprintf("%v", left) != fmt.Sprintf("%v", right)
	case ">":
		return toFloat(left) > toFloat(right)
	case "<":
		return toFloat(left) < toFloat(right)
	case ">=":
		return toFloat(left) >= toFloat(right)
	case "<=":
		return toFloat(left) <= toFloat(right)
	}
	return false
}

func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

func parseBracketField(input string) (string, int, bool) {
	if !strings.HasPrefix(input, `["`) {
		return "", 0, false
	}
	field, consumed, ok := parseQuotedField(input[1:])
	if !ok {
		return "", 0, false
	}
	consumed++
	if consumed >= len(input) || input[consumed] != ']' {
		return "", 0, false
	}
	return field, consumed + 1, true
}

func parseQuotedField(input string) (string, int, bool) {
	if !strings.HasPrefix(input, `"`) {
		return "", 0, false
	}
	escaped := false
	for i := 1; i < len(input); i++ {
		ch := input[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch != '"' {
			continue
		}
		var field string
		if err := json.Unmarshal([]byte(input[:i+1]), &field); err != nil {
			return "", 0, false
		}
		return field, i + 1, true
	}
	return "", 0, false
}

func indexTopLevel(input string, separator rune) int {
	depth := 0
	inString := false
	escapeNext := false

	for i, ch := range input {
		if inString {
			if escapeNext {
				escapeNext = false
				continue
			}
			if ch == '\\' {
				escapeNext = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			depth--
		default:
			if depth == 0 && ch == separator {
				return i
			}
		}
	}
	return -1
}

func splitTopLevel(input, separator string) []string {
	var parts []string
	var current strings.Builder
	depth := 0
	inString := false
	escapeNext := false

	for _, ch := range input {
		if inString {
			current.WriteRune(ch)
			if escapeNext {
				escapeNext = false
				continue
			}
			if ch == '\\' {
				escapeNext = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		if ch == '"' {
			inString = true
			current.WriteRune(ch)
			continue
		}
		if ch == '{' || ch == '(' || ch == '[' {
			depth++
		}
		if ch == '}' || ch == ')' || ch == ']' {
			depth--
		}
		if depth == 0 && len(separator) == 1 && ch == rune(separator[0]) {
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(ch)
	}

	if s := strings.TrimSpace(current.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}
