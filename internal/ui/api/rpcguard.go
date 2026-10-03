package api

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// This file guards the raw RPC endpoint against request messages that make the
// embedded sliver-server panic.
//
// # Why a guard is needed at all
//
// /api/rpc/call decodes an arbitrary JSON body into whichever request type a
// method expects and forwards it. Several sliver-server handlers dereference a
// NESTED message without checking it, and protojson happily leaves that nested
// message nil when the key is absent or null. Measured against the real
// descriptors:
//
//	Generate          req.Config.ID              Config nil -> panic
//	SaveHTTPC2Profile CheckHTTPC2ConfigErrors   ServerConfig / ImplantConfig nil -> panic
//
// The second is already checked on the typed path (sliver.SaveHTTPC2Profile), but
// the raw endpoint bypasses that check -- it calls the method directly through
// reflection. So the same input is refused on one route and fatal on the other.
//
// # Why this is fatal rather than a failed request
//
// The server runs as a child process (internal/launch spawns `sliver-server
// daemon`). Its panic handler logs the stack and then calls os.Exit(99), so the
// panic takes down every session, beacon and listener on the engagement. A single
// malformed request from an authenticated operator does it.
//
// # What this guard is and is not
//
// It is a list of the nil-nested-message fields that are known to crash the
// server, checked before the call is made. It is not a general validator, and it
// cannot be: the console cannot know what every upstream handler dereferences.
// The list exists because a wrong guess here is cheap and a panic there is not.
// When upstream adds another one, it belongs here -- and the fix that belongs
// upstream is a nil check in the handler.

// nilNestedGuard names a request field that must not be nil for a given method.
type nilNestedGuard struct {
	Method string
	// Field is the protobuf field name (as it appears in the descriptor).
	Field string
	// Why explains the crash, for the error message and for whoever maintains
	// this list.
	Why string
}

// nilNestedGuards is the set of known-crashing nil nested messages.
//
// Kept as data rather than as code in the call path so the list is one obvious
// place to extend, and so a test can assert that every entry actually exists in
// the descriptor it names.
var nilNestedGuards = []nilNestedGuard{
	{
		Method: "Generate",
		Field:  "Config",
		Why: "sliver-server reads req.Config.ID before checking it, so a request " +
			"without a config panics the server",
	},
	{
		Method: "SaveHTTPC2Profile",
		Field:  "C2Config",
		Why: "CheckHTTPC2ConfigErrors dereferences ServerConfig and ImplantConfig " +
			"unconditionally, so a profile without them panics the server",
	},
}

// guardsByMethod indexes nilNestedGuards for lookup.
var guardsByMethod = func() map[string][]nilNestedGuard {
	m := make(map[string][]nilNestedGuard, len(nilNestedGuards))
	for _, g := range nilNestedGuards {
		m[g.Method] = append(m[g.Method], g)
	}
	return m
}()

// checkNilNestedFields reports an error when a request would make the server
// panic on a nil nested message.
//
// msg is the decoded request; it may be nil, which is itself a case no method
// here accepts.
func checkNilNestedFields(method string, msg proto.Message) error {
	guards := guardsByMethod[method]
	if len(guards) == 0 {
		return nil
	}
	if msg == nil {
		return fmt.Errorf("method %s requires a request body", method)
	}

	fields := msg.ProtoReflect().Descriptor().Fields()
	for _, g := range guards {
		fd := fields.ByName(protoreflect.Name(g.Field))
		if fd == nil {
			// The descriptor moved out from under the guard. Refusing the call
			// would be wrong (the guard is stale, not the request), so it is
			// reported instead -- a silent skip would hide the drift.
			return fmt.Errorf("internal: guard for %s.%s names a field that no longer "+
				"exists; the guard list is stale", method, g.Field)
		}
		if fd.Kind() != protoreflect.MessageKind {
			return fmt.Errorf("internal: guard for %s.%s expects a message field", method, g.Field)
		}
		v := msg.ProtoReflect().Get(fd)
		if !v.Message().IsValid() {
			return fmt.Errorf("%s requires %s: %s", method, g.Field, g.Why)
		}
	}
	return nil
}

// guardNamesForMethod lists the guarded fields, for the method catalogue so the
// UI can show why a field is required.
func guardNamesForMethod(method string) []string {
	guards := guardsByMethod[method]
	if len(guards) == 0 {
		return nil
	}
	out := make([]string, 0, len(guards))
	for _, g := range guards {
		out = append(out, g.Field)
	}
	return out
}

// describeGuards renders the guard list for a log or error line.
func describeGuards() string {
	parts := make([]string, 0, len(nilNestedGuards))
	for _, g := range nilNestedGuards {
		parts = append(parts, g.Method+"."+g.Field)
	}
	return strings.Join(parts, ", ")
}
