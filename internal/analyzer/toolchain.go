package analyzer

import (
	"context"
	"fmt"
	"go/version"
	"os/exec"
	"runtime"
	"strings"
)

// checkActiveToolchain ensures go/packages will not emit export data newer
// than the Go runtime embedded in this Flowmap executable can read.
func checkActiveToolchain(ctx context.Context, root string) error {
	command := exec.CommandContext(ctx, "go", "env", "GOVERSION")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("inspect active Go toolchain: %w", err)
	}
	return checkToolchainVersions(runtime.Version(), strings.TrimSpace(string(output)))
}

func checkToolchainVersions(applicationVersion string, activeVersion string) error {
	applicationLanguage := version.Lang(applicationVersion)
	activeLanguage := version.Lang(activeVersion)
	if applicationLanguage == "" || activeLanguage == "" {
		return nil
	}
	if version.Compare(activeLanguage, applicationLanguage) <= 0 {
		return nil
	}
	return fmt.Errorf(
		"active Go toolchain %s is newer than this Flowmap binary (built with %s); install a Flowmap release built with %s, or select %s or older if the project supports it",
		activeLanguage, applicationLanguage, activeLanguage, applicationLanguage,
	)
}
