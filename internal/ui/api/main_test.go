package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps this package's tests out of the operator's real sliver-client
// directory.
//
// Starting a listener records the listener's website and callback address in a
// map persisted beside the saved profiles (internal/ui/sliver/listener_sites.go),
// and these tests start listeners against a stubbed RPC. Without the redirect
// below the suite wrote listener-sites.json and listener-hosts.json into the
// developer's own ~/.sliver-client/configs -- and since the profile listing scans
// that same directory, one `go test ./...` also handed
// internal/ui/sliver's TestListProfiles two "profiles" it never created. That
// failed the suite on any machine where the directory was writable, which is why
// the failure showed up in a container and not on CI.
//
// The configs directory is created here so it is the first writable candidate the
// console's jobMapPath probe finds; every reader resolves these variables per
// call, so setting them once covers the whole package.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "sliverreshine-test-home-")
	if err != nil {
		panic(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "configs"), 0o700); err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Setenv("SLIVER_CLIENT_CONFIGS", filepath.Join(home, "configs"))

	code := m.Run()

	os.RemoveAll(home)
	os.Exit(code)
}
