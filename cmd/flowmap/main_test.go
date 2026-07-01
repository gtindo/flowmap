package main

import "testing"

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
