package analyzer

import (
	"fmt"
	"strings"

	"github.com/gtindo/flowmap/internal/semantic"
)

const (
	displayedLoadDiagnosticLimit = 10
	displayedPackageLimit        = 3
)

// LoadDiagnostic is one unique backend loading problem and the package
// variants in which the current Go backend reported it.
type LoadDiagnostic struct {
	Kind     string   `json:"kind"`
	Position string   `json:"position"`
	Message  string   `json:"message"`
	Packages []string `json:"packages"`
}

// LoadReport summarizes package variants omitted from an analysis.
type LoadReport struct {
	Root                  string           `json:"root"`
	BuildTags             []string         `json:"build_tags"`
	TotalPackageVariants  int              `json:"total_package_variants"`
	FailedPackageVariants int              `json:"failed_package_variants"`
	Diagnostics           []LoadDiagnostic `json:"diagnostics"`
}

// HasFailures reports whether any loaded package variant was omitted.
func (report LoadReport) HasFailures() bool {
	return report.FailedPackageVariants > 0
}

// String renders a bounded, deterministic diagnostic report for the CLI.
func (report LoadReport) String() string {
	var output strings.Builder
	fmt.Fprintf(&output, "%d of %d loaded package variants could not be analyzed",
		report.FailedPackageVariants, report.TotalPackageVariants)

	diagnosticCount := len(report.Diagnostics)
	if diagnosticCount > 0 {
		fmt.Fprintf(&output, "\n%d unique %s:", diagnosticCount, plural(diagnosticCount, "diagnostic", "diagnostics"))
		shown := min(diagnosticCount, displayedLoadDiagnosticLimit)
		for _, diagnostic := range report.Diagnostics[:shown] {
			fmt.Fprintf(&output, "\n  - [%s]", diagnostic.Kind)
			if diagnostic.Position != "" && diagnostic.Position != "-" {
				fmt.Fprintf(&output, " %s:", diagnostic.Position)
			}
			fmt.Fprintf(&output, " %s", diagnostic.Message)
			if len(diagnostic.Packages) > 0 {
				fmt.Fprintf(&output, "\n    packages (%d): %s", len(diagnostic.Packages), displayedPackages(diagnostic.Packages))
			}
		}
		if hidden := diagnosticCount - shown; hidden > 0 {
			fmt.Fprintf(&output, "\n  ... and %d more unique %s", hidden, plural(hidden, "diagnostic", "diagnostics"))
		}
	}

	fmt.Fprintf(&output, "\nReproduce with: %s", report.reproductionCommand())
	return output.String()
}

func displayedPackages(packageNames []string) string {
	shown := min(len(packageNames), displayedPackageLimit)
	result := strings.Join(packageNames[:shown], ", ")
	if hidden := len(packageNames) - shown; hidden > 0 {
		result += fmt.Sprintf(", ... and %d more", hidden)
	}
	return result
}

func loadReportFromSemantic(root string, diagnostics semantic.DiagnosticReport) LoadReport {
	report := LoadReport{
		Root: root, BuildTags: append([]string(nil), diagnostics.BuildTags...),
		TotalPackageVariants:  diagnostics.TotalUnits,
		FailedPackageVariants: diagnostics.FailedUnits,
	}
	for _, diagnostic := range diagnostics.Diagnostics {
		report.Diagnostics = append(report.Diagnostics, LoadDiagnostic{
			Kind: diagnostic.Kind, Position: diagnostic.Position, Message: diagnostic.Message,
			Packages: append([]string(nil), diagnostic.Units...),
		})
	}
	return report
}

func (report LoadReport) reproductionCommand() string {
	command := "go -C " + shellQuote(report.Root) + " test"
	if len(report.BuildTags) > 0 {
		command += " -tags=" + shellQuote(strings.Join(report.BuildTags, ","))
	}
	return command + " ./..."
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
