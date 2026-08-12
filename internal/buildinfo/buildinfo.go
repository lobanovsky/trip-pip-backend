// Package buildinfo exposes build metadata stamped into the binary at link time.
package buildinfo

// Commit identifies the source commit the binary was built from. Release builds
// override it via -ldflags -X; local builds keep the default.
var Commit = "dev"
