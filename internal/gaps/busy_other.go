//go:build !windows

package gaps

// busy is Windows-only: elsewhere an open file never blocks another open or a rename.
func busy(error) bool { return false }
