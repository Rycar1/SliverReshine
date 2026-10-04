package sliver

import (
	"bytes"
	"compress/gzip"
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

func TestDecodeDownloadData_GzipIsDecompressed(t *testing.T) {
	// The implant compresses every file it returns and labels it "gzip"; the
	// console has to undo that before the bytes reach the operator.
	plain := []byte("hello from sliverreshine")
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	got, err := decodeDownloadData("gzip", compressed.Bytes())
	if err != nil {
		t.Fatalf("decodeDownloadData: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("decoded = %q, want %q", got, plain)
	}
}

func TestDecodeDownloadData_PassesThroughUnknownEncoder(t *testing.T) {
	// An empty or unknown encoder must not be rejected: the official client only
	// special-cases gzip, and a new codec should degrade to raw bytes rather than
	// break every download.
	raw := []byte("not compressed")
	for _, enc := range []string{"", "raw", "base64"} {
		got, err := decodeDownloadData(enc, raw)
		if err != nil {
			t.Fatalf("encoder %q: unexpected error %v", enc, err)
		}
		if string(got) != string(raw) {
			t.Fatalf("encoder %q: got %q, want %q", enc, got, raw)
		}
	}
}

func TestDecodeDownloadData_BadGzipIsAnError(t *testing.T) {
	// Corrupt data must surface as an error instead of being handed on as the
	// file's contents.
	if _, err := decodeDownloadData("gzip", []byte("not gzip at all")); err == nil {
		t.Fatal("expected an error for a non-gzip payload labelled gzip")
	}
}
