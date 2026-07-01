package sample

import "testing"

// TestNormalize exercises the pure normalization operation.
func TestNormalize(t *testing.T) {
	if Normalize(Input{Text: " x "}).Text != "x" {
		t.Fatal("unexpected output")
	}
}
