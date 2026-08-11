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
			name: "await inside call arguments is wrapped with return",
			in:   "JSON.stringify(await foo())",
			want: "(async () => { return (JSON.stringify(await foo())) })()",
		},
		{
			name: "await inside array literal is wrapped with return",
			in:   "Promise.all([await first(), await second()])",
			want: "(async () => { return (Promise.all([await first(), await second()])) })()",
		},
		{
			name: "await inside object literal is wrapped with return",
			in:   "JSON.stringify({ok: await foo()})",
			want: "(async () => { return (JSON.stringify({ok: await foo()})) })()",
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
			name: "await inside top-level for block is wrapped",
			in:   "for (const item of items) { results.push(await load(item)) }",
			want: "(async () => { for (const item of items) { results.push(await load(item)) } })()",
		},
		{
			name: "await inside top-level while block is wrapped",
			in:   "while (ready()) { await next() }",
			want: "(async () => { while (ready()) { await next() } })()",
		},
		{
			name: "await inside top-level if block is wrapped",
			in:   "if (enabled) { await run() }",
			want: "(async () => { if (enabled) { await run() } })()",
		},
		{
			name: "await inside top-level try block is wrapped",
			in:   "try { await run() } catch (error) { report(error) }",
			want: "(async () => { try { await run() } catch (error) { report(error) } })()",
		},
		{
			name: "await inside arrow function body is ignored",
			in:   "const load = async () => { await fetch('/x') }",
			want: "const load = async () => { await fetch('/x') }",
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
			name: "await inside object method body is ignored",
			in:   "({ async value(){ return await foo() } })",
			want: "({ async value(){ return await foo() } })",
		},
		{
			name: "await inside class method body is ignored",
			in:   "class Example { async value(){ return await foo() } }",
			want: "class Example { async value(){ return await foo() } }",
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
			name: "await inside top-level regex literal is ignored",
			in:   "/await/.test(text)",
			want: "/await/.test(text)",
		},
		{
			name: "await inside template text is ignored",
			in:   "`await text`",
			want: "`await text`",
		},
		{
			name: "await inside template substitution is wrapped with return",
			in:   "`value ${await foo()}`",
			want: "(async () => { return (`value ${await foo()}`) })()",
		},
		{
			name: "await inside nested template substitution is wrapped with return",
			in:   "`${`value ${await foo()}`}`",
			want: "(async () => { return (`${`value ${await foo()}`}`) })()",
		},
		{
			name: "await inside assigned regex literal is ignored",
			in:   "const re = /aw[ai\\/]t/g",
			want: "const re = /aw[ai\\/]t/g",
		},
		{
			name: "await inside regex literal after return is ignored",
			in:   "return /await/.test(text)",
			want: "return /await/.test(text)",
		},
		{
			name: "division before await is still detected",
			in:   "total / await divisor()",
			want: "(async () => { return (total / await divisor()) })()",
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

func TestPrepareCLI(t *testing.T) {
	tests := []struct {
		name      string
		script    string
		prefix    string
		autoAwait bool
		want      string
	}{
		{
			name:      "plain expression keeps completion semantics",
			script:    "document.title",
			autoAwait: true,
			want:      "document.title",
		},
		{
			name:      "lexical declaration gets per-call block scope",
			script:    "const f = 1; f",
			autoAwait: true,
			want:      "{\nconst f = 1; f\n}",
		},
		{
			name:      "json args share the invocation scope",
			script:    "user.id",
			prefix:    "const user = {\"id\":7};\n",
			autoAwait: true,
			want:      "{\nconst user = {\"id\":7};\nuser.id\n}",
		},
		{
			name:      "json args stay inside async wrapper",
			script:    "await load(user.id)",
			prefix:    "const user = {\"id\":7};\n",
			autoAwait: true,
			want:      "(async () => { const user = {\"id\":7};\nreturn (await load(user.id)) })()",
		},
		{
			name:      "top-level return gets a function scope",
			script:    "const value = 7; return value",
			autoAwait: true,
			want:      "(() => { const value = 7; return value })()",
		},
		{
			name:      "no auto await preserves raw await expression",
			script:    "await load()",
			autoAwait: false,
			want:      "await load()",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PrepareCLI(tc.script, tc.prefix, tc.autoAwait); got != tc.want {
				t.Fatalf("PrepareCLI() = %q, want %q", got, tc.want)
			}
		})
	}
}
