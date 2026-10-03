package sliver

import (
	"context"
	"testing"
	"time"
)

// WithRequestContext is what lets an HTTP request's cancellation reach the gRPC
// call the handler is waiting on. These tests pin the two properties that make
// that work: a view shares console state with the client it came from, and its
// calls inherit the request's cancellation and remaining budget.

func TestWithRequestContextSharesClientState(t *testing.T) {
	base := &Client{Profile: "operator"}
	reqCtx := context.Background()

	view := base.WithRequestContext(reqCtx)
	if view == base {
		t.Fatal("WithRequestContext returned the client itself, not a view")
	}
	if view.Profile != base.Profile {
		t.Errorf("Profile = %q, want %q", view.Profile, base.Profile)
	}
	if view.reqCtx != reqCtx {
		t.Error("the view is not bound to the request context")
	}
	if view.rootClient() != base {
		t.Error("the view does not point back at the client it came from")
	}
	if base.rootClient() != base {
		t.Error("the console-wide client is not its own root")
	}

	// State recorded through the view must land on the client, so a request
	// does not get a private listener-to-website map.
	view.rememberListenerSite(7, "webdelivery")
	if site, ok := base.listenerSite(7); !ok || site != "webdelivery" {
		t.Fatalf("listener site recorded on the view reads back as %q, %v", site, ok)
	}
	if got := base.WithRequestContext(reqCtx).ExistingPortForwards(); got != nil {
		t.Errorf("a view created its own port-forward manager: %v", got)
	}
	if got := view.ExistingSocks(); got != nil {
		t.Errorf("a view created its own SOCKS manager: %v", got)
	}
}

func TestWithRequestContextNilInputs(t *testing.T) {
	base := &Client{}
	if got := base.WithRequestContext(nil); got != base {
		t.Error("a nil context should leave the client unchanged")
	}
	var nilClient *Client
	if got := nilClient.WithRequestContext(context.Background()); got != nil {
		t.Error("a nil client should stay nil")
	}
}

func TestRPCCtxInheritsRequestCancellation(t *testing.T) {
	reqCtx, cancelRequest := context.WithCancel(context.Background())
	view := (&Client{}).WithRequestContext(reqCtx)

	callCtx, cancelCall := view.rpcCtx(time.Hour)
	defer cancelCall()

	cancelRequest()
	select {
	case <-callCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the request did not cancel the in-flight call")
	}
}

func TestRPCCtxCapsAtRequestDeadline(t *testing.T) {
	reqCtx, cancelRequest := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelRequest()

	callCtx, cancelCall := (&Client{}).WithRequestContext(reqCtx).rpcCtx(time.Hour)
	defer cancelCall()

	deadline, ok := callCtx.Deadline()
	if !ok {
		t.Fatal("the call has no deadline")
	}
	if remaining := time.Until(deadline); remaining > time.Second {
		t.Fatalf("call deadline is %v away, want the request's 50ms budget", remaining)
	}
}

func TestRPCCtxKeepsItsOwnBudgetWhenShorter(t *testing.T) {
	reqCtx, cancelRequest := context.WithTimeout(context.Background(), time.Hour)
	defer cancelRequest()

	callCtx, cancelCall := (&Client{}).WithRequestContext(reqCtx).rpcCtx(time.Minute)
	defer cancelCall()

	deadline, ok := callCtx.Deadline()
	if !ok {
		t.Fatal("the call has no deadline")
	}
	if remaining := time.Until(deadline); remaining > time.Minute {
		t.Fatalf("call deadline is %v away, want the call's own 1m budget", remaining)
	}
}

func TestRPCCtxWithoutRequestUsesOwnBudget(t *testing.T) {
	callCtx, cancelCall := (&Client{}).rpcCtx(time.Minute)
	defer cancelCall()

	deadline, ok := callCtx.Deadline()
	if !ok {
		t.Fatal("the call has no deadline")
	}
	if remaining := time.Until(deadline); remaining < 50*time.Second || remaining > time.Minute {
		t.Fatalf("call deadline is %v away, want about 1m", remaining)
	}
}
