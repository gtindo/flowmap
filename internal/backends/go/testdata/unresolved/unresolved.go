// Package unresolved holds dynamic calls whose callees are never analyzed.
package unresolved

// Describe dispatches through error, whose implementations live outside the module.
func Describe(err error) string { return err.Error() }

// Apply invokes a function value that no local caller supplies.
func Apply(transform func(int) int) int { return transform(1) }

// Length uses builtins only.
func Length(values []int) int { return len(values) }
