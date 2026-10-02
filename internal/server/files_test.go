package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/engine"
	"github.com/gtindo/flowmap/internal/protocol"
)

// TestFileListingGroupsFunctionsByFile verifies explorer grouping and order.
func TestFileListingGroupsFunctionsByFile(t *testing.T) {
	summary := func(id string, path string, line int) protocol.SymbolSummary {
		return protocol.SymbolSummary{SymbolID: id, QualifiedName: "pkg." + id, Location: protocol.SourceLocation{URI: protocol.FileURI(path), StartLine: line, EndLine: line + 1}}
	}

	tests := []struct {
		name    string
		roots   []string
		paths   []string
		symbols []protocol.SymbolSummary
		want    map[string][]string
	}{
		{
			name:  "files without functions are listed",
			roots: []string{"/work/project"},
			paths: []string{"README.md", "main.go"},
			want:  map[string][]string{"README.md": {}, "main.go": {}},
		},
		{
			name:    "functions sort by line within their file",
			roots:   []string{"/work/project"},
			paths:   []string{"main.go"},
			symbols: []protocol.SymbolSummary{summary("Later", "/work/project/main.go", 30), summary("Earlier", "/work/project/main.go", 4)},
			want:    map[string][]string{"main.go": {"Earlier", "Later"}},
		},
		{
			name:    "analyzed files missing from the listing are added",
			roots:   []string{"/work/project"},
			symbols: []protocol.SymbolSummary{summary("Generated", "/work/project/gen/ignored.go", 1)},
			want:    map[string][]string{"gen/ignored.go": {"Generated"}},
		},
		{
			name:    "functions outside every root are omitted",
			roots:   []string{"/work/project"},
			symbols: []protocol.SymbolSummary{summary("Outside", "/work/other/main.go", 1), summary("Sibling", "/work/project-two/main.go", 1)},
			want:    map[string][]string{},
		},
		{
			name:    "a resolved root also matches",
			roots:   []string{"/link/project", "/real/project"},
			symbols: []protocol.SymbolSummary{summary("Resolved", "/real/project/a/b.go", 2)},
			want:    map[string][]string{"a/b.go": {"Resolved"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := fileListing(test.roots, test.paths, test.symbols)

			got := make(map[string][]string, len(files))
			previous := ""
			for _, file := range files {
				if file.Path < previous {
					t.Fatalf("files are not sorted: %q after %q", file.Path, previous)
				}
				previous = file.Path

				ids := make([]string, 0, len(file.Functions))
				for _, function := range file.Functions {
					ids = append(ids, function.ID)
				}
				got[file.Path] = ids
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("fileListing() = %v, want %v", got, test.want)
			}
		})
	}
}

// TestListProjectFilesHonorsGitIgnore verifies that Git listings drop ignored
// and deleted files but keep untracked ones.
func TestListProjectFilesHonorsGitIgnore(t *testing.T) {
	root := t.TempDir()
	writeTestFiles(t, root, map[string]string{
		".gitignore":            "dist/\n",
		"main.go":               "package main\n",
		"deleted.go":            "package main\n",
		"dist/bundle.js":        "ignored\n",
		"internal/untracked.go": "package internal\n",
	})
	runServerGit(t, root, "init", "-q")
	runServerGit(t, root, "add", ".gitignore", "main.go", "deleted.go")
	if err := os.Remove(filepath.Join(root, "deleted.go")); err != nil {
		t.Fatal(err)
	}

	paths, truncated, err := listProjectFiles(testContext(t), root)
	if err != nil || truncated {
		t.Fatalf("listProjectFiles() = %v, %v, %v", paths, truncated, err)
	}
	want := []string{".gitignore", "internal/untracked.go", "main.go"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("listProjectFiles() = %v, want %v", paths, want)
	}
}

// TestListProjectFilesWalksWithoutGit verifies the non-Git fallback skips
// dependency and hidden directories.
func TestListProjectFilesWalksWithoutGit(t *testing.T) {
	root := t.TempDir()
	writeTestFiles(t, root, map[string]string{
		"index.js":                  "export function main() {}\n",
		"src/app.ts":                "export class App {}\n",
		"node_modules/lib/index.js": "module.exports = {}\n",
		"vendor/dep/dep.go":         "package dep\n",
		".cache/data":               "cached\n",
	})

	paths, truncated, err := listProjectFiles(testContext(t), root)
	if err != nil || truncated {
		t.Fatalf("listProjectFiles() = %v, %v, %v", paths, truncated, err)
	}
	want := []string{"index.js", "src/app.ts"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("listProjectFiles() = %v, want %v", paths, want)
	}
}

// TestFilesEndpointServesExplorerTree verifies the browser explorer API.
func TestFilesEndpointServesExplorerTree(t *testing.T) {
	root := t.TempDir()
	writeTestFiles(t, root, map[string]string{
		"README.md":      "# sample\n",
		"sample.go":      "package sample\n",
		"sample_test.go": "package sample\n",
	})
	file := filepath.Join(root, "sample.go")
	index := &analyzer.Index{
		Functions: map[string]analyzer.Function{
			"helper":  {ID: "helper", Name: "helper", QualifiedName: "sample.helper", Package: "sample", File: file, Line: 20, EndLine: 22, Classification: analyzer.Classification{Kind: "unknown"}},
			"root":    {ID: "root", Name: "Root", QualifiedName: "sample.Root", Package: "sample", File: file, Line: 3, EndLine: 9, Public: true, Classification: analyzer.Classification{Kind: "pure"}},
			"closure": {ID: "closure", Name: "Root$1", QualifiedName: "sample.Root$1", Package: "sample", File: file, Line: 5, EndLine: 5, Anonymous: true},
			"test":    {ID: "test", Name: "TestRoot", QualifiedName: "sample.TestRoot", Package: "sample", File: filepath.Join(root, "sample_test.go"), Line: 3, EndLine: 5, Test: true},
		},
		Outgoing: map[string][]analyzer.Edge{},
		Incoming: map[string][]analyzer.Edge{},
	}
	analyze := func(context.Context, analyzer.Config) (*analyzer.Index, error) { return index, nil }
	app := newTestApp(t, []ProjectConfig{{Name: DefaultProjectName, Analysis: analyzer.Config{Root: root}}}, engine.Options{Analyze: analyze})
	scanAll(t, app)

	response := serve(app, http.MethodGet, "/api/files")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/files status = %d body = %s", response.Code, response.Body.String())
	}
	var explorer ExplorerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &explorer); err != nil {
		t.Fatal(err)
	}

	got := make(map[string]string, len(explorer.Files))
	for _, entry := range explorer.Files {
		names := make([]string, 0, len(entry.Functions))
		for _, function := range entry.Functions {
			names = append(names, function.QualifiedName)
		}
		got[entry.Path] = strings.Join(names, ",")
	}
	want := map[string]string{"README.md": "", "sample.go": "sample.Root,sample.helper", "sample_test.go": "sample.TestRoot"}
	if !reflect.DeepEqual(got, want) || explorer.Truncated {
		t.Fatalf("explorer = %#v, want files %v", explorer, want)
	}
	if !strings.Contains(response.Body.String(), `"functions":[]`) || !strings.Contains(response.Body.String(), `"classification":"pure"`) {
		t.Fatalf("explorer JSON = %s", response.Body.String())
	}
}

func writeTestFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for path, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runServerGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

// TestHandlerServesFileExplorerDrawer verifies the embedded explorer assets.
func TestHandlerServesFileExplorerDrawer(t *testing.T) {
	app := newScannedApp(t, fixtureIndex(), engine.Options{})

	expectations := map[string][]string{
		"/":       {`id="explorer"`, `id="explorer-toggle"`, `aria-controls="explorer"`, `id="explorer-tree"`, `role="tree"`, `id="stage"`},
		"/app.js": {`json("/api/files")`, "flowmap-explorer-open:v1", "buildExplorerTree", "revealInExplorer", "handleExplorerShortcut"},
	}
	for path, expected := range expectations {
		response := serve(app, http.MethodGet, path)
		for _, fragment := range expected {
			if !strings.Contains(response.Body.String(), fragment) {
				t.Fatalf("GET %s omitted %s", path, fragment)
			}
		}
	}
}
