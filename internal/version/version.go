// Package version holds the takos build version.
package version

// Version is overridden at release time via
// -ldflags "-X github.com/busovikov/takos/internal/version.Version=…".
var Version = "0.0.1-dev"
