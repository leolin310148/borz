package jseval

import "testing"

func TestAutoWrapAwait(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no await is untouched",
			in:   "document.title",
			want: "document.title",
		},
		{
			name: "no await with semis is untouched",
			in:   "var x = 1; x + 2",
			want: "var x = 1; x + 2",
		},
		{
			name: "single-expression await is wrapped with return",
			in:   "await fetch('/x').then(r=>r.json())",
			want: "(async () => { return (await fetch('/x').then(r=>r.json())) })()",
		},
		{
			name: "leading whitespace before single-expression await keeps return",
			in:   "\n  await fetch('/x').then(r=>r.json())",
			want: "(async () => { return (await fetch('/x').then(r=>r.json())) })()",
		},
		{
			name: "multi-line awaited property chain keeps return",
			in:   "await fetch('/x')\n  .then(r=>r.json())",
			want: "(async () => { return (await fetch('/x')\n  .then(r=>r.json())) })()",
		},
		{
			name: "multi-line awaited binary expression keeps return",
			in:   "await foo() +\n  1",
			want: "(async () => { return (await foo() +\n  1) })()",
		},
		{
			name: "multi-statement await is wrapped without forced return",
			in:   "var x = await fetch('/x'); x.status",
			want: "(async () => { var x = await fetch('/x'); x.status })()",
		},
		{
			name: "single declaration await is wrapped without invalid return",
			in:   "const x = await fetch('/x')",
			want: "(async () => { const x = await fetch('/x') })()",
		},
		{
			name: "single let declaration await is wrapped without invalid return",
			in:   "let x = await foo()",
			want: "(async () => { let x = await foo() })()",
		},
		{
			name: "top-level for-await statement is wrapped without invalid return",
			in:   "for await (const item of stream) console.log(item)",
			want: "(async () => { for await (const item of stream) console.log(item) })()",
		},
		{
			name: "already async IIFE is not double-wrapped",
			in:   "(async () => { return await fetch('/x') })()",
			want: "(async () => { return await fetch('/x') })()",
		},
		{
			name: "async IIFE with trailing semicolon is not double-wrapped",
			in:   "(async () => { return await fetch('/x') })();",
			want: "(async () => { return await fetch('/x') })();",
		},
		{
			name: "await inside string literal is ignored",
			in:   "'await this'",
			want: "'await this'",
		},
		{
			name: "await inside line comment is ignored",
			in:   "1 + 2 // await something",
			want: "1 + 2 // await something",
		},
		{
			name: "await inside block comment is ignored",
			in:   "1 /* await x */ + 2",
			want: "1 /* await x */ + 2",
		},
		{
			name: "await inside function body (depth>0) does not trigger wrap",
			in:   "function foo(){ return await x }",
			want: "function foo(){ return await x }",
		},
		{
			name: "identifier ending in await is ignored",
			in:   "myawait + 1",
			want: "myawait + 1",
		},
		{
			name: "identifier starting with await is ignored",
			in:   "awaiter()",
			want: "awaiter()",
		},
		{
			name: "property named await is ignored",
			in:   "obj.await + 1",
			want: "obj.await + 1",
		},
		{
			name: "optional-chain property named await is ignored",
			in:   "obj?.await()",
			want: "obj?.await()",
		},
		{
			name: "trailing semicolon on single expression is preserved as no-op",
			in:   "await foo();",
			want: "(async () => { return (await foo()) })()",
		},
		{
			name: "newline counts as statement separator",
			in:   "var x = await foo()\nx + 1",
			want: "(async () => { var x = await foo()\nx + 1 })()",
		},
		{
			name: "empty script is untouched",
			in:   "",
			want: "",
		},
		{
			name: "whitespace-only script is untouched",
			in:   "   ",
			want: "   ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AutoWrapAwait(tc.in)
			if got != tc.want {
				t.Fatalf("\nin:   %q\ngot:  %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}
