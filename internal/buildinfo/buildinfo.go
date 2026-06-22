// Package buildinfo holds build-time metadata. Version is overridden at release
// time via -ldflags "-X github.com/alertd/devproxy/internal/buildinfo.Version=...".
package buildinfo

// Version is the release version, or "dev" for local builds.
var Version = "dev"
