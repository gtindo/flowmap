package gobackend

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gtindo/flowmap/internal/semantic"
	"golang.org/x/tools/go/packages"
)

const (
	displayedDiagnosticLimit = 10
	displayedUnitLimit       = 3
)

// collectDiagnosticReport converts Go loader failures into backend-neutral diagnostics.
func collectDiagnosticReport(root string, buildTags []string, loaded []*packages.Package) semantic.DiagnosticReport {
	type diagnosticKey struct {
		kind     string
		position string
		message  string
	}
	unitsByDiagnostic := make(map[diagnosticKey]map[string]struct{})
	report := semantic.DiagnosticReport{
		BuildTags: append([]string(nil), buildTags...), TotalUnits: len(loaded),
	}

	for _, loadedPackage := range loaded {
		if len(loadedPackage.Errors) == 0 {
			continue
		}
		report.FailedUnits++
		unit := loadedPackage.ID
		if unit == "" {
			unit = loadedPackage.PkgPath
		}
		if unit == "" {
			unit = "<unknown>"
		}
		for _, packageError := range loadedPackage.Errors {
			key := diagnosticKey{
				kind: loadErrorKind(packageError.Kind), position: relativePosition(root, packageError.Pos), message: packageError.Msg,
			}
			if unitsByDiagnostic[key] == nil {
				unitsByDiagnostic[key] = make(map[string]struct{})
			}
			unitsByDiagnostic[key][unit] = struct{}{}
		}
	}

	for key, unitSet := range unitsByDiagnostic {
		units := make([]string, 0, len(unitSet))
		for unit := range unitSet {
			units = append(units, unit)
		}
		sort.Strings(units)
		report.Diagnostics = append(report.Diagnostics, semantic.Diagnostic{
			Kind: key.kind, Position: key.position, Message: key.message, Units: units,
		})
	}
	sort.Slice(report.Diagnostics, func(left, right int) bool {
		leftDiagnostic, rightDiagnostic := report.Diagnostics[left], report.Diagnostics[right]
		if diagnosticKindRank(leftDiagnostic.Kind) != diagnosticKindRank(rightDiagnostic.Kind) {
			return diagnosticKindRank(leftDiagnostic.Kind) < diagnosticKindRank(rightDiagnostic.Kind)
		}
		if leftDiagnostic.Position != rightDiagnostic.Position {
			return leftDiagnostic.Position < rightDiagnostic.Position
		}
		return leftDiagnostic.Message < rightDiagnostic.Message
	})
	return report
}

func loadErrorKind(kind packages.ErrorKind) string {
	switch kind {
	case packages.ListError:
		return "go list"
	case packages.ParseError:
		return "syntax"
	case packages.TypeError:
		return "type"
	default:
		return "unknown"
	}
}

func diagnosticKindRank(kind string) int {
	switch kind {
	case "go list":
		return 0
	case "syntax":
		return 1
	case "type":
		return 2
	default:
		return 3
	}
}

func relativePosition(root, position string) string {
	rootPrefix := filepath.Clean(root) + string(filepath.Separator)
	if strings.HasPrefix(position, rootPrefix) {
		return filepath.ToSlash(strings.TrimPrefix(position, rootPrefix))
	}
	return filepath.ToSlash(position)
}

func reproductionCommand(root string, buildTags []string) string {
	command := "go -C " + shellQuote(root) + " test"
	if len(buildTags) > 0 {
		command += " -tags=" + shellQuote(strings.Join(buildTags, ","))
	}
	return command + " ./..."
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func formatDiagnosticReport(root string, report semantic.DiagnosticReport) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%d of %d loaded package variants could not be analyzed",
		report.FailedUnits, report.TotalUnits)
	if len(report.Diagnostics) > 0 {
		fmt.Fprintf(&output, "\n%d unique %s:", len(report.Diagnostics), plural(len(report.Diagnostics), "diagnostic", "diagnostics"))
		shown := min(len(report.Diagnostics), displayedDiagnosticLimit)
		for _, diagnostic := range report.Diagnostics[:shown] {
			fmt.Fprintf(&output, "\n  - [%s]", diagnostic.Kind)
			if diagnostic.Position != "" && diagnostic.Position != "-" {
				fmt.Fprintf(&output, " %s:", diagnostic.Position)
			}
			fmt.Fprintf(&output, " %s", diagnostic.Message)
			if len(diagnostic.Units) > 0 {
				fmt.Fprintf(&output, "\n    packages (%d): %s", len(diagnostic.Units), displayedUnits(diagnostic.Units))
			}
		}
		if hidden := len(report.Diagnostics) - shown; hidden > 0 {
			fmt.Fprintf(&output, "\n  ... and %d more unique %s", hidden, plural(hidden, "diagnostic", "diagnostics"))
		}
	}
	fmt.Fprintf(&output, "\nReproduce with: %s", reproductionCommand(root, report.BuildTags))
	return output.String()
}

func displayedUnits(units []string) string {
	shown := min(len(units), displayedUnitLimit)
	result := strings.Join(units[:shown], ", ")
	if hidden := len(units) - shown; hidden > 0 {
		result += fmt.Sprintf(", ... and %d more", hidden)
	}
	return result
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
