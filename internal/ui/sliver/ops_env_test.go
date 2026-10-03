package sliver

import (
	"context"
	"testing"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// envStub answers GetEnv with a fixed list, so the console's handling of the
// bytes an implant actually sends can be tested without a server.
type envStub struct {
	rpcpb.SliverRPCClient

	info *sliverpb.EnvInfo
}

func (s *envStub) GetEnv(_ context.Context, _ *sliverpb.EnvReq, _ ...grpc.CallOption) (*sliverpb.EnvInfo, error) {
	return s.info, nil
}

func TestValidUTF8ReplacesInvalidBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii is untouched", "PATH=/usr/bin", "PATH=/usr/bin"},
		{"valid multi-byte is untouched", "LANG=zh_CN.UTF-8 \u4e2d\u6587", "LANG=zh_CN.UTF-8 \u4e2d\u6587"},
		// 0xff and 0xfe are never valid UTF-8; a Windows OEM-encoded value can
		// contain them. ToValidUTF8 collapses a run of invalid bytes into one
		// replacement rather than one per byte.
		{"a run of invalid bytes becomes one U+FFFD", "BAD=\xff\xfe", "BAD=\uFFFD"},
		{"valid text around an invalid byte is kept", "A=\xffok", "A=\uFFFDok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validUTF8(c.in)
			if got != c.want {
				t.Errorf("validUTF8(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The regression this guards: a non-UTF-8 environment value made the whole
// response fail to decode, and the operator saw "string field contains invalid
// UTF-8" instead of their environment. Sanitizing on the way out means the list
// still renders, with the bad bytes marked rather than lost.
func TestGetEnvSanitizesVariablesFromTheImplant(t *testing.T) {
	c := &Client{RPC: &envStub{info: &sliverpb.EnvInfo{
		Variables: []*commonpb.EnvVar{
			{Key: "PATH", Value: "/usr/bin"},
			{Key: "BAD", Value: "caf\xe9"},
		},
	}}}

	got, err := c.GetEnv("s1")
	if err != nil {
		t.Fatalf("GetEnv: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetEnv returned %d rows, want 2", len(got))
	}
	// Sorted by key, so BAD comes first.
	if got[0].Key != "BAD" {
		t.Fatalf("got[0].Key = %q, want BAD", got[0].Key)
	}
	if got[0].Value != "caf\uFFFD" {
		t.Errorf("BAD value = %q, want %q", got[0].Value, "caf\uFFFD")
	}
	if got[1].Key != "PATH" || got[1].Value != "/usr/bin" {
		t.Errorf("PATH row = %+v, want it untouched", got[1])
	}
}
