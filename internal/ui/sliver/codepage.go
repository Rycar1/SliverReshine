package sliver

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// Why Windows output is decoded rather than transcoded on the target
//
// Every Windows module that reports a message -- reg, sc, schtasks, net -- writes
// it in the console's OEM code page, not UTF-8. On a Chinese install that is 936,
// on a Western one 437 or 850. The persistence layer's answer was to prefix each
// command with `chcp 65001`, switching the spawned cmd.exe to UTF-8 so the
// message comes back readable.
//
// That only works when the spawned cmd.exe has a console to switch. Sliver
// spawns it with HideWindow, which Go maps to CREATE_NO_WINDOW -- no console --
// so `chcp` silently fails and the message arrives in the OEM code page anyway.
// The result is worse than mojibake: the raw bytes are not valid UTF-8, so the
// JSON layer replaces each of them with U+FFFD and the original text is
// destroyed. An operator saw `?Ü¾????Ê¡?` instead of "拒绝访问。" and had no way
// to recover the meaning.
//
// So the prologue is kept as an optimisation and the decode below is the
// guarantee: output that is already valid UTF-8 is left untouched, and anything
// else is decoded from the code page the target actually reports.

// codePageCache memoises the OEM code page per session. A session ID belongs to
// one running implant for its whole life, so the answer cannot go stale, and the
// lookup costs one extra spawn rather than one per command.
//
// It is bounded because it is a package-level map keyed by session, and nothing
// removes an entry when a session ends: a console left running accumulates one
// per implant that has ever checked in. The bound is high enough that no real
// deployment reaches it -- sessions are counted in dozens, not thousands -- so it
// only ever triggers as a safety valve, and evicting the whole map at that point
// costs one probe per live session rather than being a correctness problem.
const maxCodePageCacheEntries = 512

var (
	codePageMu    sync.Mutex
	codePageCache = map[string]uint32{}
)

// windowsDecoder returns a decoder for an OEM code page, or nil when the page is
// not one we can decode. A nil decoder means the bytes are passed through
// unchanged: an unreadable message is still better than a dropped one.
func windowsDecoder(codePage uint32) encoding.Encoding {
	switch codePage {
	case 936:
		return simplifiedchinese.GBK
	case 54936:
		return simplifiedchinese.GB18030
	case 437:
		return charmap.CodePage437
	case 850:
		return charmap.CodePage850
	case 852:
		return charmap.CodePage852
	case 866:
		return charmap.CodePage866
	case 1252:
		return charmap.Windows1252
	}
	return nil
}

// sessionCodePage reports the target's active OEM code page, cached per session.
//
// The query is `chcp` with no argument, whose reply is "Active code page: 936".
// Only the digits matter and they are ASCII in every locale, so the reply can be
// parsed even though it is itself encoded in the code page we are trying to
// discover -- the usual chicken-and-egg problem, avoided by needing only digits.
//
// It goes through execRawOn so the probe does not recurse into decoding.
func (c *Client) sessionCodePage(sessionID string) uint32 {
	codePageMu.Lock()
	if cp, ok := codePageCache[sessionID]; ok {
		codePageMu.Unlock()
		return cp
	}
	codePageMu.Unlock()

	cp := uint32(0)
	res, err := c.execRawOn(sessionID, platformWindows, "cmd.exe", []string{"/c", "chcp"}, 15*time.Second)
	if err == nil {
		cp = parseCodePage(res.Stdout)
	}

	codePageMu.Lock()
	if len(codePageCache) >= maxCodePageCacheEntries {
		// Drop the whole map rather than evicting one entry: choosing a victim
		// needs an LRU this cache does not justify, and a cold map only costs a
		// re-probe for sessions that are actually still in use.
		codePageCache = map[string]uint32{}
	}
	codePageCache[sessionID] = cp
	codePageMu.Unlock()
	return cp
}

// parseCodePage pulls the number out of a chcp reply. A reply with no digits
// yields 0, which windowsDecoder maps to nil and the caller passes bytes through.
func parseCodePage(out string) uint32 {
	digits := strings.Builder{}
	for _, r := range out {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		// A run of digits that has ended is the answer; chcp prints nothing else
		// numeric.
		if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	n, err := strconv.ParseUint(digits.String(), 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

// decodeWindowsOutput converts one stream of Windows command output to UTF-8.
//
// Valid UTF-8 is returned as-is: when the target does have a console and the
// chcp prologue took effect, the bytes are already correct and re-decoding them
// from the OEM page would corrupt them. Only genuinely non-UTF-8 output is
// decoded, which is the case the prologue cannot fix.
func (c *Client) decodeWindowsOutput(sessionID string, b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	if dec := windowsDecoder(c.sessionCodePage(sessionID)); dec != nil {
		if out, err := dec.NewDecoder().Bytes(b); err == nil {
			return string(out)
		}
	}
	return string(b)
}

// decodeExecResult decodes both streams of a result in place. Stderr is decoded
// too: a failing module reports through stderr, so that is exactly the text that
// must survive.
func (c *Client) decodeExecResult(sessionID string, res *ExecResult) *ExecResult {
	if res == nil {
		return res
	}
	res.Stdout = c.decodeWindowsOutput(sessionID, []byte(res.Stdout))
	res.Stderr = c.decodeWindowsOutput(sessionID, []byte(res.Stderr))
	return res
}
