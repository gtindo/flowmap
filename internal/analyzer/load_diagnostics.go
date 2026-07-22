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
	Units    []string `json:"units,omitempty"`
}

// LoadReport summarizes package variants omitted from an analysis.
type LoadReport struct {
	Root                  string           `json:"root"`
	Language              string           `json:"language,omitempty"`
	BuildTags             []string         `json:"build_tags"`
	TotalPackageVariants  int              `json:"total_package_variants"`
	FailedPackageVariants int              `json:"failed_package_variants"`
	TotalUnits            int              `json:"total_units,omitempty"`
	FailedUnits           int              `json:"failed_units,omitempty"`
	Diagnostics           []LoadDiagnostic `json:"diagnostics"`
}

// HasFailures reports whether any loaded package variant was omitted.
func (report LoadReport) HasFailures() bool {
	return report.FailedPackageVariants > 0 || report.FailedUnits > 0
}

// String renders a bounded, deterministic diagnostic report for the CLI.
func (report LoadReport) String() string {
	var output strings.Builder
	failed, total := report.FailedPackageVariants, report.TotalPackageVariants
	unitLabel := "loaded package variants"
	if report.Language != "" && report.Language != LanguageGo {
		failed, total, unitLabel = report.FailedUnits, report.TotalUnits, "source files"
	}
	fmt.Fprintf(&output, "%d of %d %s could not be analyzed", failed, total, unitLabel)

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
			units := diagnostic.Packages
			label := "packages"
			if report.Language != "" && report.Language != LanguageGo {
				units, label = diagnostic.Units, "files"
			}
			if len(units) > 0 {
				fmt.Fprintf(&output, "\n    %s (%d): %s", label, len(units), displayedPackages(units))
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
		Root: root, Language: "go", BuildTags: append([]string(nil), diagnostics.BuildTags...),
		TotalPackageVariants:  diagnostics.TotalUnits,
		FailedPackageVariants: diagnostics.FailedUnits,
		TotalUnits:            diagnostics.TotalUnits, FailedUnits: diagnostics.FailedUnits,
	}
	for _, diagnostic := range diagnostics.Diagnostics {
		report.Diagnostics = append(report.Diagnostics, LoadDiagnostic{
			Kind: diagnostic.Kind, Position: diagnostic.Position, Message: diagnostic.Message,
			Packages: append([]string(nil), diagnostic.Units...), Units: append([]string(nil), diagnostic.Units...),
		})
	}
	return report
}

func loadReportFromJavaScript(root string, diagnostics semantic.DiagnosticReport) LoadReport {
	report := LoadReport{Root: root, Language: LanguageJavaScript, TotalUnits: diagnostics.TotalUnits, FailedUnits: diagnostics.FailedUnits}
	for _, diagnostic := range diagnostics.Diagnostics {
		report.Diagnostics = append(report.Diagnostics, LoadDiagnostic{Kind: diagnostic.Kind, Position: diagnostic.Position, Message: diagnostic.Message, Units: append([]string(nil), diagnostic.Units...)})
	}
	return report
}

func (report LoadReport) reproductionCommand() string {
	if report.Language == LanguageJavaScript {
		return "flowmap serve " + shellQuote(report.Root)
	}
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
