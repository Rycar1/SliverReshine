package sliver

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// screenshotStub answers Screenshot with whatever the test sets.
type screenshotStub struct {
	rpcStub
	data []byte
	err  string
}

func (s *screenshotStub) Screenshot(_ context.Context, _ *sliverpb.ScreenshotReq, _ ...grpc.CallOption) (*sliverpb.Screenshot, error) {
	return &sliverpb.Screenshot{
		Response: &commonpb.Response{Err: s.err},
		Data:     s.data,
	}, nil
}

// A headless target (no X11/Quartz display) returns no image bytes while the
// RPC still reports success. The console used to answer 200 with an empty Data
// field, which the UI rendered as a blank image with no explanation -- the
// operator could not tell "the target has no desktop" from "the console lost
// the image". An empty capture is now stated as an error.
func TestScreenshotEmptyResultIsAnError(t *testing.T) {
	c := &Client{RPC: &screenshotStub{}}
	_, err := c.Screenshot("s-1")
	if err == nil {
		t.Fatal("an empty screenshot was reported as success")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "empty screenshot") {
		t.Errorf("the error does not explain the empty image: %v", err)
	}
}

// A real capture must still come back base64-encoded and untouched.
func TestScreenshotReturnsBase64Image(t *testing.T) {
	c := &Client{RPC: &screenshotStub{data: []byte{0x89, 0x50, 0x4e, 0x47}}}
	got, err := c.Screenshot("s-1")
	if err != nil {
		t.Fatalf("a real image was rejected: %v", err)
	}
	if got != "iVBORw==" {
		t.Errorf("image data = %q, want the base64 of the PNG magic", got)
	}
}

// The implant own error is still surfaced verbatim rather than replaced by the
// empty-image sentence.
func TestScreenshotKeepsImplantError(t *testing.T) {
	c := &Client{RPC: &screenshotStub{err: "boom"}}
	_, err := c.Screenshot("s-1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("the implant error was lost: %v", err)
	}
}
