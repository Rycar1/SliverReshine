package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sliverreshine/internal/ui/sliver"
)

// The raw RPC console exists so the web UI can reach the *entire* SliverRPC
// surface, not just the subset that has a hand-written page. Modelled pages
// cover the everyday workflows; this endpoint covers everything else —
// armory internals, crackstation operations, and any method added upstream
// after this console was written.
//
// Both directions are driven by protojson, so request bodies use the same
// field names as the .proto definitions (snake_case) and responses come back
// as readable JSON rather than base64 soup.

// rpcMethod describes one callable method on SliverRPC.
type rpcMethod struct {
	Name       string `json:"name"`
	Streaming  bool   `json:"streaming"`
	InputType  string `json:"inputType"`
	OutputType string `json:"outputType"`
	Group      string `json:"group"`
}

// rpcMethods is computed once: the SliverRPC interface is fixed at build time.
var rpcMethods = enumerateRPCMethods()

// enumerateRPCMethods walks the generated SliverRPCClient interface and
// records every method, its request/response message types, and whether it is
// server-streaming.
func enumerateRPCMethods() []rpcMethod {
	iface := reflect.TypeOf((*rpcpb.SliverRPCClient)(nil)).Elem()
	out := make([]rpcMethod, 0, iface.NumMethod())

	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)

		// Generated client signatures are always
		//   Method(ctx context.Context, in *X, opts ...grpc.CallOption) (*Y, error)
		// for unary calls and
		//   Method(ctx context.Context, in *X, opts ...grpc.CallOption) (SliverRPC_MethodClient, error)
		// for streaming ones.
		if m.Type.NumIn() < 3 || m.Type.NumOut() < 1 {
			continue
		}
		in := m.Type.In(1)
		if in.Kind() != reflect.Ptr {
			continue
		}
		outType := m.Type.Out(0)

		streaming := !isErrorType(outType) && outType.Kind() != reflect.Ptr
		outName := typeLabel(outType)

		out = append(out, rpcMethod{
			Name:       m.Name,
			Streaming:  streaming,
			InputType:  typeLabel(in),
			OutputType: outName,
			Group:      methodGroup(m.Name),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func isErrorType(t reflect.Type) bool {
	return t.Kind() == reflect.Interface && t.Implements(reflect.TypeOf((*error)(nil)).Elem())
}

// typeLabel renders a reflect.Type as a short protobuf message name.
func typeLabel(t reflect.Type) string {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	name := t.Name()
	if name == "" {
		return t.String()
	}
	return name
}

// methodGroup buckets methods by their leading verb so the UI can group them.
func methodGroup(name string) string {
	switch {
	case strings.HasPrefix(name, "Get") || strings.HasPrefix(name, "List"):
		return "Read"
	case strings.HasPrefix(name, "Start") || strings.HasPrefix(name, "Stop"):
		return "Control"
	case strings.HasPrefix(name, "Rename") || strings.HasPrefix(name, "Update"):
		return "Modify"
	case strings.HasPrefix(name, "Remove") || strings.HasPrefix(name, "Delete") || strings.HasPrefix(name, "Kill"):
		return "Destructive"
	case strings.HasPrefix(name, "Execute") || strings.HasPrefix(name, "Run"):
		return "Execute"
	default:
		return "Other"
	}
}

// methodByName indexes the enumerated methods for lookup.
var methodByName = func() map[string]rpcMethod {
	m := make(map[string]rpcMethod, len(rpcMethods))
	for _, method := range rpcMethods {
		m[method.Name] = method
	}
	return m
}()

// handleRPCMethods lists every callable method.
func (s *Server) handleRPCMethods(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"methods": rpcMethods,
		"count":   len(rpcMethods),
	})
}

// handleRPCCall invokes one method with a protojson request body.
//
// Request:
//
//	{"method":"GetVersion","request":{...},"timeout":30}
//
// Response (unary):
//
//	{"ok":true,"result":{...}}
//
// Response (streaming):
//
//	{"ok":true,"messages":[{...},...],"count":N,"truncated":bool}
func (s *Server) handleRPCCall(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method  string          `json:"method"`
		Request json.RawMessage `json:"request"`
		Timeout int             `json:"timeout"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Method == "" {
		writeErr(w, http.StatusBadRequest, "method is required")
		return
	}
	method, known := methodByName[req.Method]
	if !known {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("unknown RPC method %q", req.Method))
		return
	}

	// The timeout is clamped. It was taken from the request body verbatim, so a
	// caller could ask for a year and the handler would hold a context, a
	// goroutine and an open response for that long -- and the HTTP server has no
	// write timeout, so nothing else would cut it short either.
	timeout := clampRPCTimeout(req.Timeout)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	call := reflect.ValueOf(c.RPC).MethodByName(req.Method)
	if !call.IsValid() {
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("method %q not present on the live client", req.Method))
		return
	}

	in, err := decodeRPCRequest(call.Type().In(1), req.Request)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Refuse a request that would make the embedded server panic. The typed
	// routes check the same shapes, but this endpoint calls the method directly
	// and bypasses those checks, so the same input is rejected on one route and
	// fatal on the other. See rpcguard.go.
	if msg, ok := in.Interface().(proto.Message); ok {
		if err := checkNilNestedFields(method.Name, msg); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	results := call.Call([]reflect.Value{reflect.ValueOf(ctx), in})
	if len(results) < 2 {
		writeErr(w, http.StatusInternalServerError, "unexpected RPC signature")
		return
	}
	if errVal := results[len(results)-1]; !errVal.IsNil() {
		writeErr(w, http.StatusBadGateway, errVal.Interface().(error).Error())
		return
	}
	resp := results[0]

	if !method.Streaming {
		writeUnaryRPCResult(w, resp)
		return
	}
	writeStreamRPCResult(w, ctx, resp)
}

// writeUnaryRPCResult answers a non-streaming RPC with its marshalled response.
func writeUnaryRPCResult(w http.ResponseWriter, resp reflect.Value) {
	out, err := marshalProtoJSON(resp.Interface())
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": out})
}

// writeStreamRPCResult drains a streaming RPC and answers with every message it
// produced.
//
// A failure after some messages arrived is reported as a failure, with the
// partial list attached. The operator learns the stream was cut short and still
// gets what did arrive -- more useful than either discarding it or answering
// 200 as though the stream had ended on its own.
func writeStreamRPCResult(w http.ResponseWriter, ctx context.Context, resp reflect.Value) {
	messages, truncated, err := drainStream(ctx, resp)
	if err != nil {
		var partial *streamFailure
		if errors.As(err, &partial) && len(partial.messages) > 0 {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"ok":       false,
				"error":    partial.Error(),
				"messages": partial.messages,
				"count":    len(partial.messages),
				"partial":  true,
			})
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"messages":  messages,
		"count":     len(messages),
		"truncated": truncated,
	})
}

// streamFailure carries the messages received before a streaming call failed.
//
// It exists so a partial read is reported as a partial read. Returning a bare
// error would discard what did arrive; returning nil -- which is what the code
// did -- claimed the stream ended normally.
type streamFailure struct {
	messages []any
	cause    error
}

func (e *streamFailure) Error() string { return e.cause.Error() }
func (e *streamFailure) Unwrap() error { return e.cause }

// RPC timeouts. A request may ask for a shorter one; the cap stops it asking for
// an arbitrarily long one, which would pin a context, a goroutine and an open
// response for that whole time.
const (
	defaultRPCTimeout = 60 * time.Second
	maxRPCTimeout     = 10 * time.Minute
)

// clampRPCTimeout turns a request's timeout field into the duration the handler
// will honour.
//
// The field is caller-controlled, so it cannot be trusted: without the cap a
// request asking for a year would hold a context, a goroutine and an open
// response for a year, and nothing downstream would cut it short either, since
// the HTTP server has no write timeout. A non-positive value means "use the
// default" rather than "no timeout", so 0 and negative numbers cannot opt out
// of the cap.
//
// The comparison happens in seconds before the multiplication on purpose: a
// value like math.MaxInt would overflow time.Duration and wrap negative, which
// context.WithTimeout reads as an already-expired deadline.
func clampRPCTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultRPCTimeout
	}
	if int64(seconds) >= int64(maxRPCTimeout/time.Second) {
		return maxRPCTimeout
	}
	return time.Duration(seconds) * time.Second
}

// maxStreamMessages bounds how much a streaming call can buffer. Several
// streams (events, beacons, the loot feed) never terminate on their own, so the
// console drains a window and reports truncation instead of hanging forever.
const maxStreamMessages = 200

// drainStream reads from a generated streaming client until the stream ends,
// the context expires, or the message cap is hit.
func drainStream(ctx context.Context, stream reflect.Value) ([]any, bool, error) {
	recv := stream.MethodByName("Recv")
	if !recv.IsValid() {
		return nil, false, fmt.Errorf("streaming client has no Recv method")
	}

	messages := make([]any, 0, 16)
	for len(messages) < maxStreamMessages {
		if ctx.Err() != nil {
			return messages, len(messages) > 0, nil
		}
		out := recv.Call(nil)
		if len(out) < 2 {
			return messages, false, fmt.Errorf("unexpected Recv signature")
		}
		if errVal := out[1]; !errVal.IsNil() {
			cause := errVal.Interface().(error)
			// io.EOF is the normal end of a finite stream, so it is a clean stop.
			// The string comparison is kept as a fallback because a wrapped EOF is
			// still EOF, and errors.Is covers the wrapped cases.
			if errors.Is(cause, io.EOF) || cause.Error() == "EOF" {
				return messages, false, nil
			}
			// A deadline or cancellation is this console's own doing, so what was
			// received is a truncation rather than a failure: several streams
			// (events, beacons, loot) never end on their own, and the timeout is
			// the documented way to read a window of them.
			if ctx.Err() != nil || errors.Is(cause, context.DeadlineExceeded) ||
				errors.Is(cause, context.Canceled) {
				return messages, len(messages) > 0, nil
			}
			// Anything else is a real failure. The partial messages go with it: this
			// used to return (messages, false, nil), so the handler answered
			// 200 {"ok":true} for a stream that died, and every message that would
			// have followed was silently missing.
			return messages, false, &streamFailure{messages: messages, cause: cause}
		}
		item, err := marshalProtoJSON(out[0].Interface())
		if err != nil {
			return messages, false, err
		}
		messages = append(messages, item)
	}
	return messages, true, nil
}

// decodeRPCRequest turns a protojson document into the concrete request
// message the method expects. An empty body becomes an empty message, which is
// what most Get* calls want.
func decodeRPCRequest(t reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	msg := reflect.New(t.Elem()).Interface()

	if len(raw) == 0 || string(raw) == "null" {
		if _, ok := msg.(*commonpb.Empty); ok {
			return reflect.ValueOf(msg), nil
		}
		return reflect.ValueOf(msg), nil
	}

	protoMsg, ok := msg.(proto.Message)
	if !ok {
		// Fall back to encoding/json for non-protobuf payloads.
		if err := json.Unmarshal(raw, msg); err != nil {
			return reflect.Value{}, fmt.Errorf("decode %s: %w", t.Elem().Name(), err)
		}
		return reflect.ValueOf(msg), nil
	}

	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, protoMsg); err != nil {
		return reflect.Value{}, fmt.Errorf("decode %s: %w", t.Elem().Name(), err)
	}
	return reflect.ValueOf(msg), nil
}

// marshalProtoJSON renders a response message as JSON. Protobuf messages go
// through protojson so field names match the .proto definitions; anything else
// falls back to encoding/json.
func marshalProtoJSON(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if msg, ok := v.(proto.Message); ok {
		data, err := (protojson.MarshalOptions{EmitUnpopulated: false}).Marshal(msg)
		if err != nil {
			return nil, err
		}
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	// Round-trip through JSON so the value is encodable.
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// keep grpc imported for the CallOption variadic handling above.
var _ = grpc.WaitForReady
