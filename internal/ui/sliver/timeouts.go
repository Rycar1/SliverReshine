package sliver

import (
	"context"
	"time"
)

// Deadlines for calls into sliver-server.
//
// Every RPC the console makes is bounded, and the bound is named here rather
// than written inline. Before this file the same handful of durations was typed
// out at more than a hundred call sites, so there was no one place to look to
// answer "how long does the console wait for the server?" and no way to change
// the answer for all of them at once.
//
// The values are the ones that were already in use. This is a rename, not a new
// policy.
const (
	// rpcProbe bounds a liveness call that should answer immediately.
	rpcProbe = 5 * time.Second

	// rpcQuick bounds a call that reads state the server already holds: a
	// session list, a beacon's task queue.
	rpcQuick = 10 * time.Second

	// rpcDefault bounds an ordinary call that reaches the implant once.
	rpcDefault = 15 * time.Second

	// rpcListenerStart bounds bringing up a listener, which binds a port and
	// registers a website.
	rpcListenerStart = 20 * time.Second

	// opTimeout bounds a call that moves a modest amount of data or builds
	// something server-side.
	opTimeout = 30 * time.Second

	// rpcSlow bounds a call that has to wait on an implant which only checks in
	// periodically.
	rpcSlow = 60 * time.Second

	// rpcLong bounds a call that moves a whole payload or waits on a build.
	rpcLong = 120 * time.Second

	// opTimeoutExt is rpcLong for extended operations -- assembly or DLL
	// execution -- which need longer than opTimeout.
	opTimeoutExt = rpcLong

	// execDefaultTimeout is rpcLong for a one-shot spawn that is not one of the
	// long-running operations with a budget of its own.
	execDefaultTimeout = rpcLong
)

// Sliver multiplexes long-running operations over a single C2 channel whose
// default ceiling is 30s (server/rpc/rpc.go minTimeout). A process dump has to
// push the whole minidump through that same channel, and a migration has to
// compile a fresh shellcode first, so both need their own budget.
const (
	// migrateTimeout bounds a shellcode build plus the injection that follows
	// it.
	migrateTimeout = 6 * time.Minute
	// migrateInjectTimeout is the inject timeout sent through the implant
	// (seconds, converted by requestFor).
	migrateInjectTimeout = 4 * time.Minute
	// dumpTimeout bounds a full-memory dump: large processes legitimately take
	// minutes.
	dumpTimeout = 10 * time.Minute
)

// rpcCtx returns a context bounded by d, for one call into the server.
//
// This is the one place the console's deadline policy is applied, so every call
// site reads as "this call, this deadline" instead of rebuilding the pattern.
// Nothing here may call the server with a context that has no deadline: a
// request that never returns holds an HTTP handler and its goroutine until the
// browser gives up, and the console has no way to notice.
func rpcCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
