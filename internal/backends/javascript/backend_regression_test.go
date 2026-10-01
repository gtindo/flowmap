package javascript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gtindo/flowmap/internal/semantic"
)

const analyzeDeadline = 10 * time.Second

func analyzeWithDeadline(t *testing.T, root string) semantic.Snapshot {
	t.Helper()

	type result struct {
		snapshot semantic.Snapshot
		err      error
	}
	done := make(chan result, 1)
	go func() {
		snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root, Language: "javascript"})
		done <- result{snapshot, err}
	}()

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("Analyze() error = %v", outcome.err)
		}
		return outcome.snapshot
	case <-time.After(analyzeDeadline):
		t.Fatalf("Analyze() did not finish within %v", analyzeDeadline)
		return semantic.Snapshot{}
	}
}

// Regression: JSX text children made the former validation parser loop forever.
func TestBackendAnalyzesJSXTextWithoutHanging(t *testing.T) {
	root := t.TempDir()
	writeJavaScriptFixture(t, root, "App.tsx", `export function App() {
  return <p>Hello &amp; welcome, it's a > b {label()}</p>;
}
const label = () => "x";
`)

	snapshot := analyzeWithDeadline(t, root)

	byName := symbolsByName(snapshot)
	assertRelationship(t, snapshot, byName, "App", "label", semantic.RelationshipCall, false)
}

// Regression: an empty block comment before a declaration panicked jsDoc.
func TestBackendToleratesEmptyBlockComment(t *testing.T) {
	root := t.TempDir()
	writeJavaScriptFixture(t, root, "empty.js", "/**/\nfunction afterEmpty() {}\n")

	snapshot := analyzeWithDeadline(t, root)

	if byName := symbolsByName(snapshot); byName["afterEmpty"] == nil {
		t.Fatalf("symbols = %v", sortedSymbolNames(snapshot))
	}
}

func TestBackendSkipsIgnoredGeneratedAndMinifiedSources(t *testing.T) {
	root := t.TempDir()
	writeJavaScriptFixture(t, root, "app.ts", "export function kept() {}\n")
	writeJavaScriptFixture(t, root, ".pnp.cjs", "function pnpRuntime() {}\n")
	writeNestedFixture(t, root, ".yarn/releases/yarn-4.cjs", "function yarnRuntime() {}\n")
	writeNestedFixture(t, root, ".turbo/cache.js", "function turboCache() {}\n")
	writeNestedFixture(t, root, "packages/web/storybook-static/main.js", "function storybookBundle() {}\n")
	writeJavaScriptFixture(t, root, "bundle.js", strings.Repeat("function minified(){return 1};", minifiedMinimumBytes/20))
	writeJavaScriptFixture(t, root, "huge.js", strings.Repeat("function huge() {}\n", maxSourceBytes/16))

	snapshot := analyzeWithDeadline(t, root)

	if names := sortedSymbolNames(snapshot); len(names) != 1 || names[0] != "kept" {
		t.Fatalf("symbols = %v, want only kept", names)
	}
	skipped := map[string][]string{}
	for _, diagnostic := range snapshot.Diagnostics.Diagnostics {
		if diagnostic.Kind == skippedDiagnosticKind {
			skipped[diagnostic.Message] = diagnostic.Units
		}
	}
	if units := skipped["skipped 1 minified file(s)"]; len(units) != 1 || units[0] != "bundle.js" {
		t.Errorf("minified diagnostic = %v", skipped)
	}
	if units := skipped["skipped 1 oversized file(s)"]; len(units) != 1 || units[0] != "huge.js" {
		t.Errorf("oversized diagnostic = %v", skipped)
	}
	if snapshot.Diagnostics.FailedUnits != 0 || snapshot.Diagnostics.TotalUnits != 1 {
		t.Errorf("diagnostics = %#v", snapshot.Diagnostics)
	}
}

func TestBackendHonorsGitIgnoreRules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	writeJavaScriptFixture(t, root, ".gitignore", "generated/\n")
	writeJavaScriptFixture(t, root, "tracked.ts", "export function tracked() {}\n")
	writeNestedFixture(t, root, "src/untracked.ts", "export function untracked() {}\n")
	writeNestedFixture(t, root, "generated/client.ts", "export function generatedClient() {}\n")
	runGit(t, root, "add", "tracked.ts", ".gitignore")

	snapshot := analyzeWithDeadline(t, root)

	names := strings.Join(sortedSymbolNames(snapshot), ",")
	if names != "tracked,untracked" {
		t.Fatalf("symbols = %s, want tracked,untracked", names)
	}
}

func TestBackendResolvesAmbiguousModulesDeterministically(t *testing.T) {
	root := t.TempDir()
	writeJavaScriptFixture(t, root, "util.ts", "export function helper() {}\n")
	writeJavaScriptFixture(t, root, "util.js", "export function helper() {}\n")
	writeJavaScriptFixture(t, root, "app.ts", "import { helper } from './util';\nexport function run() { helper(); }\n")

	for attempt := 0; attempt < 5; attempt++ {
		snapshot := analyzeWithDeadline(t, root)

		var targetFile string
		for _, relationship := range snapshot.Relationships {
			targetFile = symbolFileByID(snapshot, relationship.ToID)
		}
		if filepath.Base(targetFile) != "util.ts" {
			t.Fatalf("attempt %d resolved helper to %q, want util.ts", attempt, targetFile)
		}
	}
}

func TestBackendStopsWhenContextIsCancelled(t *testing.T) {
	root := t.TempDir()
	writeJavaScriptFixture(t, root, "app.ts", "export function run() {}\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (Backend{}).Analyze(ctx, semantic.AnalysisRequest{Root: root, Language: "javascript"})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze() error = %v, want context.Canceled", err)
	}
}

func TestIsSimpleType(t *testing.T) {
	cases := map[string]bool{"": false, "User": true, "_x$9": true, "9x": false, "User[]": false, "A|B": false, "Map<K>": false}
	for value, want := range cases {
		if got := isSimpleType(value); got != want {
			t.Errorf("isSimpleType(%q) = %t, want %t", value, got, want)
		}
	}
}

// BenchmarkAnalyzeLargeFile guards against per-symbol rescans of the whole file.
func BenchmarkAnalyzeLargeFile(b *testing.B) {
	root := b.TempDir()
	var source strings.Builder
	for index := 0; index < 4000; index++ {
		fmt.Fprintf(&source, "/** doc %d */\nexport const handler%d = (value: number) => {\n  return format(helper%d(value));\n};\n\nexport function helper%d(input: number): number {\n  return input * 2;\n}\n\n", index, index, index, index)
	}
	if err := os.WriteFile(filepath.Join(root, "large.ts"), []byte(source.String()), 0o600); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root, Language: "javascript"}); err != nil {
			b.Fatal(err)
		}
	}
}

func symbolFileByID(snapshot semantic.Snapshot, id string) string {
	for _, symbol := range snapshot.Symbols {
		if symbol.ID == id {
			return symbol.Location.File
		}
	}
	return ""
}

func writeNestedFixture(t *testing.T, root, name, source string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeJavaScriptFixture(t, filepath.Dir(path), filepath.Base(path), source)
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
