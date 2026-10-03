package api

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"c2tool/internal/ui/sliver"
)

// guardsReachTheServer proves the class of bug the guard exists for, using the
// real descriptors rather than a hand-written assumption.
//
// /api/rpc/call decodes an arbitrary body into whichever request type a method
// expects. protojson leaves a nested message nil when the key is absent or null,
// and several sliver-server handlers dereference that nested message before
// checking it -- Shell reads req.Request.SessionID first, Generate reads
// req.Config.ID first. The server runs as a child process whose panic handler
// calls os.Exit(99), so the panic takes the whole C2 down rather than failing
// the request.
func TestGuardsReachTheServer(t *testing.T) {
	// Every guarded method must exist on the RPC surface the console exposes,
	// and the guarded field must exist on the request message it names. A stale
	// guard would silently stop protecting anything.
	for _, g := range nilNestedGuards {
		t.Run(g.Method+"."+g.Field, func(t *testing.T) {
			if _, known := methodByName[g.Method]; !known {
				t.Fatalf("%s is not a method the console exposes; the guard protects nothing",
					g.Method)
			}

			// Probe with the concrete type the console would build, so a
			// descriptor change breaks this test instead of the guard.
			msg := rpcRequest(t, g.Method)
			fd := msg.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(g.Field))
			if fd == nil {
				t.Fatalf("field %q is not on %T; the guard is stale", g.Field, msg)
			}
			if fd.Kind() != protoreflect.MessageKind {
				t.Fatalf("field %q on %T is not a message", g.Field, msg)
			}
			if g.Why == "" {
				t.Errorf("guard %s.%s has no explanation; the next reader cannot judge it",
					g.Method, g.Field)
			}
		})
	}
}

// The guard must refuse the exact bodies that would crash the server.
func TestCheckNilNestedFieldsRefusesCrashingRequests(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   string
	}{
		{"Generate with config omitted", "Generate", `{"Name":"probe"}`},
		{"Generate with config null", "Generate", `{"Name":"probe","Config":null}`},
		{"SaveHTTPC2Profile with c2Config omitted", "SaveHTTPC2Profile", `{"Overwrite":false}`},
		{"SaveHTTPC2Profile with c2Config null", "SaveHTTPC2Profile", `{"C2Config":null}`},
		{"Shell with request omitted", "Shell", `{"Path":"/bin/sh"}`},
		{"Shell with request null", "Shell", `{"Request":null}`},
		{"ShellResize with request omitted", "ShellResize", `{"Rows":24}`},
		{"Portfwd with request omitted", "Portfwd", `{"RemoteAddress":"1.2.3.4:80"}`},
		{"Reconfigure with request omitted", "Reconfigure", `{}`},
		{"CloseSession with request omitted", "CloseSession", `{}`},
		{"HijackDLL with request omitted", "HijackDLL", `{"ReferenceDLL":"eA=="}`},
		{"Backdoor with request omitted", "Backdoor", `{"Name":"probe"}`},
		{"GetPrivs with request omitted", "GetPrivs", `{}`},
		{"Msf with request omitted", "Msf", `{"Payload":"x"}`},
		{"MsfRemote with request omitted", "MsfRemote", `{"Payload":"x"}`},
		{"Migrate with request omitted", "Migrate", `{"Pid":1234}`},
		{"ExecuteAssembly with request omitted", "ExecuteAssembly", `{"Assembly":"eA=="}`},
		{"Sideload with request omitted", "Sideload", `{"Data":"eA=="}`},
		{"SpawnDll with request omitted", "SpawnDll", `{"Data":"eA=="}`},
		{"GetSystem with request omitted", "GetSystem", `{"Name":"probe"}`},
		{"GetSystem with config omitted", "GetSystem", `{"Request":{"SessionID":"s"}}`},
		{"Migrate with config omitted", "Migrate", `{"Request":{"SessionID":"s"}}`},
		{"TrafficEncoderAdd with wasm omitted", "TrafficEncoderAdd", `{"ID":1}`},
		{"TrafficEncoderAdd with wasm null", "TrafficEncoderAdd", `{"Wasm":null}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := decodeForGuardTest(t, tc.method, tc.body)
			err := checkNilNestedFields(tc.method, msg)
			if err == nil {
				t.Fatalf("%s accepted %s, which panics the embedded server "+
					"(os.Exit(99) via the daemon panic handler)", tc.method, tc.body)
			}
			if !strings.Contains(err.Error(), "requires") {
				t.Errorf("rejected for an unclear reason: %v", err)
			}
		})
	}
}

// A well-formed request must still go through, or the guard breaks the endpoint.
func TestCheckNilNestedFieldsAllowsValidRequests(t *testing.T) {
	valid := []struct {
		name   string
		method string
		body   string
	}{
		{
			name:   "Generate with a config",
			method: "Generate",
			body:   `{"Name":"probe","Config":{"Name":"c","GOOS":"windows","GOARCH":"amd64"}}`,
		},
		{
			name:   "SaveHTTPC2Profile with both nested configs",
			method: "SaveHTTPC2Profile",
			body:   `{"Overwrite":false,"C2Config":{"Name":"p","ServerConfig":{"Cookies":[{"Name":"c","Value":"v"}]},"ImplantConfig":{}}}`,
		},
		{
			name:   "Shell with a request",
			method: "Shell",
			body:   `{"Path":"/bin/sh","Request":{"SessionID":"s"}}`,
		},
		{
			name:   "Reconfigure with a request",
			method: "Reconfigure",
			body:   `{"Request":{"SessionID":"s"}}`,
		},
		{
			name:   "GetSystem with request and config",
			method: "GetSystem",
			body:   `{"Request":{"SessionID":"s"},"Config":{"Name":"c"}}`,
		},
		{
			name:   "TrafficEncoderAdd with a wasm file",
			method: "TrafficEncoderAdd",
			body:   `{"Wasm":{"Name":"e.wasm","Data":"AA=="}}`,
		},
	}

	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			msg := decodeForGuardTest(t, tc.method, tc.body)
			if err := checkNilNestedFields(tc.method, msg); err != nil {
				t.Errorf("the guard rejected a valid request: %v", err)
			}
		})
	}
}

// An unguarded method must be unaffected.
func TestCheckNilNestedFieldsIgnoresUnguardedMethods(t *testing.T) {
	if err := checkNilNestedFields("GetVersion", &clientpb.GenerateReq{}); err != nil {
		t.Errorf("an unguarded method was refused: %v", err)
	}
	if err := checkNilNestedFields("GetVersion", nil); err != nil {
		t.Errorf("an unguarded method with no body was refused: %v", err)
	}
}

// decodeForGuardTest builds the request message the way handleRPCCall would:
// through the generated client type and protojson with DiscardUnknown.
func decodeForGuardTest(t *testing.T, method, body string) proto.Message {
	t.Helper()

	msg := rpcRequest(t, method)
	// Exactly what handleRPCCall does: protojson, DiscardUnknown, no strictness.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(body), msg); err != nil {
		t.Fatalf("decode %s: %v", method, err)
	}
	return msg
}

// rpcRequestTypes maps every SliverRPC method to the type of the request
// message the console would build for it. Deriving it from the interface keeps
// the tests in step with the descriptors instead of a hand-written list.
func rpcRequestTypes(t *testing.T) map[string]reflect.Type {
	t.Helper()
	iface := reflect.TypeOf((*rpcpb.SliverRPCClient)(nil)).Elem()
	out := make(map[string]reflect.Type, iface.NumMethod())
	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
		if m.Type.NumIn() < 2 {
			continue
		}
		in := m.Type.In(1)
		if in.Kind() != reflect.Ptr {
			continue
		}
		out[m.Name] = in
	}
	return out
}

// rpcRequest returns a fresh request message for method, or fails the test.
func rpcRequest(t *testing.T, method string) proto.Message {
	t.Helper()
	typ, ok := rpcRequestTypes(t)[method]
	if !ok {
		t.Fatalf("no request type for %s", method)
	}
	msg, ok := reflect.New(typ.Elem()).Interface().(proto.Message)
	if !ok {
		t.Fatalf("%s does not take a protobuf request", method)
	}
	return msg
}

// guardSpy records whether a guarded method was reached.
//
// The embedded interface is left nil so any other method panics, which makes an
// unexpected call visible rather than silently returning zero values.
type guardSpy struct {
	rpcpb.SliverRPCClient
	called map[string]bool
}

func newGuardSpy() *guardSpy {
	return &guardSpy{called: map[string]bool{}}
}

func (g *guardSpy) note(method string) {
	g.called[method] = true
}

func (g *guardSpy) Generate(_ context.Context, _ *clientpb.GenerateReq, _ ...grpc.CallOption) (*clientpb.Generate, error) {
	g.note("Generate")
	return nil, status.Error(codes.Unavailable, "the guard should have refused before this")
}

func (g *guardSpy) Shell(_ context.Context, _ *sliverpb.ShellReq, _ ...grpc.CallOption) (*sliverpb.Shell, error) {
	g.note("Shell")
	return nil, status.Error(codes.Unavailable, "the guard should have refused before this")
}

func (g *guardSpy) Reconfigure(_ context.Context, _ *sliverpb.ReconfigureReq, _ ...grpc.CallOption) (*sliverpb.Reconfigure, error) {
	g.note("Reconfigure")
	return nil, status.Error(codes.Unavailable, "the guard should have refused before this")
}

func (g *guardSpy) TrafficEncoderAdd(_ context.Context, _ *clientpb.TrafficEncoder, _ ...grpc.CallOption) (*clientpb.TrafficEncoderTests, error) {
	g.note("TrafficEncoderAdd")
	return nil, status.Error(codes.Unavailable, "the guard should have refused before this")
}

// The endpoint itself must refuse, not just the helper. This drives
// handleRPCCall through a real request, so a missing call to the guard fails here
// rather than on an operator's console.
func TestRawRPCCallRefusesNilNestedBeforeInvoking(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		request map[string]any
	}{
		{"Generate without a config", "Generate", map[string]any{"Name": "probe"}},
		{"Shell without a request", "Shell", map[string]any{"Path": "/bin/sh"}},
		{"Reconfigure without a request", "Reconfigure", map[string]any{}},
		{"TrafficEncoderAdd without a wasm file", "TrafficEncoderAdd", map[string]any{"ID": 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := newGuardSpy()
			s := New()
			s.SetClient(&sliver.Client{RPC: spy})

			body, _ := json.Marshal(map[string]any{"method": tc.method, "request": tc.request})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/rpc/call", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			s.withClient(s.handleRPCCall)(rec, req)

			if spy.called[tc.method] {
				t.Fatalf("the guarded %s request reached the real RPC. That is the whole bug: "+
					"the server dereferences the nested message and its panic handler calls "+
					"os.Exit(99), taking every session down", tc.method)
			}
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "requires") {
				t.Errorf("the refusal does not say what is missing: %q", rec.Body.String())
			}
		})
	}
}

// A valid request must still reach the RPC, or the guard has broken the endpoint.
func TestRawRPCCallAllowsValidRequestsThrough(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		request map[string]any
	}{
		{"Generate with a config", "Generate", map[string]any{"Name": "probe", "Config": map[string]any{"Name": "c"}}},
		{"Shell with a request", "Shell", map[string]any{"Path": "/bin/sh", "Request": map[string]any{"SessionID": "s"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := newGuardSpy()
			s := New()
			s.SetClient(&sliver.Client{RPC: spy})

			body, _ := json.Marshal(map[string]any{"method": tc.method, "request": tc.request})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/rpc/call", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			s.withClient(s.handleRPCCall)(rec, req)

			if !spy.called[tc.method] {
				t.Errorf("a valid request never reached the RPC (status %d, body %q); the "+
					"guard is too strict", rec.Code, rec.Body.String())
			}
		})
	}
}

// serverDeref is one `param.Field.` access found in the embedded sliver-server
// source, where param is the handler's request parameter.
type serverDeref struct {
	method string
	field  string
	file   string
	line   int
}

// scanServerDerefs parses every non-test .go file under dir and records the
// two-level field accesses rooted at each *Server handler's request parameter.
func scanServerDerefs(t *testing.T, dir string) []serverDeref {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	var out []serverDeref
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Recv == nil || fd.Body == nil || len(fd.Recv.List) == 0 {
					continue
				}
				recv := fd.Recv.List[0].Type
				if se, ok := recv.(*ast.StarExpr); ok {
					recv = se.X
				}
				if id, ok := recv.(*ast.Ident); !ok || id.Name != "Server" {
					continue
				}
				param := ""
				if fd.Type.Params != nil {
					for _, p := range fd.Type.Params.List {
						for _, n := range p.Names {
							param = n.Name
						}
					}
				}
				if param == "" {
					continue
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					se, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					inner, ok := se.X.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					root, ok := inner.X.(*ast.Ident)
					if !ok || root.Name != param {
						return true
					}
					pos := fset.Position(se.Pos())
					out = append(out, serverDeref{
						method: fd.Name.Name,
						field:  inner.Sel.Name,
						file:   filepath.Base(pos.Filename),
						line:   pos.Line,
					})
					return true
				})
			}
		}
	}
	return out
}

// nilSafeServerDerefs documents the dereferences the scan finds that cannot
// panic. Every entry carries a reason: an unexplained allowlist entry is how the
// next crash gets missed.
var nilSafeServerDerefs = map[string]string{
	"StartRportFwdListener.Request":  "read only after GenericHandler returned, and GenericHandler rejects a nil Request",
	"StopRportFwdListener.Request":   "read only after GenericHandler returned, and GenericHandler rejects a nil Request",
	"GenerateExternalSaveBuild.File": "guarded by req.GetFile() == nil before any use",
	"TrafficEncoderRm.Wasm":          "guarded by req.Wasm == nil before any use",
}

// TestGuardCoversEveryUnguardedServerDeref scans the embedded server for
// handlers that dereference a nested request message without checking it, and
// fails when one is not covered by nilNestedGuards.
//
// This is the regression net for the guard. Without it, upstream adding a new
// `req.Request.X` or `req.Config.X` silently reopens the crash the guard exists
// to prevent.
func TestGuardCoversEveryUnguardedServerDeref(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "sliver", "server", "rpc")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("embedded sliver-server source is not available (%v); cannot audit the guard list", err)
	}

	inputs := rpcRequestTypes(t)
	seen := map[string]bool{}
	for _, d := range scanServerDerefs(t, dir) {
		typ, isRPC := inputs[d.method]
		if !isRPC {
			continue // helper function, not an RPC handler
		}
		if m, ok := methodByName[d.method]; ok && m.Streaming {
			continue // streams are driven by drainStream, not by this guard
		}
		desc, ok := reflect.New(typ.Elem()).Interface().(proto.Message)
		if !ok {
			continue
		}
		fd := desc.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(d.field))
		if fd == nil || fd.Kind() != protoreflect.MessageKind {
			continue // not a nested message; a nil value cannot be dereferenced
		}

		key := d.method + "." + d.field
		if seen[key] {
			continue
		}
		seen[key] = true

		if guardedField(d.method, d.field) {
			continue
		}
		if why, ok := nilSafeServerDerefs[key]; ok && why != "" {
			continue
		}
		t.Errorf("%s:%d: %s dereferences %s without a nil check, and the guard does "+
			"not cover it; add it to nilNestedGuards or document it in nilSafeServerDerefs",
			d.file, d.line, d.method, key)
	}
}

// guardedField reports whether nilNestedGuards covers method.field.
func guardedField(method, field string) bool {
	for _, g := range guardsByMethod[method] {
		if g.Field == field {
			return true
		}
	}
	return false
}
