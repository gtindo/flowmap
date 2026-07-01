package analyzer

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestCollectLoadReportDeduplicatesAndClassifiesDiagnostics(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work", "project")
	typePosition := filepath.Join(root, "broken", "broken.go") + ":7:2"
	loaded := []*packages.Package{
		{ID: "example.com/project/b [example.com/project/b.test]", Errors: []packages.Error{
			{Kind: packages.TypeError, Pos: typePosition, Msg: "undefined: missing"},
			{Kind: packages.TypeError, Pos: typePosition, Msg: "undefined: missing"},
		}},
		{ID: "example.com/project/a", Errors: []packages.Error{
			{Kind: packages.TypeError, Pos: typePosition, Msg: "undefined: missing"},
			{Kind: packages.ParseError, Pos: filepath.Join(root, "a.go") + ":3:1", Msg: "expected declaration"},
			{Kind: packages.ListError, Msg: "cannot find module providing package example.com/lost"},
			{Kind: packages.UnknownError, Pos: "-", Msg: "driver failed"},
		}},
		{ID: "example.com/project/healthy"},
	}

	report := collectLoadReport(root, []string{"integration", "linux"}, loaded)
	if report.TotalPackageVariants != 3 || report.FailedPackageVariants != 2 {
		t.Fatalf("package counts = %d total, %d failed", report.TotalPackageVariants, report.FailedPackageVariants)
	}
	if got, want := diagnosticKinds(report.Diagnostics), []string{"go list", "syntax", "type", "unknown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic kinds = %v, want %v", got, want)
	}
	typeDiagnostic := report.Diagnostics[2]
	if typeDiagnostic.Position != "broken/broken.go:7:2" {
		t.Fatalf("relative position = %q", typeDiagnostic.Position)
	}
	wantPackages := []string{"example.com/project/a", "example.com/project/b [example.com/project/b.test]"}
	if !reflect.DeepEqual(typeDiagnostic.Packages, wantPackages) {
		t.Fatalf("affected packages = %v, want %v", typeDiagnostic.Packages, wantPackages)
	}
	formatted := report.String()
	if !strings.Contains(formatted, "[type] broken/broken.go:7:2: undefined: missing") {
		t.Fatalf("formatted report omitted exact type error:\n%s", formatted)
	}
	if !strings.Contains(formatted, "packages (2): example.com/project/a, example.com/project/b [example.com/project/b.test]") {
		t.Fatalf("formatted report omitted the affected package count:\n%s", formatted)
	}
	if !strings.Contains(formatted, "go -C '/work/project' test -tags='integration,linux' ./...") {
		t.Fatalf("formatted report omitted tagged reproduction command:\n%s", formatted)
	}
}

func TestLoadReportBoundsAffectedPackageNames(t *testing.T) {
	report := LoadReport{
		Root: "/work/project", TotalPackageVariants: 5, FailedPackageVariants: 5,
		Diagnostics: []LoadDiagnostic{{
			Kind: "go list", Message: "dependency unavailable", Packages: []string{"a", "b", "c", "d", "e"},
		}},
	}
	formatted := report.String()
	if !strings.Contains(formatted, "packages (5): a, b, c, ... and 2 more") {
		t.Fatalf("affected package display was not bounded:\n%s", formatted)
	}
}

func TestLoadReportDisplayIsBounded(t *testing.T) {
	report := LoadReport{Root: "/work/project", TotalPackageVariants: 12, FailedPackageVariants: 12}
	for diagnosticIndex := 0; diagnosticIndex < 12; diagnosticIndex++ {
		report.Diagnostics = append(report.Diagnostics, LoadDiagnostic{
			Kind: "type", Position: fmt.Sprintf("file%d.go:1:1", diagnosticIndex), Message: "broken",
		})
	}

	formatted := report.String()
	if strings.Count(formatted, "\n  - [type]") != displayedLoadDiagnosticLimit {
		t.Fatalf("displayed diagnostics were not capped:\n%s", formatted)
	}
	if !strings.Contains(formatted, "... and 2 more unique diagnostics") {
		t.Fatalf("hidden diagnostic count missing:\n%s", formatted)
	}
}

func TestShellQuoteHandlesApostrophes(t *testing.T) {
	report := LoadReport{Root: "/work/friend's project"}
	if got, want := report.reproductionCommand(), "go -C '/work/friend'\"'\"'s project' test ./..."; got != want {
		t.Fatalf("reproduction command = %q, want %q", got, want)
	}
}

func diagnosticKinds(diagnostics []LoadDiagnostic) []string {
	result := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, diagnostic.Kind)
	}
	return result
}
