// Package buildinfo reports the version of the running binary.
//
// Release builds set the values at link time with -ldflags "-X"; any other
// build falls back to what the Go toolchain embeds from the module and VCS.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

// Set at link time, for example:
//
//	-X github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo.version=1.2.3
var (
	version string
	commit  string
	date    string
)

// Info describes the running binary.
type Info struct {
	// Version is the release version without the leading "v", or "dev".
	Version string `json:"version"`
	// Commit is the full VCS revision, if known.
	Commit string `json:"commit,omitempty"`
	// Date is the RFC 3339 commit time, if known. Release builds use the
	// commit time rather than the wall clock so they are reproducible.
	Date string `json:"date,omitempty"`
	// GoVersion is the toolchain that built the binary.
	GoVersion string `json:"goVersion"`
	// Platform is GOOS/GOARCH.
	Platform string `json:"platform"`
}

var get = sync.OnceValue(load)

// Get returns the build information of the running binary.
func Get() Info { return get() }

func load() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = strings.TrimPrefix(bi.Main.Version, "v")
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && info.Commit == "":
				info.Commit = s.Value
			case s.Key == "vcs.time" && info.Date == "":
				info.Date = s.Value
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}
