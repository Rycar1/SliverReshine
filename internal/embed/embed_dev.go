//go:build !embedserver

// Package embed exposes the Sliver server binary to the launcher.
//
// Development builds carry no payload: the launcher falls back to a
// sliver-server binary found next to the executable or on PATH. Release builds
// add the `embedserver` tag, which compiles embed_release.go instead and bakes
// the gzip-compressed server into the single binary.
package embed

// Payload is the gzip-compressed Sliver server binary. It is nil for
// development builds.
var Payload []byte

// Compressed reports whether Payload holds a gzip stream.
const Compressed = true

// Embedded lists the platform keys this binary carries. A development build
// carries none, which is what makes the launcher fall back to an external
// sliver-server instead of failing.
var Embedded []string
