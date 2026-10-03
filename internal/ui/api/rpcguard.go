package api

import (
	"fmt"

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
// descriptors and the server source (sliver/server/rpc):
//
//	Shell             req.Request.SessionID      Request nil -> panic
//	Generate          req.Config.ID              Config  nil -> panic
//	TrafficEncoderAdd req.Wasm.Data              Wasm    nil -> panic
//	SaveHTTPC2Profile CheckHTTPC2ConfigErrors    C2Config nil -> panic
//
// The last is already checked on the typed path (sliver.SaveHTTPC2Profile), but
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
//
// TestGuardCoversEveryUnguardedServerDeref scans the embedded server source and
// fails when a handler dereferences a nested request message that this list does
// not cover, so a new upstream method cannot silently reopen the crash. When it
// does, add the entry here -- and the fix that belongs upstream is a nil check
// in the handler.

// nilNestedGuard names a request field that must not be nil for a given method.
type nilNestedGuard struct {
	Method string
	// Field is the protobuf field name (as it appears in the descriptor).
	Field string
	// Why explains the crash, for the error message and for whoever maintains
	// this list.
	Why string
}

// requestDerefWhy is shared by every Request guard: the handlers in this class
// all read a field of the nested commonpb.Request before checking that the
// message exists.
const requestDerefWhy = "the handler reads req.Request before checking it, so a " +
	"request without it panics the server"

// requestDerefMethods lists the unary methods whose sliver-server handler
// dereferences the nested Request message without a nil check. Each was read in
// sliver/server/rpc: Backdoor reads req.Request.SessionID, Shell and ShellResize
// read req.Request.SessionID, Migrate/ExecuteAssembly/Sideload/SpawnDll/Msf/
// MsfRemote/GetPrivs read req.Request.Async first, Reconfigure reads
// req.Request.SessionID on its first line, Kill/KillSession/CloseSession/
// HijackDLL/GetSystem/Portfwd read req.Request.SessionID, and so on.
//
// These are all "generic" RPCs in spirit -- most even call rpc.GenericHandler --
// but the dereference happens before that call, so the nil check inside
// GenericHandler is never reached.
var requestDerefMethods = []string{
	"Backdoor",
	"CloseSession",
	"ExecuteAssembly",
	"GetPrivs",
	"GetSystem",
	"HijackDLL",
	"Kill",
	"Migrate",
	"Msf",
	"MsfRemote",
	"Portfwd",
	"Reconfigure",
	"Shell",
	"ShellResize",
	"Sideload",
	"SpawnDll",
}

// explicitNilNestedGuards covers methods that crash on a nil nested message
// other than Request.
var explicitNilNestedGuards = []nilNestedGuard{
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
	{
		Method: "Migrate",
		Field:  "Config",
		Why: "the handler reads req.Config in the branch that regenerates missing " +
			"shellcode, so a request without a config panics the server whenever " +
			"the named shellcode is not cached",
	},
	{
		Method: "GetSystem",
		Field:  "Config",
		Why: "the handler reads req.Config.HTTPC2ConfigName unconditionally, so a " +
			"request without a config panics the server",
	},
	{
		Method: "TrafficEncoderAdd",
		Field:  "Wasm",
		Why: "the handler reads req.Wasm.Data to compute the encoder ID before " +
			"testTrafficEncoder's nil check, so a request without a wasm file " +
			"panics the server",
	},
}

// nilNestedGuards is the set of known-crashing nil nested messages.
//
// Kept as data rather than as code in the call path so the list is one obvious
// place to extend, and so a test can assert that every entry actually exists in
// the descriptor it names.
var nilNestedGuards = buildNilNestedGuards()

func buildNilNestedGuards() []nilNestedGuard {
	out := make([]nilNestedGuard, 0, len(requestDerefMethods)+len(explicitNilNestedGuards))
	for _, method := range requestDerefMethods {
		out = append(out, nilNestedGuard{
			Method: method,
			Field:  "Request",
			Why:    requestDerefWhy,
		})
	}
	return append(out, explicitNilNestedGuards...)
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
