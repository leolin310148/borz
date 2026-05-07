package jseval

import (
	"strings"
	"testing"
)

func TestParseJSONArgs_Valid(t *testing.T) {
	args, err := ParseJSONArgs([]string{
		`name="alice"`,
		`age=42`,
		`flags=[1,2,3]`,
		`opts={"x":1}`,
		`nilable=null`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 5 {
		t.Fatalf("expected 5 args, got %d", len(args))
	}
	if args[0].Name != "name" || args[0].RawValue != `"alice"` {
		t.Errorf("name arg mismatch: %+v", args[0])
	}
}

func TestParseJSONArgs_Errors(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"missing =", "foo", "expected name=value"},
		{"empty name", "=42", "expected name=value"},
		{"bad ident", "1foo=42", "not a valid identifier"},
		{"bad json", "x=oops", "not valid JSON"},
		{"reserved", "await=42", "not a valid identifier"},
		{"reserved default", "default=42", "not a valid identifier"},
		{"reserved in", "in=42", "not a valid identifier"},
		{"future reserved", "interface=42", "not a valid identifier"},
		{"hyphen", "my-name=42", "not a valid identifier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseJSONArgs([]string{tc.input})
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestParseJSONArgs_Duplicate(t *testing.T) {
	_, err := ParseJSONArgs([]string{"a=1", "a=2"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestPrefixJSONArgs(t *testing.T) {
	args := []JSONArg{
		{Name: "user", RawValue: `{"id":7}`},
		{Name: "n", RawValue: "42"},
	}
	got := PrefixJSONArgs(args)
	want := "const user = {\"id\":7};\nconst n = 42;\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestPrefixJSONArgs_Empty(t *testing.T) {
	if got := PrefixJSONArgs(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
