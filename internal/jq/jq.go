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
		re := regexp.MustCompile(`^select\((.+?)\s*(==|!=|>=|<=|>|<)\s*(.+)\)$`)
		matches := re.FindStringSubmatch(expr)
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
				colonIdx := strings.Index(entry, ":")
				if colonIdx == -1 {
					key := strings.TrimPrefix(strings.TrimSpace(entry), ".")
					vals := applyExpression([]interface{}{item}, "."+key)
					if len(vals) > 0 {
						obj[key] = vals[0]
					}
				} else {
					key := strings.TrimSpace(entry[:colonIdx])
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
		} else if strings.HasPrefix(remaining, "[") {
			// Array index
			re := regexp.MustCompile(`^\[(-?\d+)\]`)
			matches := re.FindStringSubmatch(remaining)
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
		} else {
			// Field access
			re := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)`)
			matches := re.FindStringSubmatch(remaining)
			if matches == nil {
				break
			}
			field := matches[1]
			var results []interface{}
			for _, item := range current {
				results = append(results, getField(item, field))
			}
			current = results
			remaining = remaining[len(field):]
		}
	}

	return current
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

func splitTopLevel(input, separator string) []string {
	var parts []string
	var current strings.Builder
	depth := 0
	inString := false
	prevChar := rune(0)

	for _, ch := range input {
		if ch == '"' && prevChar != '\\' {
			inString = !inString
		}
		if !inString {
			if ch == '{' || ch == '(' || ch == '[' {
				depth++
			}
			if ch == '}' || ch == ')' || ch == ']' {
				depth--
			}
			if depth == 0 {
				if len(separator) == 1 && ch == rune(separator[0]) {
					parts = append(parts, strings.TrimSpace(current.String()))
					current.Reset()
					prevChar = ch
					continue
				}
			}
		}
		current.WriteRune(ch)
		prevChar = ch
	}

	if s := strings.TrimSpace(current.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}
