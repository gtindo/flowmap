// Package broken verifies that one invalid package does not hide healthy neighbors.
package broken

// Broken intentionally refers to a missing symbol for partial-load coverage.
func Broken() { missingSymbol() }
