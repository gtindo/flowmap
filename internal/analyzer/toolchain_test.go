package analyzer

import (
	"strings"
	"testing"
)

func TestCheckToolchainVersionsAcceptsCurrentAndOlderToolchains(t *testing.T) {
	for _, activeVersion := range []string{"go1.24.12", "go1.25.8", "go1.26", "go1.26.3"} {
		if err := checkToolchainVersions("go1.26.3", activeVersion); err != nil {
			t.Errorf("checkToolchainVersions(go1.26.3, %s) error = %v", activeVersion, err)
		}
	}
}

func TestCheckToolchainVersionsRejectsNewerToolchainWithGuidance(t *testing.T) {
	err := checkToolchainVersions("go1.26.3", "go1.27.1")
	if err == nil {
		t.Fatal("checkToolchainVersions() accepted a newer Go toolchain")
	}
	message := err.Error()
	for _, expected := range []string{"active Go toolchain go1.27", "built with go1.26", "install a Flowmap release built with go1.27"} {
		if !strings.Contains(message, expected) {
			t.Errorf("version-skew error omitted %q: %s", expected, message)
		}
	}
}

func TestCheckToolchainVersionsToleratesDevelopmentVersion(t *testing.T) {
	if err := checkToolchainVersions("devel go1.27-deadbeef", "go1.26.3"); err != nil {
		t.Fatalf("checkToolchainVersions() error = %v", err)
	}
}
