package main

import (
	"path/filepath"
	"testing"
)

// repoRoot is the module root relative to this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// TestAnalyzerReadsTheServerRouting is the guard on the guard.
//
// The whole value of this tool is the promoted/not-promoted distinction. If the
// server-side scan silently stopped finding handlers, every RPC would look like
// it needed a reply check, the tool would report dozens of false findings, and
// the next person would delete it rather than read it. If it found *everything*
// promoted, it would report nothing ever -- which is how a real dropped error
// would slip through unnoticed.
func TestAnalyzerReadsTheServerRouting(t *testing.T) {
	a, err := newAnalyzer(repoRoot(t))
	if err != nil {
		t.Fatalf("newAnalyzer: %v", err)
	}

	// Portfwd is the method this tool exists to have caught: the server handler
	// calls session.Request directly, so Response.Err reaches the console.
	if a.promoted["Portfwd"] {
		t.Error("Portfwd is marked as promoting Err, but its handler does not call GenericHandler; " +
			"a dropped reply error would go unreported")
	}

	// These go through GenericHandler, so their Err becomes a gRPC status and
	// inspecting the reply is not required.
	for _, method := range []string{"Ls", "Execute", "GetPrivs"} {
		if !a.promoted[method] {
			t.Errorf("%s is not marked as promoting Err, but its handler calls GenericHandler; "+
				"this tool would report a false finding for every caller", method)
		}
	}

	// A broad sanity band: most of the RPC surface is GenericHandler-based, but
	// not all of it. Both extremes mean the scan broke.
	if len(a.promoted) < 50 {
		t.Errorf("only %d promoted methods found; the server scan looks broken", len(a.promoted))
	}
	if len(a.promoted) >= len(a.methods) {
		t.Errorf("all %d methods look promoted; the server scan is over-matching", len(a.methods))
	}
}

// TestNoDroppedReplyErrors fails when a console call site discards a reply whose
// in-band error the server does not promote.
//
// This is the CI gate. A new `_, err := client.RPC.Something(...)` on a
// non-promoting RPC fails here with the file and line, instead of shipping as a
// console that reports success for an operation the implant refused.
func TestNoDroppedReplyErrors(t *testing.T) {
	root := repoRoot(t)
	a, err := newAnalyzer(root)
	if err != nil {
		t.Fatalf("newAnalyzer: %v", err)
	}

	findings, err := a.scan(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	// The scan must actually see the codebase, or an empty result would look
	// like success.
	if len(findings) < 50 {
		t.Fatalf("only %d call sites classified; the scan found almost nothing", len(findings))
	}

	for _, f := range findings {
		if f.Class == "OK" {
			continue
		}
		t.Errorf("%s:%d: %s drops the reply of %s, whose Response.Err the server does not promote",
			f.File, f.Line, f.Class, f.Method)
	}
}

// TestAnalyzerLearnsErrBearingReplies checks the protobuf scan. commonpb.Response
// carries the error, and every implant-facing reply embeds it; a reply type that
// is known to embed Response must be classified as Err-bearing.
func TestAnalyzerLearnsErrBearingReplies(t *testing.T) {
	a, err := newAnalyzer(repoRoot(t))
	if err != nil {
		t.Fatalf("newAnalyzer: %v", err)
	}

	for _, name := range []string{"commonpb.Response", "sliverpb.Portfwd", "sliverpb.Shell"} {
		if !a.errTypes[name] {
			t.Errorf("%s is not classified as Err-bearing; the protobuf scan missed it", name)
		}
	}

	// A reply with no Response field must not be flagged, or the tool would
	// demand checks that cannot be written.
	if a.errTypes["commonpb.Empty"] {
		t.Error("commonpb.Empty is classified as Err-bearing, but it has no Err field")
	}
}

// TestReplyTypesComeFromTheMethodTable verifies the gRPC interface scan, which is
// what maps a call site to the struct whose Err it should read.
func TestReplyTypesComeFromTheMethodTable(t *testing.T) {
	a, err := newAnalyzer(repoRoot(t))
	if err != nil {
		t.Fatalf("newAnalyzer: %v", err)
	}

	if got := a.methods["Portfwd"]; got != "sliverpb.Portfwd" {
		t.Errorf("Portfwd maps to %q, want sliverpb.Portfwd", got)
	}
	if got := a.methods["Ls"]; got != "sliverpb.Ls" {
		t.Errorf("Ls maps to %q, want sliverpb.Ls", got)
	}
}
