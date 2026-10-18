//go:build unix

package plugins

import (
	"io/fs"
	"os"
	"os/exec"
	"syscall"
)

// ownGroup runs cmd in a process group of its own, killed as a whole when
// the call ends early, so that nothing the plugin started lives on.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// executable reports whether the file is a program to run.
func executable(info fs.FileInfo) bool { return info.Mode()&0o111 != 0 }

// unsafe returns why JEC does not run a file, or what a directory holds:
// someone else than root and the user JEC runs as may change it. It
// returns "" when the file is safe.
func unsafe(info fs.FileInfo) string {
	if info.Mode()&0o022 != 0 {
		return "writable"
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int(st.Uid) != os.Geteuid() {
		return "owner"
	}
	return ""
}
