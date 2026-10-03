package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
)

// These tests cover the per-request ceiling and the request-scoped client view
// it feeds: a non-streaming handler must run under a bounded context, and when
// the browser goes away the gRPC call it is waiting on must be cancelled rather
// than left to finish on its own budget.

// blockingStub parks inside GetSessions until the context it was given is done.
// It is the shape of a slow sliver-server call, and the only way to observe
// from a test that a handler's cancellation reaches the RPC.
type blockingStub struct {
	rpcpb.SliverRPCClient
	entered chan struct{}
}

func (s *blockingStub) GetSessions(ctx context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWithRequestCeilingBoundsHandlerContext(t *testing.T) {
	var remaining time.Duration
	h := withRequestCeiling(func(_ http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("the handler context has no deadline")
			return
		}
		remaining = time.Until(deadline)
	})
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/info", nil))

	if remaining <= 0 || remaining > apiRequestCeiling {
		t.Fatalf("handler budget is %v, want up to %v", remaining, apiRequestCeiling)
	}
	if remaining < apiRequestCeiling-time.Minute {
		t.Fatalf("handler budget is %v, want about %v", remaining, apiRequestCeiling)
	}
}

func TestWithRequestCeilingPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var err error
	h := withRequestCeiling(func(_ http.ResponseWriter, r *http.Request) {
		err = r.Context().Err()
	})
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/info", nil).WithContext(ctx))

	if err != context.Canceled {
		t.Fatalf("handler context error = %v, want context.Canceled", err)
	}
}

func TestClientForRequiresConnection(t *testing.T) {
	rec := httptest.NewRecorder()
	if c := New().clientFor(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil)); c != nil {
		t.Fatal("clientFor returned a client while disconnected")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestClientForBindsClientToRequest(t *testing.T) {
	s := serverWithStub(&rpcStub{})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)

	c := s.clientFor(httptest.NewRecorder(), req)
	if c == nil {
		t.Fatal("clientFor returned nil while connected")
	}
	if c == s.Client() {
		t.Fatal("clientFor returned the console-wide client instead of a request-scoped view")
	}
	if c.Profile != s.Client().Profile {
		t.Errorf("view profile = %q, want %q", c.Profile, s.Client().Profile)
	}
}

func TestRequestCancellationStopsInFlightRPC(t *testing.T) {
	stub := &blockingStub{entered: make(chan struct{}, 1)}
	s := serverWithStub(stub)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.Routes().ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	select {
	case <-stub.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler never reached the RPC")
	}

	// The browser goes away. The handler is parked inside the RPC; without the
	// request-scoped view it would sit there until rpcQuick (10s) expired.
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the request did not stop the in-flight RPC")
	}
}
