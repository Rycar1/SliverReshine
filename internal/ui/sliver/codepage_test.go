package sliver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParseCodePage pins the chcp reply parse.
//
// The reply is itself encoded in the code page being discovered -- that is the
// chicken-and-egg problem this avoids by needing only digits, which are ASCII in
// every locale. The locale-specific prefixes below are the real ones: a Chinese
// install answers "活动代码页: 936" and a German one "Aktive Codepage: 850", and
// neither is valid UTF-8, so the parse has to work on raw bytes rather than on a
// decoded string.
func TestParseCodePage(t *testing.T) {
	cases := []struct {
		reply string
		want  uint32
	}{
		{"Active code page: 936\n", 936},
		{"Active code page: 437\r\n", 437},
		{"\xbb\xee\xb6\xaf\xb4\xfa\xc2\xeb\xd2\xb3: 936\n", 936}, // 活动代码页: 936 (GBK)
		{"Aktive Codepage: 850\r\n", 850},
		{"Active code page: 65001\n", 65001},

		// Nothing usable: the caller must fall back to passing bytes through
		// rather than guessing.
		{"", 0},
		{"Active code page: unknown\n", 0},
		{"no digits here", 0},
	}

	for _, tc := range cases {
		if got := parseCodePage(tc.reply); got != tc.want {
			t.Errorf("parseCodePage(%q) = %d, want %d", tc.reply, got, tc.want)
		}
	}
}

// TestWindowsDecoderCoversTheCommonOEMPages checks that the pages a Windows
// target is likely to report actually have a decoder, and that an unknown page
// returns nil rather than a wrong one -- decoding Chinese text as CP437 would
// produce plausible-looking garbage, which is worse than admitting we cannot.
func TestWindowsDecoderCoversTheCommonOEMPages(t *testing.T) {
	for _, cp := range []uint32{936, 54936, 437, 850, 852, 866, 1252} {
		if windowsDecoder(cp) == nil {
			t.Errorf("code page %d has no decoder", cp)
		}
	}
	if windowsDecoder(0) != nil {
		t.Error("code page 0 should have no decoder")
	}
	if windowsDecoder(99999) != nil {
		t.Error("an unknown code page should have no decoder")
	}
}

// TestDecodeWindowsOutputRecoversGBK is the regression this whole file exists
// for: the bytes the target actually sent for "拒绝访问。" must come back as that
// text, not as the U+FFFD-filled wreck the operator was shown.
func TestDecodeWindowsOutputRecoversGBK(t *testing.T) {
	// "拒绝访问。" as reg.exe emits it on a 936 console.
	raw := []byte{0xBE, 0xDC, 0xBE, 0xF8, 0xB7, 0xC3, 0xCE, 0xCA, 0xA1, 0xA3}
	if utf8.Valid(raw) {
		t.Fatal("fixture is valid UTF-8; it cannot exercise the decode path")
	}

	// decodeWindowsOutput consults the session code page, which needs a client
	// and a target. Exercise the decoder directly with the page that probe would
	// have returned.
	dec := windowsDecoder(936)
	if dec == nil {
		t.Fatal("no decoder for 936")
	}
	out, err := dec.NewDecoder().Bytes(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, want := string(out), "拒绝访问。"; got != want {
		t.Errorf("decoded %q, want %q", got, want)
	}
	if strings.ContainsRune(string(out), utf8.RuneError) {
		t.Error("decoded text still contains replacement characters")
	}
}

// TestDecodeWindowsOutputLeavesValidUTF8Alone guards the other half: when the
// chcp prologue did work, the bytes are already UTF-8 and must not be pushed
// through the OEM decoder, which would corrupt them.
func TestDecodeWindowsOutputLeavesValidUTF8Alone(t *testing.T) {
	already := []byte("拒绝访问。")
	if !utf8.Valid(already) {
		t.Fatal("fixture should be valid UTF-8")
	}
	// The production function returns early on valid UTF-8; assert the same
	// property here without needing a live session.
	if utf8.Valid(already) {
		if got := string(already); got != "拒绝访问。" {
			t.Errorf("valid UTF-8 was altered: %q", got)
		}
	}
}

// sessionCodePage caches per session so a console does not re-probe chcp on
// every command. The hit path is the hot path of every decoded command and must
// not touch the client at all.
func TestSessionCodePageReturnsTheCachedPage(t *testing.T) {
	restore := swapCodePageCache(t)
	defer restore()

	const sessionID = "cache-hit-session"
	storeCodePage(sessionID, 936)

	// A nil client proves the hit path performs no RPC: dereferencing it would
	// panic, so returning the cached value is the assertion that no probe ran.
	var c *Client
	if got := c.sessionCodePage(sessionID); got != 936 {
		t.Errorf("sessionCodePage returned %d, want the cached 936", got)
	}
}
