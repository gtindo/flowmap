package analyzer

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	displayedLoadDiagnosticLimit = 10
	displayedPackageLimit        = 3
)

// LoadDiagnostic is one unique Go package loading problem and the package
// variants in which go/packages reported it.
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

func collectLoadReport(root string, buildTags []string, loaded []*packages.Package) LoadReport {
	type diagnosticKey struct {
		kind     string
		position string
		message  string
	}
	packagesByDiagnostic := make(map[diagnosticKey]map[string]struct{})
	report := LoadReport{
		Root:                 root,
		BuildTags:            append([]string(nil), buildTags...),
		TotalPackageVariants: len(loaded),
	}

	for _, loadedPackage := range loaded {
		if len(loadedPackage.Errors) == 0 {
			continue
		}
		report.FailedPackageVariants++
		packageName := loadedPackage.ID
		if packageName == "" {
			packageName = loadedPackage.PkgPath
		}
		if packageName == "" {
			packageName = "<unknown>"
		}
		for _, packageError := range loadedPackage.Errors {
			key := diagnosticKey{
				kind:     loadErrorKind(packageError.Kind),
				position: relativeDiagnosticPosition(root, packageError.Pos),
				message:  packageError.Msg,
			}
			if packagesByDiagnostic[key] == nil {
				packagesByDiagnostic[key] = make(map[string]struct{})
			}
			packagesByDiagnostic[key][packageName] = struct{}{}
		}
	}

	for key, packageSet := range packagesByDiagnostic {
		packageNames := make([]string, 0, len(packageSet))
		for packageName := range packageSet {
			packageNames = append(packageNames, packageName)
		}
		sort.Strings(packageNames)
		report.Diagnostics = append(report.Diagnostics, LoadDiagnostic{
			Kind: key.kind, Position: key.position, Message: key.message, Packages: packageNames,
		})
	}
	sort.Slice(report.Diagnostics, func(left, right int) bool {
		leftDiagnostic, rightDiagnostic := report.Diagnostics[left], report.Diagnostics[right]
		if loadErrorKindRank(leftDiagnostic.Kind) != loadErrorKindRank(rightDiagnostic.Kind) {
			return loadErrorKindRank(leftDiagnostic.Kind) < loadErrorKindRank(rightDiagnostic.Kind)
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

func loadErrorKindRank(kind string) int {
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

func relativeDiagnosticPosition(root, position string) string {
	rootPrefix := filepath.Clean(root) + string(filepath.Separator)
	if strings.HasPrefix(position, rootPrefix) {
		return filepath.ToSlash(strings.TrimPrefix(position, rootPrefix))
	}
	return filepath.ToSlash(position)
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
