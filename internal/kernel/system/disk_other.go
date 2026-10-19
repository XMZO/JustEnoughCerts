//go:build !linux && !darwin

package system

// diskSpace cannot tell the space of a file system here.
func diskSpace(string) (free, total uint64, ok bool) { return 0, 0, false }
