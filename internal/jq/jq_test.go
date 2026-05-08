package jq

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func mustJSON(t *testing.T, s string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad json fixture: %v", err)
	}
	return v
}

func TestApply_Identity(t *testing.T) {
	data := mustJSON(t, `{"a":1}`)
	got := Apply(data, ".")
	if len(got) != 1 || !reflect.DeepEqual(got[0], data) {
		t.Errorf("Apply . = %v", got)
	}
}

func TestApply_FieldAccess(t *testing.T) {
	data := mustJSON(t, `{"name":"alice","age":30}`)
	got := Apply(data, ".name")
	if len(got) != 1 || got[0] != "alice" {
		t.Errorf("Apply .name = %v", got)
	}
}

func TestApply_NestedField(t *testing.T) {
	data := mustJSON(t, `{"user":{"name":"bob"}}`)
	got := Apply(data, ".user.name")
	if len(got) != 1 || got[0] != "bob" {
		t.Errorf("Apply .user.name = %v", got)
	}
}

func TestApply_BracketQuotedField(t *testing.T) {
	data := mustJSON(t, `{"content-type":"text/html","nested-key":{"child name":"ok"}}`)
	got := Apply(data, `.["content-type"]`)
	if len(got) != 1 || got[0] != "text/html" {
		t.Errorf(`Apply .["content-type"] = %v`, got)
	}
	got = Apply(data, `.["nested-key"]["child name"]`)
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf(`Apply .["nested-key"]["child name"] = %v`, got)
	}
}

func TestApply_DotQuotedField(t *testing.T) {
	data := mustJSON(t, `{"tab-id":"abc","quote\"key":7}`)
	got := Apply(data, `."tab-id"`)
	if len(got) != 1 || got[0] != "abc" {
		t.Errorf(`Apply ."tab-id" = %v`, got)
	}
	got = Apply(data, `."quote\"key"`)
	if len(got) != 1 || got[0] != float64(7) {
		t.Errorf(`Apply ."quote\"key" = %v`, got)
	}
}

func TestApply_InvalidQuotedFieldFallsBackToCurrent(t *testing.T) {
	data := mustJSON(t, `{"a":1}`)
	got := Apply(data, `.["a"`)
	if len(got) != 1 || !reflect.DeepEqual(got[0], data) {
		t.Errorf(`Apply .["a" = %v`, got)
	}
}

func TestApply_MissingField(t *testing.T) {
	data := mustJSON(t, `{"a":1}`)
	got := Apply(data, ".missing")
	if len(got) != 0 {
		t.Errorf("Apply .missing = %v, want empty", got)
	}
}

func TestApply_NullField(t *testing.T) {
	data := mustJSON(t, `{"a":null}`)
	got := Apply(data, ".a")
	if len(got) != 1 || got[0] != nil {
		t.Errorf("Apply .a = %v, want [nil]", got)
	}
}

func TestApply_ArraySpread(t *testing.T) {
	data := mustJSON(t, `[{"n":1},{"n":2},{"n":3}]`)
	got := Apply(data, ".[].n")
	want := []interface{}{float64(1), float64(2), float64(3)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply .[].n = %v, want %v", got, want)
	}
}

func TestApply_ArrayIndex(t *testing.T) {
	data := mustJSON(t, `[10,20,30]`)
	if got := Apply(data, ".[0]"); len(got) != 1 || got[0] != float64(10) {
		t.Errorf(".[0] = %v", got)
	}
	if got := Apply(data, ".[2]"); len(got) != 1 || got[0] != float64(30) {
		t.Errorf(".[2] = %v", got)
	}
}

func TestApply_ArrayNegativeIndex(t *testing.T) {
	data := mustJSON(t, `[10,20,30]`)
	got := Apply(data, ".[-1]")
	if len(got) != 1 || got[0] != float64(30) {
		t.Errorf(".[-1] = %v", got)
	}
}

func TestApply_ArrayIndexOutOfRange(t *testing.T) {
	data := mustJSON(t, `[10,20]`)
	got := Apply(data, ".[5]")
	if len(got) != 0 {
		t.Errorf(".[5] = %v, want empty", got)
	}
}

func TestApply_Pipe(t *testing.T) {
	data := mustJSON(t, `{"user":{"name":"alice"}}`)
	got := Apply(data, ". | .user | .name")
	if len(got) != 1 || got[0] != "alice" {
		t.Errorf("pipe chain = %v", got)
	}
}

func TestApply_SelectEquals(t *testing.T) {
	data := mustJSON(t, `[{"role":"admin"},{"role":"user"},{"role":"admin"}]`)
	got := Apply(data, `.[] | select(.role == "admin")`)
	if len(got) != 2 {
		t.Errorf("select admins = %v, want 2", got)
	}
}

func TestApply_SelectNotEquals(t *testing.T) {
	data := mustJSON(t, `[{"n":1},{"n":2},{"n":3}]`)
	got := Apply(data, `.[] | select(.n != 2)`)
	if len(got) != 2 {
		t.Errorf("select != 2 = %v, want 2", got)
	}
}

func TestApply_SelectNumeric(t *testing.T) {
	data := mustJSON(t, `[{"n":1},{"n":5},{"n":10}]`)
	cases := []struct {
		expr string
		want int
	}{
		{`.[] | select(.n > 3)`, 2},
		{`.[] | select(.n < 5)`, 1},
		{`.[] | select(.n >= 5)`, 2},
		{`.[] | select(.n <= 5)`, 2},
	}
	for _, c := range cases {
		got := Apply(data, c.expr)
		if len(got) != c.want {
			t.Errorf("%s = %v, want %d matches", c.expr, got, c.want)
		}
	}
}

func TestApply_SelectStringOrdering(t *testing.T) {
	data := mustJSON(t, `[{"name":"alpha"},{"name":"beta"},{"name":"gamma"}]`)
	got := Apply(data, `.[] | select(.name >= "beta")`)
	if len(got) != 2 {
		t.Fatalf("select names >= beta = %v, want two matches", got)
	}
	if got[0].(map[string]interface{})["name"] != "beta" || got[1].(map[string]interface{})["name"] != "gamma" {
		t.Fatalf("select names >= beta matched %v", got)
	}
}

func TestApply_SelectBoolean(t *testing.T) {
	data := mustJSON(t, `[{"ok":true},{"ok":false}]`)
	got := Apply(data, `.[] | select(.ok == true)`)
	if len(got) != 1 {
		t.Errorf("select ok==true = %v", got)
	}
}

func TestApply_SelectEqualityIsTypeAware(t *testing.T) {
	data := mustJSON(t, `[{"value":true},{"value":"true"},{"value":1},{"value":"1"}]`)
	got := Apply(data, `.[] | select(.value == "true")`)
	if len(got) != 1 {
		t.Fatalf("select string true = %v, want one match", got)
	}
	m := got[0].(map[string]interface{})
	if m["value"] != "true" {
		t.Fatalf("select string true matched %v", m)
	}

	got = Apply(data, `.[] | select(.value == 1)`)
	if len(got) != 1 {
		t.Fatalf("select numeric 1 = %v, want one match", got)
	}
	m = got[0].(map[string]interface{})
	if m["value"] != float64(1) {
		t.Fatalf("select numeric 1 matched %v", m)
	}
}

func TestApply_ObjectProjection(t *testing.T) {
	data := mustJSON(t, `[{"name":"a","age":1,"x":99},{"name":"b","age":2,"x":100}]`)
	got := Apply(data, `.[] | {name, age}`)
	if len(got) != 2 {
		t.Fatalf("projection len = %d", len(got))
	}
	m := got[0].(map[string]interface{})
	if m["name"] != "a" || m["age"] != float64(1) {
		t.Errorf("proj[0] = %v", m)
	}
	if _, has := m["x"]; has {
		t.Errorf("projection leaked field x: %v", m)
	}
}

func TestApply_ObjectProjectionPreservesNullAndOmitsMissing(t *testing.T) {
	data := mustJSON(t, `{"present":null}`)
	got := Apply(data, `{present, missing}`)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	m := got[0].(map[string]interface{})
	if _, ok := m["present"]; !ok || m["present"] != nil {
		t.Fatalf("present field = %v, want explicit nil", m)
	}
	if _, ok := m["missing"]; ok {
		t.Fatalf("missing field was included: %v", m)
	}
}

func TestApply_ObjectProjectionRename(t *testing.T) {
	data := mustJSON(t, `{"name":"alice","age":30}`)
	got := Apply(data, `{n: .name, years: .age}`)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	m := got[0].(map[string]interface{})
	if m["n"] != "alice" || m["years"] != float64(30) {
		t.Errorf("rename proj = %v", m)
	}
}

func TestApply_ObjectProjectionQuotedKeys(t *testing.T) {
	data := mustJSON(t, `{"id":"abc","content-type":"text/html","quote\"key":7}`)
	got := Apply(data, `{"tab-id": .id, "content-type": .["content-type"], "quote\"out": ."quote\"key"}`)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	m := got[0].(map[string]interface{})
	if m["tab-id"] != "abc" || m["content-type"] != "text/html" || m[`quote"out`] != float64(7) {
		t.Errorf("quoted projection = %v", m)
	}
	if _, has := m[`"tab-id"`]; has {
		t.Errorf("quoted key was not unquoted: %v", m)
	}
}

func TestApply_ObjectProjectionQuotedKeyWithColon(t *testing.T) {
	data := mustJSON(t, `{"id":"abc"}`)
	got := Apply(data, `{"meta:id": .id}`)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	m := got[0].(map[string]interface{})
	if m["meta:id"] != "abc" {
		t.Errorf("projection with colon key = %v", m)
	}
}

func TestApply_ObjectProjectionQuotedFieldShorthand(t *testing.T) {
	data := mustJSON(t, `{"tab-id":"abc","content-type":"text/html"}`)
	got := Apply(data, `{."tab-id", .["content-type"]}`)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	m := got[0].(map[string]interface{})
	if m["tab-id"] != "abc" || m["content-type"] != "text/html" {
		t.Errorf("quoted shorthand projection = %v", m)
	}
}

func TestApply_ObjectProjectionEmpty(t *testing.T) {
	data := mustJSON(t, `{"a":1}`)
	got := Apply(data, `{}`)
	if len(got) != 1 {
		t.Fatalf("empty proj len = %d", len(got))
	}
	if m := got[0].(map[string]interface{}); len(m) != 0 {
		t.Errorf("empty proj = %v", m)
	}
}

func TestApply_Keys(t *testing.T) {
	data := mustJSON(t, `{"b":2,"a":1,"c":3}`)
	got := Apply(data, "keys")
	if len(got) != 1 {
		t.Fatalf("keys len = %d", len(got))
	}
	keys := got[0].([]interface{})
	strs := make([]string, len(keys))
	for i, k := range keys {
		strs[i] = k.(string)
	}
	sort.Strings(strs)
	if !reflect.DeepEqual(strs, []string{"a", "b", "c"}) {
		t.Errorf("keys = %v", strs)
	}
}

func TestApply_KeysAreSorted(t *testing.T) {
	data := mustJSON(t, `{"z":1,"a":2,"m":3}`)
	got := Apply(data, "keys")
	if len(got) != 1 {
		t.Fatalf("keys len = %d", len(got))
	}
	if !reflect.DeepEqual(got[0], []interface{}{"a", "m", "z"}) {
		t.Fatalf("keys = %v, want sorted [a m z]", got[0])
	}
}

func TestApply_KeysForArray(t *testing.T) {
	data := mustJSON(t, `["a","b","c"]`)
	got := Apply(data, "keys")
	if len(got) != 1 {
		t.Fatalf("keys len = %d", len(got))
	}
	if !reflect.DeepEqual(got[0], []interface{}{float64(0), float64(1), float64(2)}) {
		t.Fatalf("keys = %v, want [0 1 2]", got[0])
	}
}

func TestApply_Length(t *testing.T) {
	cases := []struct {
		input string
		want  float64
	}{
		{`[1,2,3]`, 3},
		{`{"a":1,"b":2}`, 2},
		{`"hello"`, 5},
		{`"\u4f60\u597d"`, 2},
		{`42`, 0},
	}
	for _, c := range cases {
		got := Apply(mustJSON(t, c.input), "length")
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("length(%s) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestApply_UnknownExpressionReturnsInputs(t *testing.T) {
	data := mustJSON(t, `{"a":1}`)
	got := Apply(data, "not_a_keyword")
	if len(got) != 1 || !reflect.DeepEqual(got[0], data) {
		t.Errorf("unknown expr = %v", got)
	}
}

func TestApply_SelectInvalidSyntaxReturnsInputs(t *testing.T) {
	data := mustJSON(t, `[1,2,3]`)
	// No comparator — falls through unchanged
	got := Apply(data, `select(.x)`)
	if len(got) != 1 {
		t.Errorf("invalid select = %v", got)
	}
}

func TestApply_PipeWithObjectProjection(t *testing.T) {
	data := mustJSON(t, `[{"role":"admin","name":"a"},{"role":"user","name":"b"}]`)
	got := Apply(data, `.[] | select(.role == "admin") | {name}`)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	if m := got[0].(map[string]interface{}); m["name"] != "a" {
		t.Errorf("projection = %v", m)
	}
}

func TestApply_NullLiteralSelect(t *testing.T) {
	data := mustJSON(t, `[{"x":null},{"x":1}]`)
	got := Apply(data, `.[] | select(.x == null)`)
	if len(got) != 1 {
		t.Errorf("select null = %v", got)
	}
}

func TestParseLiteral(t *testing.T) {
	cases := []struct {
		in   string
		want interface{}
	}{
		{`"hi"`, "hi"},
		{`true`, true},
		{`false`, false},
		{`null`, nil},
		{`42`, float64(42)},
		{`3.14`, 3.14},
		{`bareword`, "bareword"},
		{`"\z"`, `"\z"`},
	}
	for _, c := range cases {
		if got := parseLiteral(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseLiteral(%q) = %v (%T), want %v (%T)", c.in, got, got, c.want, c.want)
		}
	}
}

func TestCompareValues(t *testing.T) {
	if !compareValues("a", "==", "a") {
		t.Error("eq strings")
	}
	if compareValues("a", "==", "b") {
		t.Error("ne strings should not eq")
	}
	if !compareValues("a", "!=", "b") {
		t.Error("!= strings")
	}
	if compareValues(true, "==", "true") {
		t.Error("bool true should not equal string true")
	}
	if compareValues(float64(1), "!=", 1) {
		t.Error("numeric equality should compare across numeric Go types")
	}
	if !compareValues(1, "!=", "1") {
		t.Error("numeric 1 should not equal string 1")
	}
	if !compareValues(float64(5), ">", float64(3)) {
		t.Error("5>3")
	}
	if !compareValues(float64(3), "<", float64(5)) {
		t.Error("3<5")
	}
	if !compareValues(float64(5), ">=", float64(5)) {
		t.Error("5>=5")
	}
	if !compareValues(float64(5), "<=", float64(5)) {
		t.Error("5<=5")
	}
	if !compareValues(int64(6), ">", uint(5)) {
		t.Error("ordered comparisons should support non-int numeric Go types")
	}
	if !compareValues(json.Number("7"), ">=", int32(7)) {
		t.Error("ordered comparisons should support json.Number")
	}
	if !compareValues("10", ">", float64(2)) {
		t.Error("ordered comparisons should support numeric strings")
	}
	if !compareValues("beta", ">=", "alpha") {
		t.Error("ordered comparisons should support strings")
	}
	if compareValues("alpha", ">=", "beta") {
		t.Error("string ordering should not coerce both sides to zero")
	}
	if compareValues("abc", "<=", float64(1)) {
		t.Error("nonnumeric strings should not be ordered against numbers")
	}
	if compareValues("x", "??", "y") {
		t.Error("unknown op should be false")
	}
}

func TestToFloat(t *testing.T) {
	if toFloat(float64(1.5)) != 1.5 {
		t.Error("float64")
	}
	if toFloat(7) != 7 {
		t.Error("int")
	}
	if toFloat("2.5") != 2.5 {
		t.Error("string")
	}
	if toFloat(struct{}{}) != 0 {
		t.Error("unknown → 0")
	}
}

func TestSplitTopLevel_RespectsNesting(t *testing.T) {
	parts := splitTopLevel(`.a | select(.n > 1) | {x: .a, y: .b}`, "|")
	if len(parts) != 3 {
		t.Fatalf("parts = %v", parts)
	}
	if parts[1] != "select(.n > 1)" {
		t.Errorf("part[1] = %q", parts[1])
	}
}

func TestSplitTopLevel_RespectsStrings(t *testing.T) {
	parts := splitTopLevel(`"a|b" | .x`, "|")
	if len(parts) != 2 {
		t.Fatalf("parts = %v", parts)
	}
	if parts[0] != `"a|b"` {
		t.Errorf("part[0] = %q", parts[0])
	}
}

func TestSplitTopLevel_HandlesStringEndingInEscapedBackslash(t *testing.T) {
	parts := splitTopLevel(`"C:\\" | .x`, "|")
	if !reflect.DeepEqual(parts, []string{`"C:\\"`, ".x"}) {
		t.Fatalf("parts = %v", parts)
	}
}

func TestSplitTopLevel_HandlesCommasAfterStringEndingInEscapedBackslash(t *testing.T) {
	parts := splitTopLevel(`path: "C:\\", name: .name`, ",")
	if !reflect.DeepEqual(parts, []string{`path: "C:\\"`, "name: .name"}) {
		t.Fatalf("parts = %v", parts)
	}
}

func TestIndexTopLevel(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`key: .value`, 3},
		{`"meta:id": .id`, len(`"meta:id"`)},
		{`outer: {inner: .value}`, 5},
		{`no_colon`, -1},
	}
	for _, c := range cases {
		if got := indexTopLevel(c.in, ':'); got != c.want {
			t.Errorf("indexTopLevel(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestGetField_NonMap(t *testing.T) {
	if got := getField("string", "x"); !isMissing(got) {
		t.Errorf("getField on string = %v", got)
	}
}
