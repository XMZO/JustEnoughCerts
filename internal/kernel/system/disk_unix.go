//go:build linux || darwin

package system

import "syscall"

// diskSpace tells the bytes free to JEC and in all on the file system that
// dir is on.
func diskSpace(dir string) (free, total uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, false
	}
	bsize := uint64(max(st.Bsize, 0)) // its type differs between systems
	return st.Bavail * bsize, st.Blocks * bsize, true
}
