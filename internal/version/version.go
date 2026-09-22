// Package version holds the build-time version string.
package version

// Version is set at build time via -ldflags "-X .../internal/version.Version=<v>".
// It defaults to "dev" for untagged builds.
var Version = "dev"
