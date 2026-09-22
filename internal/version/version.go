// Package version holds the build-time version string.
package version

import "runtime"

// Version is set at build time via -ldflags "-X .../internal/version.Version=<v>".
// It defaults to "dev" for untagged builds.
var Version = "dev"

// Info is the machine-readable shape of `backstory version --json`.
type Info struct {
	Version string `json:"version"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Get returns the current build and runtime version info.
func Get() Info {
	return Info{
		Version: Version,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
}
