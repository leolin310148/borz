package jq

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	tests := []struct{ name, input, expression, want string }{
		{"identity", `{"a":1}`, `.`, `[{"a":1}]`},
		{"field", `{"name":"alice"}`, `.name`, `["alice"]`},
		{"quoted", `{"x-sap-ext-overview":[{"name":"a"},{"name":"b"}]}`, `.["x-sap-ext-overview"][] | select(.name == "b")`, `[{"name":"b"}]`},
		{"empty selection", `[{"status":200}]`, `.[] | select(.status >= 400)`, `[]`},
		{"collect selection", `[{"status":200},{"status":500}]`, `[.[] | select(.status >= 400)]`, `[[{"status":500}]]`},
		{"map", `[{"status":200},{"status":500}]`, `map(select(.status >= 400))`, `[[{"status":500}]]`},
		{"download projection", `[{"id":1,"startTime":"a","url":"secret"},{"id":2,"startTime":"b","url":"secret"}]`, `sort_by(.startTime) | reverse | .[0:1] | map({id,startTime})`, `[[{"id":2,"startTime":"b"}]]`},
		{"nested", `{"a":{"b":3}}`, `.a.b`, `[3]`},
		{"keys", `{"z":1,"a":2}`, `keys`, `[["a","z"]]`},
		{"length", `[1,2]`, `length`, `[2]`},
		{"negative index", `[1,2]`, `.[-1]`, `[2]`},
		{"slice", `[1,2,3]`, `.[1:]`, `[[2,3]]`},
		{"missing is null", `{}`, `.missing`, `[null]`},
		{"projection", `{"x":1}`, `{a:.x,b:null,c:false}`, `[{"a":1,"b":null,"c":false}]`},
		{"predicate", `[{"name":"abc"},{"name":"xyz"}]`, `.[] | select(.name | startswith("a")) | .name`, `["abc"]`},
		{"object iteration", `{"a":1,"b":2}`, `[.[]] | sort`, `[[1,2]]`},
		{"multiple results", `{}`, `1,2,3`, `[1,2,3]`},
		{"environment isolated", `{}`, `env`, `[{}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var input interface{}
			if err := json.Unmarshal([]byte(tt.input), &input); err != nil {
				t.Fatal(err)
			}
			got, err := Apply(input, tt.expression)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(got)
			if err != nil || string(raw) != tt.want {
				t.Fatalf("got %s, %v; want %s", raw, err, tt.want)
			}
		})
	}
}

func TestApplyRejectsInvalidFiltersWithoutReturningInput(t *testing.T) {
	for _, expression := range []string{`.["a"`, `map(`, `nonesuch`, `select()`, `.a |`, `import "secret" as s; s::x`} {
		t.Run(expression, func(t *testing.T) {
			if Validate(expression) == nil {
				t.Fatal("expected validation error")
			}
			got, err := Apply(map[string]interface{}{"secret": "private"}, expression)
			if err == nil || len(got) != 0 {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestApplyRuntimeErrorIsAtomicAndPrivate(t *testing.T) {
	got, err := Apply("private-token", `. , (. + 1)`)
	if err == nil || len(got) != 0 || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestApplyResultLimit(t *testing.T) {
	got, err := Apply(nil, `range(100001)`)
	if err == nil || len(got) != 0 {
		t.Fatalf("got %d results, %v", len(got), err)
	}
}
