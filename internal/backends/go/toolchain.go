package gobackend

import (
	"context"
	"encoding/json"
	"fmt"
	"go/version"
	"os/exec"
	"runtime"
)

// toolchainEnvironment holds the active toolchain facts the loader depends on.
type toolchainEnvironment struct {
	Version     string `json:"GOVERSION"`
	Root        string `json:"GOROOT"`
	ModuleCache string `json:"GOMODCACHE"`
}

// inspectActiveToolchain protects go/packages from newer export data and
// reports where standard-library and module-cache sources live.
func inspectActiveToolchain(ctx context.Context, root string) (toolchainEnvironment, error) {
	command := exec.CommandContext(ctx, "go", "env", "-json", "GOVERSION", "GOROOT", "GOMODCACHE")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return toolchainEnvironment{}, fmt.Errorf("inspect active Go toolchain: %w", err)
	}

	var environment toolchainEnvironment
	if err := json.Unmarshal(output, &environment); err != nil {
		return toolchainEnvironment{}, fmt.Errorf("decode active Go toolchain environment: %w", err)
	}

	if err := checkToolchainVersions(runtime.Version(), environment.Version); err != nil {
		return toolchainEnvironment{}, err
	}
	return environment, nil
}

func checkToolchainVersions(applicationVersion, activeVersion string) error {
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
