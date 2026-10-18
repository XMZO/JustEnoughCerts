//go:build !unix

package plugins

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

// ownGroup does nothing: JEC is built for Unix systems only, and runs
// elsewhere for development.
func ownGroup(*exec.Cmd) {}

// executable reports whether the file is a program to run.
func executable(info fs.FileInfo) bool { return strings.EqualFold(filepath.Ext(info.Name()), ".exe") }

// unsafe returns "": there are no Unix permissions to check.
func unsafe(fs.FileInfo) string { return "" }
