package sliver

import (
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

func TestBeaconTaskToView(t *testing.T) {
	v := beaconTaskToView(&clientpb.BeaconTask{
		ID:          "task-1",
		BeaconID:    "beacon-1",
		CreatedAt:   1700000000,
		State:       "completed",
		CompletedAt: 1700000060,
		Description: "exec /bin/whoami",
		Response:    []byte("root"),
	})
	if v == nil {
		t.Fatal("nil view")
	}
	if v.ID != "task-1" || v.BeaconID != "beacon-1" || v.State != "completed" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if v.ResponseB64 != "cm9vdA==" {
		t.Fatalf("ResponseB64 = %q, want cm9vdA==", v.ResponseB64)
	}
}

func TestBeaconTaskToView_Nil(t *testing.T) {
	if beaconTaskToView(nil) != nil {
		t.Fatal("nil input should produce nil view")
	}
}

func TestSocksProxyView_StartValidation(t *testing.T) {
	sm := NewSocksManager(nil)
	if _, err := sm.Start("", "127.0.0.1", 0, "", ""); err == nil {
		t.Fatal("empty session id should error")
	}
}

func TestItoa(t *testing.T) {
	cases := map[uint32]string{0: "0", 1: "1", 1080: "1080", 65535: "65535"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Fatalf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}
