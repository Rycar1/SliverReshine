//go:build embedserver

// Package embed carries the gzip-compressed Sliver server binary for release
// builds. The payloads under internal/embed/serverbin/ are produced by
// build/embed-payload.ps1 from sliver-server binaries built out of the
// BishopFox/sliver v1.7.3 tree.
//
// Payloads are keyed by platform — serverbin/payload-<goos>-<goarch>.gz — and
// the one matching the host is selected at runtime. A single unkeyed
// payload.gz used to be embedded instead, and because the release script built
// the launcher for the target platform but compressed whichever server binary
// it was handed, a Windows launcher could end up carrying the Linux server. The
// failure only surfaced at exec time, as "This version of %1 is not compatible
// with the version of Windows you're running" — which names neither the payload
// nor the platform. Keying the file makes the mismatch impossible to express,
// and embed.Embedded below lets the launcher say exactly what it was built
// with when the host is not covered.
package embed

import (
	"embed"
	"io/fs"
	"runtime"
	"sort"
	"strings"
)

//go:embed serverbin/payload-*.gz
var payloads embed.FS

// Compressed reports whether Payload holds a gzip stream.
const Compressed = true

// payloadPrefix and payloadSuffix frame the platform key inside the embedded
// filename: serverbin/payload-<goos>-<goarch>.gz.
const (
	payloadPrefix = "serverbin/payload-"
	payloadSuffix = ".gz"
)

// Embedded lists the platform keys (goos/goarch) this binary carries, sorted.
//
// It is exported so a launcher running on a platform with no payload can name
// what it does have instead of failing with an opaque exec error.
var Embedded = listPayloads()

// Payload is the gzip-compressed Sliver server binary for the platform this
// launcher was compiled for. It is nil when that platform was not embedded.
var Payload = selectPayload(runtime.GOOS, runtime.GOARCH)

// selectPayload returns the payload for goos/goarch, or nil when absent.
func selectPayload(goos, goarch string) []byte {
	name := payloadPrefix + goos + "-" + goarch + payloadSuffix
	data, err := payloads.ReadFile(name)
	if err != nil {
		return nil
	}
	return data
}

// listPayloads enumerates the embedded platform keys, sorted for stable output.
func listPayloads() []string {
	entries, err := fs.ReadDir(payloads, "serverbin")
	if err != nil {
		return nil
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "payload-") || !strings.HasSuffix(name, payloadSuffix) {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(name, "payload-"), payloadSuffix)
		// The key is <goos>-<goarch>; a bare goos is not a platform key.
		if !strings.Contains(key, "-") {
			continue
		}
		key = strings.Replace(key, "-", "/", 1)
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

