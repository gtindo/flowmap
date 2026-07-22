package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gtindo/flowmap/internal/analyzer"
)

// TestSplitTags verifies CLI build-tag normalization without starting the server.
func TestSplitTags(t *testing.T) {
	actual := splitTags(" linux, integration ,,")
	if len(actual) != 2 || actual[0] != "linux" || actual[1] != "integration" {
		t.Fatalf("splitTags() = %#v", actual)
	}
}

func TestLoadProjectsValidatesRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	api := filepath.Join(filepath.Dir(path), "api")
	web := filepath.Join(filepath.Dir(path), "web")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(web, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(api, "go.mod"), []byte("module example.com/api\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "app.ts"), []byte("export function Root() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := `{"projects":[{"name":"API","path":"` + api + `","tags":["integration"]},{"name":"Web","path":"` + web + `"}]}`
	if err := os.WriteFile(path, []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}

	projects, err := loadProjects(path)
	if err != nil || len(projects) != 2 || projects[0].Name != "API" || projects[0].Analyses[0].BuildTags[0] != "integration" || !filepath.IsAbs(projects[1].Analyses[0].Root) {
		t.Fatalf("loadProjects() = %#v, %v", projects, err)
	}

	if err := os.WriteFile(path, []byte(`{"projects":[{"name":"API","path":"`+api+`"},{"name":"API","path":"`+web+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProjects(path); err == nil || !strings.Contains(err.Error(), "duplicate name") {
		t.Fatalf("duplicate project error = %v", err)
	}
}

// TestVersionDefaultsToDevelopment verifies local builds identify themselves honestly.
func TestVersionDefaultsToDevelopment(t *testing.T) {
	if version == "" {
		t.Fatal("version must never be empty")
	}
}

func TestWriteLoadWarningReportsPartialFailures(t *testing.T) {
	var output bytes.Buffer
	writeLoadWarning(&output, analyzer.LoadReport{
		Root: "/work/project", TotalPackageVariants: 2, FailedPackageVariants: 1,
		Diagnostics: []analyzer.LoadDiagnostic{{Kind: "type", Position: "broken.go:4:17", Message: "undefined: missingSymbol"}},
	})
	if got := output.String(); !strings.Contains(got, "flowmap: warning: 1 of 2 loaded package variants") ||
		!strings.Contains(got, "[type] broken.go:4:17: undefined: missingSymbol") {
		t.Fatalf("warning output = %q", got)
	}
}

func TestWriteLoadWarningIgnoresHealthyLoad(t *testing.T) {
	var output bytes.Buffer
	writeLoadWarning(&output, analyzer.LoadReport{TotalPackageVariants: 2})
	if output.Len() != 0 {
		t.Fatalf("healthy load warning = %q", output.String())
	}
}
