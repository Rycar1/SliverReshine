package api

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// fakeStream stands in for a generated protobuf streaming client: it has only a
// Recv method, which is all drainStream uses.
type fakeStream struct {
	values []any
	errs   []error
	i      int
}

func (f *fakeStream) Recv() (any, error) {
	if f.i >= len(f.values) {
		return nil, errors.New("EOF")
	}
	v, e := f.values[f.i], f.errs[f.i]
	f.i++
	if e != nil {
		return v, e
	}
	return v, nil
}

// drainStream must not report a truncated success when the stream failed.
//
// The code path under test:
//
//	if len(messages) > 0 {
//	    return messages, false, nil   // <-- the error is dropped
//	}
//
// An operator calling a streaming RPC through the raw console therefore sees
// {"ok":true,...,"truncated":false} for a stream that died with a transport or
// permission error. Every message that would have followed is silently missing,
// and the response claims the stream ended normally.
func TestDrainStreamDoesNotSwallowAMidStreamFailure(t *testing.T) {
	stream := &fakeStream{
		values: []any{map[string]any{"First": 1}, nil},
		errs:   []error{nil, errors.New("connection reset by peer")},
	}

	messages, truncated, err := drainStream(context.Background(), reflect.ValueOf(stream))

	if len(messages) != 1 {
		t.Fatalf("messages = %d, want the 1 message received before the failure", len(messages))
	}
	if err == nil {
		t.Fatalf("drainStream returned no error after a mid-stream failure: "+
			"messages=%d truncated=%v -- the caller reports success and the "+
			"operator never learns the stream was cut short", len(messages), truncated)
	}
	if truncated {
		t.Error("truncated = true, want false: this is a failure, not a cap")
	}
}

// A finite stream that ends normally must still be a clean success.
func TestDrainStreamCleanEOFIsSuccess(t *testing.T) {
	stream := &fakeStream{
		values: []any{map[string]any{"A": 1}, map[string]any{"B": 2}},
		errs:   []error{nil, nil},
	}
	messages, truncated, err := drainStream(context.Background(), reflect.ValueOf(stream))
	if err != nil {
		t.Fatalf("a normal end of stream reported an error: %v", err)
	}
	if truncated {
		t.Error("truncated = true for a stream that ended on its own")
	}
	if len(messages) != 2 {
		t.Errorf("messages = %d, want 2", len(messages))
	}
}

// Hitting the message cap is reported as truncation, not as an error.
func TestDrainStreamCapIsTruncation(t *testing.T) {
	values := make([]any, maxStreamMessages+5)
	errs := make([]error, len(values))
	for i := range values {
		values[i] = map[string]any{"N": i}
	}
	stream := &fakeStream{values: values, errs: errs}

	messages, truncated, err := drainStream(context.Background(), reflect.ValueOf(stream))
	if err != nil {
		t.Fatalf("hitting the cap reported an error: %v", err)
	}
	if !truncated {
		t.Error("truncated = false after hitting the cap")
	}
	if len(messages) != maxStreamMessages {
		t.Errorf("messages = %d, want %d", len(messages), maxStreamMessages)
	}
}
