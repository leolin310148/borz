// Package jq implements a mini jq-compatible expression filter.
package jq

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	selectExprRegexp = regexp.MustCompile(`^select\((.+?)\s*(==|!=|>=|<=|>|<)\s*(.+)\)$`)
	arrayIndexRegexp = regexp.MustCompile(`^\[(-?\d+)\]`)
	fieldNameRegexp  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)`)
)

type missingValue struct{}

var missing = missingValue{}

// Apply applies a jq expression to data and returns matching results.
func Apply(data interface{}, expression string) []interface{} {
	results := applyExpression([]interface{}{data}, expression)
	var filtered []interface{}
	for _, r := range results {
		if !isMissing(r) {
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
					if value, ok := parseJSONLiteral(valueExpr); ok {
						obj[key] = value
						continue
					}
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
			switch v := item.(type) {
			case map[string]interface{}:
				names := make([]string, 0, len(v))
				for k := range v {
					names = append(names, k)
				}
				sort.Strings(names)

				keys := make([]interface{}, len(names))
				for i, k := range names {
					keys[i] = k
				}
				results = append(results, keys)
			case []interface{}:
				keys := make([]interface{}, len(v))
				for i := range v {
					keys[i] = float64(i)
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
				results = append(results, float64(utf8.RuneCountInString(v)))
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
		value := getField(item, field)
		if !isMissing(value) {
			results = append(results, value)
		}
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
		if v, ok := m[field]; ok {
			return v
		}
	}
	return missing
}

func isMissing(value interface{}) bool {
	_, ok := value.(missingValue)
	return ok
}

func parseLiteral(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if parsed, ok := parseJSONLiteral(trimmed); ok {
		return parsed
	}
	if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return n
	}
	return trimmed
}

func parseJSONLiteral(value string) (interface{}, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, ".") {
		return nil, false
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

func compareValues(left interface{}, op string, right interface{}) bool {
	switch op {
	case "==":
		return equalValues(left, right)
	case "!=":
		return !equalValues(left, right)
	case ">":
		cmp, ok := compareOrderedValues(left, right)
		return ok && cmp > 0
	case "<":
		cmp, ok := compareOrderedValues(left, right)
		return ok && cmp < 0
	case ">=":
		cmp, ok := compareOrderedValues(left, right)
		return ok && cmp >= 0
	case "<=":
		cmp, ok := compareOrderedValues(left, right)
		return ok && cmp <= 0
	}
	return false
}

func equalValues(left interface{}, right interface{}) bool {
	if leftNum, ok := numberValue(left); ok {
		if rightNum, ok := numberValue(right); ok {
			return leftNum == rightNum
		}
	}
	return reflect.DeepEqual(left, right)
}

func numberValue(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func compareOrderedValues(left interface{}, right interface{}) (int, bool) {
	if leftNum, leftOK := numericComparableValue(left); leftOK {
		if rightNum, rightOK := numericComparableValue(right); rightOK {
			return compareFloat(leftNum, rightNum), true
		}
	}
	leftString, leftIsString := left.(string)
	rightString, rightIsString := right.(string)
	if leftIsString && rightIsString {
		return strings.Compare(leftString, rightString), true
	}
	return 0, false
}

func numericComparableValue(v interface{}) (float64, bool) {
	if n, ok := numberValue(v); ok {
		return n, true
	}
	if s, ok := v.(string); ok {
		n, err := strconv.ParseFloat(s, 64)
		return n, err == nil
	}
	return 0, false
}

func compareFloat(left float64, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func toFloat(v interface{}) float64 {
	if n, ok := numberValue(v); ok {
		return n
	}
	switch n := v.(type) {
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
