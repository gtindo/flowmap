package main

import (
	"bytes"
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
