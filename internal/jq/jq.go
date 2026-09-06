// Package jq evaluates jq filters without an external executable.
package jq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

func compile(expression string) (*gojq.Code, error) {
	if strings.TrimSpace(expression) == "" {
		return nil, fmt.Errorf("jq expression must not be empty")
	}
	query, err := gojq.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("invalid jq expression: %w", err)
	}
	// No filesystem module loader, environment loader, or input stream: a
	// filter can only access the supplied response.
	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("invalid jq expression: %w", err)
	}
	return code, nil
}

// Validate rejects invalid filters before a CLI command has side effects.
func Validate(expression string) error {
	_, err := compile(expression)
	return err
}

// Apply returns the complete result stream, or an error with no partial output.
func Apply(data interface{}, expression string) ([]interface{}, error) {
	code, err := compile(expression)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	iter := code.RunWithContext(ctx, data)
	results := []interface{}{}
	for {
		value, ok := iter.Next()
		if !ok {
			return results, nil
		}
		if _, ok := value.(error); ok {
			// gojq runtime errors can include the unfiltered input (e.g. a
			// signed URL). Do not echo that data through an error message.
			return nil, fmt.Errorf("jq evaluation failed; check the input shape and field paths")
		}
		if len(results) >= 100000 {
			return nil, fmt.Errorf("jq result limit exceeded; narrow the filter")
		}
		results = append(results, value)
	}
}
