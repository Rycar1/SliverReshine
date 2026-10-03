package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"c2tool/internal/ui/sliver"
)

// guardsReachTheServer proves the class of bug the guard exists for, using the
// real descriptors rather than a hand-written assumption.
//
// /api/rpc/call decodes an arbitrary body into whichever request type a method
// expects. protojson leaves a nested message nil when the key is absent or null,
// and several sliver-server handlers dereference that nested message before
// checking it -- Generate reads req.Config.ID first. The server runs as a child
// process whose panic handler calls os.Exit(99), so the panic takes the whole C2
// down rather than failing the request.
func TestGuardsReachTheServer(t *testing.T) {
	// Every guarded method must exist on the RPC surface the console exposes,
	// and the guarded field must exist on the request message it names. A stale
	// guard would silently stop protecting anything.
	for _, g := range nilNestedGuards {
		t.Run(g.Method+"."+g.Field, func(t *testing.T) {
			m, known := methodByName[g.Method]
			if !known {
				t.Fatalf("%s is not a method the console exposes; the guard protects nothing",
					g.Method)
			}
			_ = m

			// Probe with the concrete type the console would build, so a
			// descriptor change breaks this test instead of the guard.
			var msg proto.Message
			switch g.Method {
			case "Generate":
				msg = &clientpb.GenerateReq{}
			case "SaveHTTPC2Profile":
				msg = &clientpb.HTTPC2ConfigReq{}
			default:
				t.Skipf("no probe type for %s; add one when adding a guard", g.Method)
			}

			fd := msg.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(g.Field))
			if fd == nil {
				t.Fatalf("field %q is not on %T; the guard is stale",
					g.Field, msg)
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

	var msg proto.Message
	switch method {
	case "Generate":
		msg = &clientpb.GenerateReq{}
	case "SaveHTTPC2Profile":
		msg = &clientpb.HTTPC2ConfigReq{}
	default:
		t.Fatalf("no probe type for %s", method)
	}

	// Exactly what handleRPCCall does: protojson, DiscardUnknown, no strictness.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(body), msg); err != nil {
		t.Fatalf("decode %s: %v", method, err)
	}
	return msg
}

// generateSpy records whether Generate was reached.
//
// The embedded interface is left nil so any other method panics, which makes an
// unexpected call visible rather than silently returning zero values.
type generateSpy struct {
	rpcpb.SliverRPCClient
	called bool
}

func (g *generateSpy) Generate(_ context.Context, _ *clientpb.GenerateReq, _ ...grpc.CallOption) (*clientpb.Generate, error) {
	g.called = true
	return nil, status.Error(codes.Unavailable, "the guard should have refused before this")
}

// The endpoint itself must refuse, not just the helper. This drives
// handleRPCCall through a real request, so a missing call to the guard fails here
// rather than on an operator's console.
func TestRawRPCCallRefusesNilNestedBeforeInvoking(t *testing.T) {
	spy := &generateSpy{}
	s := New()
	s.SetClient(&sliver.Client{RPC: spy})

	body, _ := json.Marshal(map[string]any{
		"method":  "Generate",
		"request": map[string]any{"Name": "probe"}, // Config omitted
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/call", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	s.handleRPCCall(rec, req)

	if spy.called {
		t.Fatal("the guarded request reached the real RPC. That is the whole bug: " +
			"Generate dereferences req.Config.ID on the server, and the server's " +
			"panic handler calls os.Exit(99), taking every session down")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Config") {
		t.Errorf("the refusal does not name the missing field: %q", rec.Body.String())
	}
}

// A valid request must still reach the RPC, or the guard has broken the endpoint.
func TestRawRPCCallAllowsValidRequestsThrough(t *testing.T) {
	spy := &generateSpy{}
	s := New()
	s.SetClient(&sliver.Client{RPC: spy})

	body, _ := json.Marshal(map[string]any{
		"method":  "Generate",
		"request": map[string]any{"Name": "probe", "Config": map[string]any{"Name": "c"}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/call", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	s.handleRPCCall(rec, req)

	if !spy.called {
		t.Errorf("a valid request never reached the RPC (status %d, body %q); the "+
			"guard is too strict", rec.Code, rec.Body.String())
	}
}
