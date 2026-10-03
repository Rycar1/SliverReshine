package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// plainRPCRequest stands in for a non-protobuf request body. handleRPCCall
// reaches decodeRPCRequest with whatever request type the method declares, so
// the encoding/json fallback has to keep working.
type plainRPCRequest struct {
	Name string `json:"name"`
}

// ---------------------------------------------------------------------------
// decodeRPCRequest
// ---------------------------------------------------------------------------

func TestDecodeRPCRequestEmptyBodyBuildsAnEmptyMessage(t *testing.T) {
	typ := reflect.TypeOf((*commonpb.Empty)(nil))
	for _, raw := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("null")} {
		v, err := decodeRPCRequest(typ, raw)
		if err != nil {
			t.Fatalf("decodeRPCRequest(%q): %v", string(raw), err)
		}
		if v.Kind() != reflect.Ptr || v.IsNil() {
			t.Fatalf("decodeRPCRequest(%q) returned %v, want a non-nil pointer", string(raw), v)
		}
		if _, ok := v.Interface().(*commonpb.Empty); !ok {
			t.Fatalf("decodeRPCRequest(%q) returned %T, want *commonpb.Empty", string(raw), v.Interface())
		}
	}
}

// An absent body is the normal shape of the Get* calls; it must not be an error
// for a message that is not commonpb.Empty.
func TestDecodeRPCRequestEmptyBodyIsFineForAnyMessage(t *testing.T) {
	v, err := decodeRPCRequest(reflect.TypeOf((*sliverpb.ShellReq)(nil)), nil)
	if err != nil {
		t.Fatalf("decodeRPCRequest(nil): %v", err)
	}
	req, ok := v.Interface().(*sliverpb.ShellReq)
	if !ok {
		t.Fatalf("got %T, want *sliverpb.ShellReq", v.Interface())
	}
	if req.GetPath() != "" || req.GetRequest() != nil {
		t.Errorf("empty body produced a populated message: %v", req)
	}
}

func TestDecodeRPCRequestDecodesProtojsonAndDiscardsUnknownFields(t *testing.T) {
	raw := json.RawMessage(`{"Path":"/bin/sh","Request":{"SessionID":"s1"},"NoSuchField":true}`)
	v, err := decodeRPCRequest(reflect.TypeOf((*sliverpb.ShellReq)(nil)), raw)
	if err != nil {
		t.Fatalf("decodeRPCRequest: %v", err)
	}
	req, ok := v.Interface().(*sliverpb.ShellReq)
	if !ok {
		t.Fatalf("got %T, want *sliverpb.ShellReq", v.Interface())
	}
	if req.GetPath() != "/bin/sh" {
		t.Errorf("Path = %q, want %q", req.GetPath(), "/bin/sh")
	}
	if req.GetRequest().GetSessionID() != "s1" {
		t.Errorf("Request.SessionID = %q, want %q", req.GetRequest().GetSessionID(), "s1")
	}
}

func TestDecodeRPCRequestRejectsMalformedBody(t *testing.T) {
	_, err := decodeRPCRequest(reflect.TypeOf((*sliverpb.ShellReq)(nil)), json.RawMessage(`{"Path":`))
	if err == nil {
		t.Fatal("decodeRPCRequest accepted truncated JSON")
	}
	if !strings.Contains(err.Error(), "decode ShellReq") {
		t.Errorf("error %q does not name the request type", err)
	}
}

func TestDecodeRPCRequestRejectsATypeMismatch(t *testing.T) {
	if _, err := decodeRPCRequest(reflect.TypeOf((*sliverpb.ShellReq)(nil)), json.RawMessage(`{"Path":123}`)); err == nil {
		t.Fatal("decodeRPCRequest accepted a number for a string field")
	}
}

func TestDecodeRPCRequestFallsBackToJSONForNonProtobuf(t *testing.T) {
	typ := reflect.TypeOf((*plainRPCRequest)(nil))

	v, err := decodeRPCRequest(typ, json.RawMessage(`{"name":"x"}`))
	if err != nil {
		t.Fatalf("decodeRPCRequest: %v", err)
	}
	got, ok := v.Interface().(*plainRPCRequest)
	if !ok {
		t.Fatalf("got %T, want *plainRPCRequest", v.Interface())
	}
	if got.Name != "x" {
		t.Errorf("Name = %q, want %q", got.Name, "x")
	}

	if _, err := decodeRPCRequest(typ, json.RawMessage(`{`)); err == nil {
		t.Fatal("decodeRPCRequest accepted malformed JSON for a non-protobuf request")
	}
}

// ---------------------------------------------------------------------------
// marshalProtoJSON
// ---------------------------------------------------------------------------

func TestMarshalProtoJSONPassesNilThrough(t *testing.T) {
	got, err := marshalProtoJSON(nil)
	if err != nil {
		t.Fatalf("marshalProtoJSON(nil): %v", err)
	}
	if got != nil {
		t.Errorf("marshalProtoJSON(nil) = %v, want nil", got)
	}
}

func TestMarshalProtoJSONUsesProtojsonFieldNames(t *testing.T) {
	out, err := marshalProtoJSON(&sliverpb.ShellReq{Path: "/bin/sh"})
	if err != nil {
		t.Fatalf("marshalProtoJSON: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", out)
	}
	if m["Path"] != "/bin/sh" {
		t.Errorf("Path = %v, want %q", m["Path"], "/bin/sh")
	}
	// EmitUnpopulated is off: an unset field must not appear in the payload.
	if _, present := m["Request"]; present {
		t.Errorf("unpopulated field leaked into the payload: %v", m)
	}
}

func TestMarshalProtoJSONFallsBackToEncodingJSON(t *testing.T) {
	type payload struct {
		A int `json:"a"`
	}
	out, err := marshalProtoJSON(payload{A: 1})
	if err != nil {
		t.Fatalf("marshalProtoJSON: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", out)
	}
	if m["a"] != float64(1) {
		t.Errorf("a = %v (%T), want 1", m["a"], m["a"])
	}
}

func TestMarshalProtoJSONReportsAnUnencodableValue(t *testing.T) {
	if _, err := marshalProtoJSON(make(chan int)); err == nil {
		t.Fatal("marshalProtoJSON encoded a channel without error")
	}
}

// ---------------------------------------------------------------------------
// the enumeration helpers the method list and grouping depend on
// ---------------------------------------------------------------------------

func TestEnumeratedRPCMethodsAreSortedAndLabelled(t *testing.T) {
	if len(rpcMethods) == 0 {
		t.Fatal("no RPC methods were enumerated")
	}
	if len(methodByName) != len(rpcMethods) {
		t.Errorf("methodByName has %d entries for %d methods", len(methodByName), len(rpcMethods))
	}
	for i, m := range rpcMethods {
		if i > 0 && rpcMethods[i-1].Name >= m.Name {
			t.Errorf("methods are not sorted: %q before %q", rpcMethods[i-1].Name, m.Name)
		}
		if m.InputType == "" {
			t.Errorf("%s has no input type", m.Name)
		}
		if m.OutputType == "" {
			t.Errorf("%s has no output type", m.Name)
		}
		if m.Group == "" {
			t.Errorf("%s has no group", m.Name)
		}
		if got, ok := methodByName[m.Name]; !ok || got.Name != m.Name {
			t.Errorf("methodByName[%q] = %+v, ok = %v", m.Name, got, ok)
		}
	}
	if m, ok := methodByName["GetSessions"]; !ok {
		t.Error("GetSessions is missing from the enumerated methods")
	} else if m.Streaming {
		t.Error("GetSessions was reported as streaming")
	}
}

func TestMethodGroupBucketsByVerb(t *testing.T) {
	cases := map[string]string{
		"GetSessions":         "Read",
		"ListLoots":           "Read",
		"StartMTLSListener":   "Control",
		"StopSocks":           "Control",
		"RenameSession":       "Modify",
		"UpdateImplantConfig": "Modify",
		"RemoveCanary":        "Destructive",
		"DeleteLoot":          "Destructive",
		"KillJob":             "Destructive",
		"ExecuteAssembly":     "Execute",
		"RunAs":               "Execute",
		"Shell":               "Other",
		"Generate":            "Other",
		"":                    "Other",
	}
	for name, want := range cases {
		if got := methodGroup(name); got != want {
			t.Errorf("methodGroup(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestTypeLabelUnwrapsPointersAndSlices(t *testing.T) {
	if got := typeLabel(reflect.TypeOf((*sliverpb.ShellReq)(nil))); got != "ShellReq" {
		t.Errorf("typeLabel(*ShellReq) = %q, want %q", got, "ShellReq")
	}
	if got := typeLabel(reflect.TypeOf([]*sliverpb.ShellReq{})); got != "ShellReq" {
		t.Errorf("typeLabel([]*ShellReq) = %q, want %q", got, "ShellReq")
	}
	if got := typeLabel(reflect.TypeOf(0)); got != "int" {
		t.Errorf("typeLabel(int) = %q, want %q", got, "int")
	}
	// An unnamed type has no Name(); the label must still be usable.
	if got := typeLabel(reflect.TypeOf(struct{}{})); got != "struct {}" {
		t.Errorf("typeLabel(struct{}) = %q, want %q", got, "struct {}")
	}
}

func TestIsErrorTypeRecognisesError(t *testing.T) {
	if !isErrorType(reflect.TypeOf((*error)(nil)).Elem()) {
		t.Error("isErrorType(error) = false, want true")
	}
	if isErrorType(reflect.TypeOf((*sliverpb.ShellReq)(nil))) {
		t.Error("isErrorType(*ShellReq) = true, want false")
	}
	if isErrorType(reflect.TypeOf(0)) {
		t.Error("isErrorType(int) = true, want false")
	}
}
