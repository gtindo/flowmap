package analyzer

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureGitSnapshotFindsAllLocalFunctionChanges(t *testing.T) {
	repository := t.TempDir()
	project := filepath.Join(repository, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-q")
	runGit(t, repository, "config", "user.email", "flowmap@example.com")
	runGit(t, repository, "config", "user.name", "Flowmap Test")
	runGit(t, repository, "branch", "-M", "feature/review")
	writeTestFile(t, filepath.Join(project, "sample.go"), `package sample

// Existing returns the original value.
func Existing() int { return 1 }

// Documented has old documentation.
func Documented() {}

func Stable() {}
func Deleted() {}
`)
	writeTestFile(t, filepath.Join(project, "sample_test.go"), `package sample

func TestExisting() { Existing() }
`)
	writeTestFile(t, filepath.Join(project, "space name.go"), "package sample\n\nfunc Spaced() int { return 1 }\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-qm", "initial")

	writeTestFile(t, filepath.Join(project, "sample.go"), `package sample

var Version = 2

// Existing returns the original value.
func Existing() int { return 2 }

// Documented has new documentation only.
func Documented() {}

func Stable() {}
func NewTracked() {}
`)
	runGit(t, repository, "add", "project/sample.go")
	writeTestFile(t, filepath.Join(project, "sample.go"), strings.Replace(
		mustReadTestFile(t, filepath.Join(project, "sample.go")), "return 2", "return 3", 1,
	))
	writeTestFile(t, filepath.Join(project, "sample_test.go"), `package sample

func TestExisting() { Existing(); Existing() }
`)
	writeTestFile(t, filepath.Join(project, "space name.go"), "package sample\n\nfunc Spaced() int { return 2 }\n")
	writeTestFile(t, filepath.Join(project, "untracked.go"), `package sample

// NewUntracked is not in the index yet.
func NewUntracked() {}
`)

	functions := testFunctionsFromFiles(t, project, "sample.go", "sample_test.go", "space name.go", "untracked.go")
	existingFunction := findTestFunctionByName(t, functions, "sample.Existing")
	functions["anonymous"] = Function{
		ID: "anonymous", Name: "Existing$1", QualifiedName: "sample.Existing$1",
		Package: "sample", File: existingFunction.File, Line: existingFunction.Line,
		EndLine: existingFunction.EndLine, Anonymous: true,
	}
	snapshot := captureGitSnapshot(context.Background(), project, functions)
	if !snapshot.Available || snapshot.Branch != "feature/review" || snapshot.Detached || snapshot.Revision == "" {
		t.Fatalf("Git snapshot = %#v", snapshot)
	}
	want := map[string]string{
		"sample.Documented":   "updated",
		"sample.Existing":     "updated",
		"sample.NewTracked":   "new",
		"sample.NewUntracked": "new",
		"sample.Spaced":       "updated",
		"sample.TestExisting": "updated",
	}
	if len(snapshot.ChangedFunctions) != len(want) {
		t.Fatalf("changed functions = %#v", snapshot.ChangedFunctions)
	}
	for index, changed := range snapshot.ChangedFunctions {
		if index > 0 && snapshot.ChangedFunctions[index-1].QualifiedName > changed.QualifiedName {
			t.Fatalf("changed functions are not sorted: %#v", snapshot.ChangedFunctions)
		}
		if want[changed.QualifiedName] != changed.Kind {
			t.Errorf("%s kind = %q, want %q", changed.QualifiedName, changed.Kind, want[changed.QualifiedName])
		}
		function := findTestFunctionByName(t, functions, changed.QualifiedName)
		if function.Change == nil || function.Change.Diff == "" {
			t.Errorf("%s omitted its diff", changed.QualifiedName)
		}
	}
	if stable := findTestFunctionByName(t, functions, "sample.Stable"); stable.Change != nil {
		t.Fatalf("unchanged function marked changed: %#v", stable.Change)
	}
	documented := findTestFunctionByName(t, functions, "sample.Documented")
	if !strings.Contains(documented.Change.Diff, "new documentation only") {
		t.Fatalf("documentation-only diff = %q", documented.Change.Diff)
	}
	existing := findTestFunctionByName(t, functions, "sample.Existing")
	if !strings.Contains(existing.Change.Diff, "return 3") {
		t.Fatalf("combined staged/unstaged diff = %q", existing.Change.Diff)
	}

	runGit(t, repository, "checkout", "--detach", "-q")
	detached := captureGitSnapshot(context.Background(), project, functions)
	if !detached.Detached || detached.Branch != "" || detached.Revision == "" {
		t.Fatalf("detached snapshot = %#v", detached)
	}
}

func TestCaptureGitSnapshotHandlesUnbornAndNonGitDirectories(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-q")
	writeTestFile(t, filepath.Join(repository, "new.go"), "package fresh\n\nfunc First() {}\n")
	functions := testFunctionsFromFiles(t, repository, "new.go")
	snapshot := captureGitSnapshot(context.Background(), repository, functions)
	if !snapshot.Available || snapshot.Detached || snapshot.Revision != "" || snapshot.Branch == "" {
		t.Fatalf("unborn snapshot = %#v", snapshot)
	}
	if len(snapshot.ChangedFunctions) != 1 || snapshot.ChangedFunctions[0].Kind != "new" {
		t.Fatalf("unborn changes = %#v", snapshot.ChangedFunctions)
	}

	nonRepository := t.TempDir()
	writeTestFile(t, filepath.Join(nonRepository, "plain.go"), "package plain\n\nfunc Plain() {}\n")
	nonGit := captureGitSnapshot(context.Background(), nonRepository, testFunctionsFromFiles(t, nonRepository, "plain.go"))
	if nonGit.Available || nonGit.ChangedFunctions == nil {
		t.Fatalf("non-Git snapshot = %#v", nonGit)
	}
}

func testFunctionsFromFiles(t *testing.T, root string, names ...string) map[string]Function {
	t.Helper()
	result := make(map[string]Function)
	for _, name := range names {
		filename := filepath.Join(root, name)
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, filename, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			functionDeclaration, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			qualifiedName := parsed.Name.Name + "." + functionDeclaration.Name.Name
			position := fileSet.Position(functionDeclaration.Pos())
			end := fileSet.Position(functionDeclaration.End())
			id := fmt.Sprintf("%s:%d", name, position.Line)
			result[id] = Function{
				ID: id, Name: functionDeclaration.Name.Name, QualifiedName: qualifiedName,
				Package: parsed.Name.Name, File: filename, Line: position.Line, EndLine: end.Line,
				Test: strings.HasSuffix(name, "_test.go"),
			}
		}
	}
	return result
}

func findTestFunctionByName(t *testing.T, functions map[string]Function, qualifiedName string) Function {
	t.Helper()
	for _, function := range functions {
		if function.QualifiedName == qualifiedName {
			return function
		}
	}
	t.Fatalf("function %s not found", qualifiedName)
	return Function{}
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadTestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
