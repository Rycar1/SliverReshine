package sliver

import (
	"testing"

	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

func TestDirToView_SortsDirsFirst(t *testing.T) {
	d := dirToView(&sliverpb.Ls{
		Path:   "/tmp",
		Exists: true,
		Files: []*sliverpb.FileInfo{
			{Name: "zeta.txt", IsDir: false, Size: 10},
			{Name: "alpha", IsDir: true},
			{Name: "beta.txt", IsDir: false},
			{Name: "gamma", IsDir: true},
		},
	})
	if d == nil || !d.Exists || d.Path != "/tmp" {
		t.Fatalf("unexpected view: %+v", d)
	}
	want := []string{"alpha", "gamma", "beta.txt", "zeta.txt"}
	for i, name := range want {
		if d.Files[i].Name != name {
			t.Fatalf("files[%d] = %q, want %q (order: %v)", i, d.Files[i].Name, name, d.Files)
		}
	}
}

func TestDirToView_HandlesNil(t *testing.T) {
	if dirToView(nil) != nil {
		t.Fatal("nil input should produce nil view")
	}
}

func TestDirToView_SkipsNilFiles(t *testing.T) {
	d := dirToView(&sliverpb.Ls{
		Files: []*sliverpb.FileInfo{nil, {Name: "ok.txt", IsDir: false}},
	})
	if len(d.Files) != 1 || d.Files[0].Name != "ok.txt" {
		t.Fatalf("unexpected files: %+v", d.Files)
	}
}

func TestEncodeBase64(t *testing.T) {
	got := encodeBase64([]byte("hello world"))
	if got != "aGVsbG8gd29ybGQ=" {
		t.Fatalf("encodeBase64 = %q", got)
	}
	if encodeBase64(nil) != "" {
		t.Fatal("nil should produce empty string")
	}
}
