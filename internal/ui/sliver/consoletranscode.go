package sliver

import (
	"bytes"
	"strings"

	"golang.org/x/text/transform"
)

// ConsoleCodec transcodes a Windows shell's console stream in both directions.
//
// Two problems meet here, and both follow from the same fact: a Windows shell
// spawned by the implant has no console.
//
// # Encoding
//
// A console-less shell reads and writes the console's OEM code page -- 936 on a
// Chinese install, 437 or 850 elsewhere. The browser speaks UTF-8. Passing the
// bytes through unchanged is what produced
//
//	Microsoft Windows [汾 6.3.9600]
//
// from "Microsoft Windows [版本 6.3.9600]": the raw GBK bytes were decoded as
// UTF-8, and every byte that did not happen to form a valid UTF-8 sequence
// became U+FFFD or a wrong glyph. The text is destroyed, not merely ugly.
//
// Exec mode already decoded its output -- see decodeWindowsOutput. The shell
// tunnel did not, so the two modes disagreed about the same target: `whoami` in
// compat mode was readable while `whoami` in shell mode was not.
//
// Input needs the mirror image. When the operator types a non-ASCII character
// the browser sends UTF-8, and a shell reading GBK from its pipe sees mojibake
// and cannot open the file the operator named.
//
// # Streaming
//
// A code page such as GBK is multi-byte, and the tunnel hands over arbitrary
// chunks -- an 8192-byte read boundary can fall in the middle of a character.
// Decoding each chunk independently would corrupt precisely those characters,
// and which ones would depend on where the boundary landed: a bug that appears
// as "the output is sometimes wrong" and resists reproduction. The codec
// therefore holds an incomplete trailing sequence until the rest arrives.
type ConsoleCodec struct {
	codePage uint32

	dec transform.Transformer
	enc transform.Transformer

	// pendingDec and pendingEnc hold the bytes of an incomplete multi-byte
	// character that the next call is expected to finish.
	pendingDec []byte
	pendingEnc []byte
}

// NewConsoleCodec returns a codec for the session's console, or nil when the
// session needs no transcoding: a non-Windows target, or a Windows target
// already running on UTF-8 (code page 65001).
//
// A nil codec is safe to use; every method tolerates it.
func (c *Client) NewConsoleCodec(sessionID string) *ConsoleCodec {
	// Check the platform before probing the code page. The probe spawns cmd.exe
	// on the target, and on a Linux session that is a wasted round trip that
	// cannot succeed -- there is no cmd.exe to run and no code page to report.
	// The answer is already known here, so asking is pure cost.
	if osName, ok := c.sessionOS(sessionID); ok && !strings.Contains(strings.ToLower(osName), platformWindows) {
		return nil
	}

	cp := c.sessionCodePage(sessionID)
	enc := windowsDecoder(cp)
	if enc == nil {
		return nil
	}
	return &ConsoleCodec{
		codePage: cp,
		dec:      enc.NewDecoder(),
		enc:      enc.NewEncoder(),
	}
}

// CodePage reports the code page the codec transcodes, or 0 when there is none.
func (c *ConsoleCodec) CodePage() uint32 {
	if c == nil {
		return 0
	}
	return c.codePage
}

// Decode converts target output to UTF-8.
//
// It always decodes from the code page, with no "looks like UTF-8 already"
// escape hatch. That heuristic is wrong for a byte stream, and demonstrably so:
// GBK 0xCF 0xA2 is "息", but those same two bytes are also a valid UTF-8
// sequence (U+03E2, "Ϣ"). A chunk boundary that happened to land just before
// them produced a two-byte buffer that passed a utf8.Valid test and was emitted
// undecoded -- so the last character of a message was silently replaced with an
// unrelated glyph, at a rate that depended on the read sizes. Only whole-message
// decoding can afford that test; see decodeWindowsOutput.
//
// Passing through is instead decided by the code page itself: an unknown page,
// or 65001, yields a nil codec at construction and this method is never reached.
func (c *ConsoleCodec) Decode(p []byte) []byte {
	if c == nil || c.dec == nil || len(p) == 0 {
		return p
	}
	c.pendingDec = append(c.pendingDec, p...)
	if len(c.pendingDec) == 0 {
		return nil
	}

	// Decode from the OEM code page, keeping whatever trailing bytes do not yet
	// form a complete character so the next read can finish them.
	out, rest := transformStream(c.dec, c.pendingDec)
	c.pendingDec = append([]byte(nil), rest...)
	return out
}

// Encode converts operator input from UTF-8 into the target's code page.
//
// An incomplete trailing rune is held rather than encoded partially, so a
// multi-byte character arriving in two chunks is not turned into two wrong ones.
func (c *ConsoleCodec) Encode(p []byte) []byte {
	if c == nil || c.enc == nil || len(p) == 0 {
		return p
	}
	c.pendingEnc = append(c.pendingEnc, p...)
	if len(c.pendingEnc) == 0 {
		return nil
	}
	out, rest := transformStream(c.enc, c.pendingEnc)
	c.pendingEnc = append([]byte(nil), rest...)
	return out
}

// FlushDecode emits anything the decoder is still holding.
//
// The codec holds a trailing sequence only when it might be the start of a
// character, so at end of stream those bytes are known to be undecodable. They
// are returned as-is rather than dropped: a wrong glyph is easier to recognise
// than a message with its tail missing.
func (c *ConsoleCodec) FlushDecode() []byte {
	if c == nil || len(c.pendingDec) == 0 {
		return nil
	}
	out := c.pendingDec
	c.pendingDec = nil
	return out
}

// transformStream drives a x/text transformer over a buffer that may end
// mid-character.
//
// It returns what could be converted and the unconverted tail, which the caller
// holds for the next call. ErrShortSrc is that tail -- an incomplete character,
// not a failure -- so it ends the loop rather than being reported.
func transformStream(t transform.Transformer, src []byte) (out []byte, rest []byte) {
	var buf bytes.Buffer
	chunk := make([]byte, 4096)

	for len(src) > 0 {
		nDst, nSrc, err := t.Transform(chunk, src, false)
		if nDst > 0 {
			buf.Write(chunk[:nDst])
		}
		src = src[nSrc:]

		if err != nil {
			if err == transform.ErrShortDst {
				continue
			}
			// ErrShortSrc, or a genuine decode error: keep what is left.
			break
		}
		if nDst == 0 && nSrc == 0 {
			// No progress; stop rather than spin on bytes we cannot consume.
			break
		}
	}

	return buf.Bytes(), src
}
