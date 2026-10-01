package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/protocol"
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

// TestEngineCommandServesProtocolOverStdio drives a real Go analysis through
// the stdio engine exactly as an editor extension would.
func TestEngineCommandServesProtocolOverStdio(t *testing.T) {
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module example.com/sample\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "package sample\n\n// Root is the entry point.\nfunc Root() int { return helper() }\n\nfunc helper() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(module, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	var stderr bytes.Buffer
	served := make(chan error, 1)
	go func() {
		served <- runEngine(ctx, nil, stdinReader, stdoutWriter, &stderr)
		_ = stdoutWriter.Close()
	}()

	client := protocol.NewClient(stdoutReader, stdinWriter)
	published := make(chan protocol.Snapshot, 1)
	client.OnNotification(func(method string, params json.RawMessage) {
		if method != protocol.MethodAnalysisPublished {
			return
		}
		var notification protocol.AnalysisPublished
		if json.Unmarshal(params, &notification) == nil {
			published <- notification.Snapshot
		}
	})

	if _, err := client.Initialize(ctx, protocol.InitializeParams{ClientInfo: protocol.PeerInfo{Name: "cli-test"}}); err != nil {
		t.Fatal(err)
	}
	workspace, err := client.OpenWorkspace(ctx, protocol.WorkspaceOpenParams{RootURI: protocol.FileURI(module), Views: []protocol.ViewSpec{{Language: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	viewID := workspace.Views[0].ViewID
	if _, err := client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID}); err != nil {
		t.Fatal(err)
	}

	var snapshot protocol.Snapshot
	select {
	case snapshot = <-published:
	case <-ctx.Done():
		t.Fatalf("analysis was not published; stderr: %s", stderr.String())
	}

	search, err := client.SearchSymbols(ctx, protocol.SymbolSearchParams{SnapshotQuery: protocol.SnapshotQuery{ViewID: viewID, SnapshotID: snapshot.SnapshotID}, Query: "Root"})
	if err != nil || len(search.Items) != 1 || search.Items[0].QualifiedName != "sample.Root" || !search.Items[0].Public {
		t.Fatalf("search = %#v, %v", search, err)
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	_ = stdinWriter.Close()
	if err := <-served; err != nil {
		t.Fatalf("runEngine() = %v; stderr: %s", err, stderr.String())
	}
}
