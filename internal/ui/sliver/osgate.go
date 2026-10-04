package sliver

import (
	"fmt"
	"strings"
)

// Windows-only RPCs, and why the console checks the target before sending one.
//
// Sliver's implant registers a handler per message type, and most of the
// Windows-specific ones -- services, the registry, token manipulation,
// process migration, execute-assembly, spawn-dll, GetSystem -- have no handler
// in the Linux implant (the macOS one is missing all of them too, plus it has
// extensions that Linux does not). Sending one of those messages to such a
// target comes back as the implant's own "unknown message type": a sentence
// that names neither the feature nor the target's platform, so an operator
// reads it as a console bug rather than as "this target is the wrong OS".
//
// The console already resolves every session's platform for the execute path
// (sessionOS, see exec.go), so the same fact answers the question before the
// RPC goes out. The gate is deliberately permissive: when the platform cannot
// be resolved the RPC still goes out and the implant's own error is what the
// operator sees. Refusing on an unknown platform would break Windows sessions
// the console merely failed to enumerate.

// requireWindows refuses a Windows-only feature on a target known not to be
// Windows.
func (c *Client) requireWindows(sessionID, feature string) error {
	return c.requirePlatform(sessionID, feature, func(osName string) bool {
		return strings.Contains(osName, platformWindows)
	})
}

// requireWindowsOrDarwin covers the extension RPCs, which the Windows and
// macOS implants both implement and the Linux implant does not.
func (c *Client) requireWindowsOrDarwin(sessionID, feature string) error {
	return c.requirePlatform(sessionID, feature, func(osName string) bool {
		return strings.Contains(osName, platformWindows) || strings.Contains(osName, "darwin")
	})
}

// requirePlatform runs supported against the session's platform and turns a
// negative into a sentence that names the feature and the platform. An
// unresolved platform is not a refusal.
func (c *Client) requirePlatform(sessionID, feature string, supported func(string) bool) error {
	osName, ok := c.sessionOS(sessionID)
	if !ok {
		return nil
	}
	osName = strings.ToLower(strings.TrimSpace(osName))
	if osName == "" || supported(osName) {
		return nil
	}
	return fmt.Errorf("%s is not supported on the target operating system (%s)", feature, osName)
}
