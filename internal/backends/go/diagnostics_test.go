package gobackend

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gtindo/flowmap/internal/semantic"
	"golang.org/x/tools/go/packages"
)

func TestCollectDiagnosticReportDeduplicatesAndOrdersDiagnostics(t *testing.T) {
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

	report := collectDiagnosticReport(root, []string{"integration", "linux"}, loaded)
	if report.TotalUnits != 3 || report.FailedUnits != 2 {
		t.Fatalf("package counts = %d total, %d failed", report.TotalUnits, report.FailedUnits)
	}
	if got, want := diagnosticKinds(report.Diagnostics), []string{"go list", "syntax", "type", "unknown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic kinds = %v, want %v", got, want)
	}
	typeDiagnostic := report.Diagnostics[2]
	if typeDiagnostic.Position != "broken/broken.go:7:2" {
		t.Fatalf("relative position = %q", typeDiagnostic.Position)
	}
	wantUnits := []string{"example.com/project/a", "example.com/project/b [example.com/project/b.test]"}
	if !reflect.DeepEqual(typeDiagnostic.Units, wantUnits) {
		t.Fatalf("affected units = %v, want %v", typeDiagnostic.Units, wantUnits)
	}
}

func diagnosticKinds(diagnostics []semantic.Diagnostic) []string {
	result := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, diagnostic.Kind)
	}
	return result
}
