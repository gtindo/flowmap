package javascript

import (
	"strings"
	"testing"

	"github.com/gtindo/flowmap/internal/semantic"
)

func TestMaskSourceSeparatesCodeFromText(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		source  string
		visible []string
		hidden  []string
	}{
		{
			name:    "apostrophe in JSX text",
			path:    "App.tsx",
			source:  "const A = () => <p>it's {label()}</p>;\nfunction after() { run(); }",
			visible: []string{"label()", "function after() { run(); }"},
			hidden:  []string{"it's"},
		},
		{
			name:    "URL in JSX text is not a comment",
			path:    "App.jsx",
			source:  "const A = () => <a href={url()}>http://example.com {go()}</a>;",
			visible: []string{"url()", "go()"},
			hidden:  []string{"example.com"},
		},
		{
			name:    "parenthesized JSX text is not a call",
			path:    "Form.tsx",
			source:  "const F = () => (\n  <label htmlFor=\"w\">Weight (kg)</label>\n);",
			visible: []string{"<label htmlFor="},
			hidden:  []string{"Weight (kg)", "\"w\""},
		},
		{
			name:    "attribute strings, expressions, fragments, and nested elements",
			path:    "List.tsx",
			source:  "const L = () => <><img alt=\"it's\" onClick={() => save()} /><List<Item> items={xs}><b>x</b></List></>;",
			visible: []string{"save()", "items={xs}"},
			hidden:  []string{"it's"},
		},
		{
			name:    "generic arrow parameters are not JSX",
			path:    "id.tsx",
			source:  "const id = <T,>(value: T): T => value;\nconst run = () => go();",
			visible: []string{"<T,>(value: T)", "go()"},
		},
		{
			name:    "type-level generic falls back to code",
			path:    "types.tsx",
			source:  "type Fn = <T>(value: T) => T;\nexport function after() { return go(); }",
			visible: []string{"export function after() { return go(); }"},
		},
		{
			name:    "TypeScript assertions are not JSX",
			path:    "cast.ts",
			source:  "const n = <number>value; const s = 'it''s';\nfunction after() { go(); }",
			visible: []string{"<number>value", "function after() { go(); }"},
		},
		{
			name:    "comparisons are not JSX",
			path:    "loop.jsx",
			source:  "for (let i = 0; i <n; i++) { if (a<b) step(); }",
			visible: []string{"i <n", "a<b", "step()"},
		},
		{
			name:    "regular expression containing quotes",
			path:    "re.js",
			source:  "const r = /^[!#'`|a-z]+$/i;\nfunction after() { go(); }",
			visible: []string{"function after() { go(); }"},
			hidden:  []string{"a-z"},
		},
		{
			name:    "division is not a regular expression",
			path:    "math.js",
			source:  "const half = total(x) / 2 / count(); i++ / 2; const r = (a) / b;",
			visible: []string{"total(x) / 2 / count()", "(a) / b"},
		},
		{
			name:    "template expressions stay visible",
			path:    "t.ts",
			source:  "const s = `a ${format(`b ${inner()}`)} c'`;\nfunction after() { go(); }",
			visible: []string{"format(", "inner()", "function after() { go(); }"},
			hidden:  []string{" c'"},
		},
		{
			name:    "unterminated string stops at the line break",
			path:    "broken.js",
			source:  "const s = 'unterminated;\nfunction after() { go(); }",
			visible: []string{"function after() { go(); }"},
			hidden:  []string{"unterminated"},
		},
		{
			name:    "comments, escapes, and hashbang",
			path:    "cli.js",
			source:  "#!/usr/bin/env node\nconst s = \"a\\\"{\"; // call()\n/* other() */ run();",
			visible: []string{"run();"},
			hidden:  []string{"node", "{", "call()", "other()"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			masked := maskSource(testCase.source, jsxCapable(testCase.path))

			if len(masked) != len(testCase.source) || strings.Count(masked, "\n") != strings.Count(testCase.source, "\n") {
				t.Fatalf("mask changed offsets: %q", masked)
			}
			for _, fragment := range testCase.visible {
				if !strings.Contains(masked, fragment) {
					t.Errorf("masked source hid %q: %q", fragment, masked)
				}
			}
			for _, fragment := range testCase.hidden {
				if strings.Contains(masked, fragment) {
					t.Errorf("masked source kept %q: %q", fragment, masked)
				}
			}
		})
	}
}

// Regression: JSX text was scanned as code, so an apostrophe opened a string
// that hid later calls and declarations, and `Text (note)` became a call.
func TestBackendReadsCallsAfterJSXText(t *testing.T) {
	root := t.TempDir()
	writeJavaScriptFixture(t, root, "Form.tsx", `export function Form() {
  return (
    <form onSubmit={() => submit()}>
      <label>Weight (kg)</label>
      <p>Don't forget: http://example.com</p>
      {render()}
    </form>
  );
}
function submit() {}
function render() {}
`)

	snapshot := analyzeWithDeadline(t, root)

	byName := symbolsByName(snapshot)
	assertRelationship(t, snapshot, byName, "Form", "submit", semantic.RelationshipCall, false)
	assertRelationship(t, snapshot, byName, "Form", "render", semantic.RelationshipCall, false)
	if form := byName["Form"]; form.Location.EndLine != 9 {
		t.Errorf("Form ends at line %d, want 9", form.Location.EndLine)
	}
	for _, fact := range byName["Form"].Facts {
		if fact.Name == "Weight" {
			t.Errorf("JSX text produced external call fact %q", fact.Name)
		}
	}
}

func BenchmarkMaskSource(b *testing.B) {
	source := strings.Repeat("export const Item = ({ label }: Props) => <li className=\"item\" onClick={() => pick(label)}>It's {label} (x)</li>;\nconst re = /'[a-z]+'/g; const s = `${format(label)}`;\n", 2000)
	b.SetBytes(int64(len(source)))
	for b.Loop() {
		_ = maskSource(source, true)
	}
}
