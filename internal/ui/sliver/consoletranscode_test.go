package sliver

import (
	"bytes"
	"sync"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// newTestCodec builds a codec for a fixed code page, so the tests do not depend
// on the host's locale or on a live session.
func newTestCodec(codePage uint32) *ConsoleCodec {
	enc := windowsDecoder(codePage)
	if enc == nil {
		return nil
	}
	return &ConsoleCodec{
		codePage: codePage,
		dec:      enc.NewDecoder(),
		enc:      enc.NewEncoder(),
	}
}

// The regression this whole type exists for: Windows shell output in the OEM code
// page must reach the browser as UTF-8.
//
// The concrete symptom was the cmd.exe banner, which arrived as
// "Microsoft Windows [汾 6.3.9600]" instead of "[版本 6.3.9600]".
func TestCodecDecodesGBKConsoleOutput(t *testing.T) {
	codec := newTestCodec(936)
	if codec == nil {
		t.Fatal("no decoder for code page 936; the GBK path is gone")
	}

	// "版本" in GBK.
	gbk := []byte{0xB0, 0xE6, 0xB1, 0xBE}
	want := "版本"

	got := string(codec.Decode(gbk))
	if got != want {
		t.Fatalf("Decode(GBK 版本) = %q, want %q", got, want)
	}
}

// The codec decodes unconditionally, and that is a deliberate correction.
//
// There used to be a "already valid UTF-8, pass it through" shortcut. It is
// wrong for a byte stream: GBK 0xCF 0xA2 ("息") is also a valid UTF-8 sequence
// (U+03E2 "Ϣ"), so a chunk that happened to end right after those two bytes was
// emitted undecoded and the last character of a message became an unrelated
// glyph -- at a rate that depended on where the read boundaries fell.
//
// TestCodecSurvivesEverySplitPoint is what caught it. This test pins the
// corrected behaviour so the shortcut is not reintroduced.
func TestCodecAlwaysDecodesFromTheCodePage(t *testing.T) {
	codec := newTestCodec(936)
	if codec == nil {
		t.Fatal("no decoder for code page 936")
	}

	// These two bytes are simultaneously valid GBK and valid UTF-8. The codec
	// must treat them as GBK, because that is what the target's console writes.
	ambiguous := []byte{0xCF, 0xA2}
	got := string(codec.Decode(ambiguous))

	if got != "息" {
		t.Fatalf("Decode(0xCF 0xA2) = %q, want %q; the UTF-8 shortcut is back", got, "息")
	}
}

// A code page the console cannot transcode must still not lose bytes.
//
// The passthrough decision is made at construction, not per chunk: an unknown
// page yields a nil codec, and a nil codec is transparent.
func TestCodecLeavesUntranscodableBytesAlone(t *testing.T) {
	// 65001 is UTF-8, which needs no transcoding.
	if codec := newTestCodec(65001); codec != nil {
		t.Error("code page 65001 produced a codec; it should need none")
	}
	// An unknown page must not produce one either, rather than guessing.
	if codec := newTestCodec(999999); codec != nil {
		t.Error("an unknown code page produced a codec")
	}
}

// A multi-byte character split across two reads must survive.
//
// The tunnel hands over arbitrary chunks and an 8192-byte boundary can fall
// inside a character. Decoding each chunk independently corrupts exactly those
// characters, and which ones depends on where the boundary landed -- a bug that
// shows up as "the output is sometimes wrong" and is very hard to reproduce.
func TestCodecReassemblesASplitCharacter(t *testing.T) {
	codec := newTestCodec(936)
	if codec == nil {
		t.Fatal("no decoder for code page 936")
	}

	// "版本" split after the first byte of the second character.
	full := []byte{0xB0, 0xE6, 0xB1, 0xBE}
	part1 := full[:3]
	part2 := full[3:]

	first := codec.Decode(part1)
	second := codec.Decode(part2)

	combined := string(first) + string(second)
	if combined != "版本" {
		t.Fatalf("split decode = %q, want %q (first=%q second=%q)",
			combined, "版本", first, second)
	}

	// The first call must not have invented a replacement character for the
	// half character it was holding.
	if bytes.ContainsRune(first, '\uFFFD') {
		t.Errorf("first chunk produced a replacement character: %q", first)
	}
}

// Every possible split point of a multi-byte string must decode correctly. One
// split point working is not enough: the boundary is arbitrary.
func TestCodecSurvivesEverySplitPoint(t *testing.T) {
	enc := simplifiedchinese.GBK.NewEncoder()
	want := "中文测试版本信息"
	encoded, err := enc.Bytes([]byte(want))
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	// GBK uses two bytes per CJK character, so a fixture of CJK text must encode
	// to more bytes than it has runes. Comparing against the UTF-8 length would be
	// wrong: UTF-8 uses three bytes per CJK character, so GBK output is shorter
	// and the assertion would fail on a correct encoder.
	if len(encoded) <= len([]rune(want)) {
		t.Fatalf("fixture is not multi-byte: %d bytes for %d runes", len(encoded), len([]rune(want)))
	}

	for split := 1; split < len(encoded); split++ {
		codec := newTestCodec(936)
		if codec == nil {
			t.Fatal("no decoder for code page 936")
		}
		got := string(codec.Decode(encoded[:split])) + string(codec.Decode(encoded[split:]))
		if got != want {
			t.Errorf("split at %d: got %q, want %q", split, got, want)
		}
	}
}

// Input has to be encoded into the target's code page, which is the mirror of
// the output path. A non-ASCII character sent as raw UTF-8 is read by a GBK
// shell as two or three unrelated characters.
func TestCodecEncodesOperatorInput(t *testing.T) {
	codec := newTestCodec(936)
	if codec == nil {
		t.Fatal("no decoder for code page 936")
	}

	got := codec.Encode([]byte("版本"))
	want := []byte{0xB0, 0xE6, 0xB1, 0xBE}
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode(版本) = % x, want % x", got, want)
	}
}

// A round trip through Encode then Decode must return the original text, at
// every split point. This is what an operator actually does: type a path, and
// expect the shell to receive that path.
func TestCodecRoundTripsThroughEverySplitPoint(t *testing.T) {
	text := "C:\\用户\\测试 文件.txt"
	encoded := newTestCodec(936).Encode([]byte(text))

	for split := 1; split < len(encoded); split++ {
		// Decoding the encoder's own output has to give the text back, even
		// when the boundary falls inside a character.
		codec := newTestCodec(936)
		if codec == nil {
			t.Fatal("no decoder for code page 936")
		}
		got := string(codec.Decode(encoded[:split])) + string(codec.Decode(encoded[split:]))
		if got != text {
			t.Errorf("split %d: round trip = %q, want %q", split, got, text)
		}
	}
}

// A nil codec must be transparent. It is what a unix session and a UTF-8 Windows
// target get, and every call site relies on it being safe.
func TestNilCodecIsTransparent(t *testing.T) {
	var codec *ConsoleCodec
	in := []byte("plain ascii and 版本")

	if got := string(codec.Decode(in)); got != string(in) {
		t.Errorf("nil Decode changed the bytes: %q", got)
	}
	if got := string(codec.Encode(in)); got != string(in) {
		t.Errorf("nil Encode changed the bytes: %q", got)
	}
	if got := codec.CodePage(); got != 0 {
		t.Errorf("nil CodePage = %d, want 0", got)
	}
	if got := codec.FlushDecode(); got != nil {
		t.Errorf("nil FlushDecode = %q, want nil", got)
	}
}

// Output the decoder cannot interpret must be passed through rather than
// dropped: an unreadable message still tells the operator more than silence.
func TestCodecFlushesHeldBytesOnClose(t *testing.T) {
	codec := newTestCodec(936)
	if codec == nil {
		t.Fatal("no decoder for code page 936")
	}

	// A lone lead byte is held, because more bytes could complete it.
	held := []byte{0xB0}
	if out := codec.Decode(held); len(out) != 0 {
		t.Fatalf("expected the lead byte to be held, got %q", out)
	}

	// At end of stream it is emitted rather than swallowed.
	flushed := codec.FlushDecode()
	if len(flushed) == 0 {
		t.Fatal("FlushDecode dropped the held bytes; the message would lose its tail")
	}
}

// The terminal drives Decode and Encode from two goroutines at once: the output
// pump decodes implant bytes while the input loop encodes keystrokes. The codec
// is safe there only because the directions share no mutable state, and that is a
// property nobody notices breaking until a race detector runs.
//
// This drives both directions concurrently with multi-byte text, so a shared
// buffer or a shared transformer shows up as a data race under -race rather than
// as corrupted output in the field. It is not a correctness oracle -- the
// split-point tests are -- it is the thing that fails loudly if the two
// directions are ever coupled.
func TestCodecIsSafeForConcurrentUse(t *testing.T) {
	codec := newTestCodec(936)
	if codec == nil {
		t.Fatal("no decoder for code page 936")
	}

	const rounds = 200
	var wg sync.WaitGroup

	// Output direction.
	wg.Add(1)
	go func() {
		defer wg.Done()
		gbk := []byte{0xB0, 0xE6, 0xB1, 0xBE}
		for i := 0; i < rounds; i++ {
			codec.Decode(gbk[:i%len(gbk)+1])
		}
	}()

	// Input direction.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			codec.Encode([]byte("版本"[:i%len("版本")+1]))
		}
	}()

	wg.Wait()
}
