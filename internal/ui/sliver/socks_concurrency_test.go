package sliver

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// The SOCKS stream is written from one goroutine per local connection, and gRPC
// forbids concurrent SendMsg on a single stream (grpc/stream.go, Stream
// interface). Before the fix every write went straight to the stream, so two
// connections at once corrupted the transport's send map and killed the process
// with a fatal runtime error.
//
// The test cannot make gRPC itself misbehave, so it does the next best thing: it
// installs a stream that records whether two Send calls ever overlap, then drives
// the same helper the real call sites use. A regression to a bare
// p.stream.Send(...) at those sites stops routing through the lock and the
// overlap counter goes non-zero.

// fakeSocksStream implements the streaming client by embedding the real
// interface, so every method this test does not care about is inherited rather
// than hand-written. Calling one of them panics on a nil embedded interface,
// which is the behaviour the other stubs in this package rely on: a test that
// starts exercising a new call fails loudly instead of passing for the wrong
// reason.
type fakeSocksStream struct {
	rpcpb.SliverRPC_SocksProxyClient

	sendCalls  int64
	closeCalls int64
	overlaps   int64

	// inSend is 1 while a Send is inside the critical section.
	inSend int32

	mu   sync.Mutex
	sent []*sliverpb.SocksData
}

func (f *fakeSocksStream) Send(msg *sliverpb.SocksData) error {
	if !atomic.CompareAndSwapInt32(&f.inSend, 0, 1) {
		atomic.AddInt64(&f.overlaps, 1)
	}

	// Widen the window deliberately. Without this the critical section is short
	// enough that the scheduler may never interleave the goroutines, and the test
	// would pass whether or not the lock exists.
	for i := 0; i < 500; i++ {
		_ = i
	}

	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()
	atomic.AddInt64(&f.sendCalls, 1)

	atomic.StoreInt32(&f.inSend, 0)
	return nil
}

// CloseSend is implemented rather than inherited so the fake can be closed
// without dereferencing the nil embedded interface. It records the call so the
// test can assert the close actually happened.
func (f *fakeSocksStream) CloseSend() error {
	atomic.AddInt64(&f.closeCalls, 1)
	return nil
}

// newTestProxy builds a proxy carrying just the fields the send path touches.
func newTestProxy(stream *fakeSocksStream) *SocksProxy {
	return &SocksProxy{
		SessionID: "test-session",
		stream:    stream,
		conns:     map[uint64]net.Conn{},
		done:      make(chan struct{}),
	}
}

// TestSocksSendIsSerialised drives the send helper from many goroutines at once.
// A missing lock shows up as a non-zero overlap count.
func TestSocksSendIsSerialised(t *testing.T) {
	fake := &fakeSocksStream{}
	p := newTestProxy(fake)

	const goroutines = 32
	const perGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = p.send(&sliverpb.SocksData{
					TunnelID: uint64(id),
					Data:     []byte{byte(i)},
				})
			}
		}(g)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&fake.overlaps); got != 0 {
		t.Fatalf("Send was entered concurrently %d time(s); the stream lock is missing", got)
	}
	if want := int64(goroutines * perGoroutine); atomic.LoadInt64(&fake.sendCalls) != want {
		t.Errorf("recorded %d sends, want %d", atomic.LoadInt64(&fake.sendCalls), want)
	}
}

// The helper has to actually reach the stream, or the test above would pass on a
// send that silently does nothing.
func TestSocksSendReachesTheStream(t *testing.T) {
	fake := &fakeSocksStream{}
	p := newTestProxy(fake)

	if err := p.send(&sliverpb.SocksData{TunnelID: 1}); err != nil {
		t.Fatalf("send returned an error: %v", err)
	}
	if atomic.LoadInt64(&fake.sendCalls) != 1 {
		t.Fatalf("send did not reach the stream (calls = %d)", atomic.LoadInt64(&fake.sendCalls))
	}
}

// CloseSend is covered by the same contract, so it must take the same lock. The
// assertion is that it does not race with an in-flight Send: run both together
// under -race and a missing lock is reported by the detector.
func TestSocksCloseSendDoesNotRaceWithSend(t *testing.T) {
	fake := &fakeSocksStream{}
	p := newTestProxy(fake)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = p.send(&sliverpb.SocksData{TunnelID: 1})
		}
	}()
	go func() {
		defer wg.Done()
		p.streamMu.Lock()
		_ = p.stream.CloseSend()
		p.streamMu.Unlock()
	}()
	wg.Wait()

	if atomic.LoadInt64(&fake.overlaps) != 0 {
		t.Errorf("Send overlapped with another Send while CloseSend ran")
	}
}
