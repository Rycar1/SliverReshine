package sliver

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The console must be willing to receive anything the Sliver server is willing
// to send. The server registers its listeners with `ServerMaxMessageSize`
// (server/transport/mtls.go) and Sliver's own client is configured for
// `ClientMaxReceiveMessageSize` (2 GB - 1). A console that caps lower than that
// rejects the reply after the work has already been done on the target, which
// is how a full-memory process dump used to fail:
//
//	rpc error: code = ResourceExhausted desc = grpc: received message larger
//	than max (590803284 vs. 134217728)
//
// The dump completed successfully on the target in that case; only the
// transport refused it.
func TestConsoleReceiveLimitMatchesSliver(t *testing.T) {
	// Sliver's own client limit. Kept as a literal so this test fails loudly if
	// upstream ever moves, rather than silently tracking the change.
	const sliverClientLimit = (2 * 1024 * 1024 * 1024) - 1

	if maxReceiveMessageSize != sliverClientLimit {
		t.Fatalf("maxReceiveMessageSize = %d, want %d to match Sliver's own client",
			maxReceiveMessageSize, sliverClientLimit)
	}
}

// A limit below the size of a routine full-memory minidump is the exact defect
// this constant replaced. 128 MB is the number that used to be hardcoded.
func TestConsoleReceiveLimitExceedsATypicalProcessDump(t *testing.T) {
	const oldLimit = 128 * 1024 * 1024
	const observedDump = 590803284 // from the failing run

	if maxReceiveMessageSize <= observedDump {
		t.Fatalf("limit %d is not above the observed failing dump size %d",
			maxReceiveMessageSize, observedDump)
	}
	if oldLimit >= observedDump {
		t.Fatal("test premise is wrong: the old limit would have accepted the dump")
	}
}

// Guards against the literal being reintroduced at the call site. The dial
// options must reference the constant, not a number.
func TestDialOptionsUseTheConstant(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("client.go"))
	if err != nil {
		t.Fatalf("read client.go: %v", err)
	}

	// The option must name the constant.
	if !regexp.MustCompile(`MaxCallRecvMsgSize\(\s*maxReceiveMessageSize\s*\)`).Match(src) {
		t.Fatal("dial options do not pass maxReceiveMessageSize to MaxCallRecvMsgSize")
	}

	// And no bare byte-count literal may remain inside a MaxCallRecvMsgSize call.
	bad := regexp.MustCompile(`MaxCallRecvMsgSize\(\s*\d`)
	if loc := bad.FindIndex(src); loc != nil {
		t.Fatalf("a numeric literal is still used for the receive limit at byte %d", loc[0])
	}

	// Sanity: the constant itself is a plain integer expression we can parse.
	m := regexp.MustCompile(`const maxReceiveMessageSize = (.+)`).FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("maxReceiveMessageSize is not declared as a simple const")
	}
	if _, err := strconv.Atoi(m[1]); err != nil {
		t.Logf("constant expression is %q (evaluated in code, not a bare literal)", m[1])
	}
}
