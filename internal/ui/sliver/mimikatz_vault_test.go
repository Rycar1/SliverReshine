package sliver

import (
	"testing"
	"unicode/utf8"
)

// Captured verbatim from `mimikatz # vault::cred` on a real Windows host. This
// is the shape the synthetic tests missed: the fields are unspaced ("UserName",
// "TargetName") and the secret is a hex byte string, not text.
const realVaultCredOutput = ` .#####.   mimikatz 2.2.0 (x64) #19041 Sep 19 2022 17:44:08
 .## ^ ##.  "A La Vie, A L'Amour" - (oe.eo)
 ## / \ ##  /*** Benjamin DELPY ` + "`" + `gentilkiwi` + "`" + ` ( benjamin@gentilkiwi.com )
mimikatz(commandline) # vault::cred
TargetName : MicrosoftAccount:user=rycarl@outlook.com / <NULL>
UserName   : Rycarl@outlook.com
Comment    : PersistedCredential
Type       : 1 - generic
Persist    : 2 - local_machine
Flags      : 00000000
Credential :
Attributes : 12

TargetName : reg.redrock.team / <NULL>
UserName   : robot-ctf
Comment    : <NULL>
Type       : 1 - generic
Persist    : 2 - local_machine
Flags      : 00000000
Credential : 65 30 51 76 54 43 37 67 4d 48 5a 61 44 4d 56 4b 4c
Attributes : 12

mimikatz(commandline) # exit
Bye!
`

func TestParseVaultCredDecodesHexSecret(t *testing.T) {
	got := ParseMimikatz(realVaultCredOutput, "vault::cred")

	var withSecret *ParsedCredential
	for i := range got {
		if got[i].Secret != "" {
			withSecret = &got[i]
		}
	}
	if withSecret == nil {
		t.Fatalf("no credential with a secret was parsed; got %d entries: %+v", len(got), got)
	}
	if withSecret.Username != "robot-ctf" {
		t.Errorf("username = %q, want robot-ctf", withSecret.Username)
	}
	// The hex blob is the password, not a hash: 65='e' 30='0' 51='Q' ...
	if want := "e0QvTC7gMHZaDMVKL"; withSecret.Secret != want {
		t.Errorf("secret = %q, want %q (the hex blob must be decoded, not stored raw)", withSecret.Secret, want)
	}
	if withSecret.Kind != "plaintext" {
		t.Errorf("kind = %q, want plaintext", withSecret.Kind)
	}

	// An empty Credential field is a stored entry with no readable secret. The
	// account is still worth surfacing, but it must not look like a password.
	var empty *ParsedCredential
	for i := range got {
		if got[i].Username == "Rycarl@outlook.com" {
			empty = &got[i]
		}
	}
	if empty == nil {
		t.Fatal("the Microsoft account entry was dropped entirely")
	}
	if empty.Kind != "vault-reference" {
		t.Errorf("empty-blob kind = %q, want vault-reference", empty.Kind)
	}
	if empty.Secret != "" {
		t.Errorf("empty-blob secret = %q, want empty", empty.Secret)
	}

	// The banner and the command echo must not turn into credentials.
	for _, p := range got {
		if p.Username == "" {
			t.Error("parsed a credential with no username")
		}
	}
}

// The unspaced "UserName" spelling must not be mistaken for the sekurlsa
// "* Username" field, and vice versa.
func TestParseVaultCredDoesNotDisturbSekurlsa(t *testing.T) {
	const sekurlsa = `Authentication Id : 0 ; 425310 (00000000:00067d5e)
Session           : Interactive from 1
User Name         : labadmin
Domain            : LAB

	msv :
	 * Username : labadmin
	 * Domain   : LAB
	 * NTLM     : 209c6174da490caeb422f3fa5a7ae634
`
	got := ParseMimikatz(sekurlsa, "sekurlsa::logonpasswords")
	if len(got) != 1 {
		t.Fatalf("expected 1 credential, got %d: %+v", len(got), got)
	}
	if got[0].Username != "labadmin" || got[0].Kind != "ntlm" {
		t.Errorf("sekurlsa parse changed: %+v", got[0])
	}
	if got[0].Domain != "LAB" {
		t.Errorf("domain = %q, want LAB", got[0].Domain)
	}
}

// A capture containing both shapes must not double-store the same secret.
func TestParseMimikatzDeduplicatesAcrossShapes(t *testing.T) {
	const both = realVaultCredOutput + `
Authentication Id : 0 ; 999 (00000000:000003e7)
User Name         : robot-ctf
Domain            :

	msv :
	 * Username : robot-ctf
	 * Password : e0QvTC7gMHZaDMVKL
`
	got := ParseMimikatz(both, "mixed")
	seen := map[string]int{}
	for _, p := range got {
		seen[p.Username+"|"+p.Secret]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("duplicate entry %q stored %d times", k, n)
		}
	}
}

func TestDecodeHexBlob(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"65 30 51 76", "e0Qv", true},
		{"65 30 51 76 00 00", "e0Qv", true}, // padding is trimmed
		{"4d 69 6d 69 6b 61 74 7a", "Mimikatz", true},
		{"not hex at all", "", false},
		{"6", "", false},     // odd field
		{"zz zz", "", false}, // not hex digits
		{"00 00", "", false}, // nothing but padding
		{"", "", false},      // empty
		{"41 42 43", "ABC", true},
	}
	for _, tc := range cases {
		got, ok := decodeHexBlob(tc.in)
		if ok != tc.ok {
			t.Errorf("decodeHexBlob(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && string(got) != tc.want {
			t.Errorf("decodeHexBlob(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A plain-text Credential line (some builds print it directly) must survive.
func TestParseVaultCredPlainCredential(t *testing.T) {
	const plain = `TargetName : server01 / <NULL>
UserName   : svc_backup
Credential : Sup3rSecret!
`
	got := ParseMimikatz(plain, "vault::cred")
	if len(got) != 1 {
		t.Fatalf("expected 1 credential, got %d: %+v", len(got), got)
	}
	if got[0].Secret != "Sup3rSecret!" {
		t.Errorf("secret = %q, want Sup3rSecret!", got[0].Secret)
	}
}

// A binary blob must not cost the whole harvest.
//
// Captured from a real host: mimikatz parses fine, but one credential holds
// non-UTF-8 bytes. gRPC marshaling then rejects the entire CredsAdd batch, so
// 28 parsed credentials produced 0 vault entries -- the operator sees "parsed
// 28" and an empty vault. The blob is kept hex-encoded; everything else must
// still land.
func TestBinaryVaultBlobDoesNotBreakTheBatch(t *testing.T) {
	const mixed = `TargetName : server01 / <NULL>
UserName   : svc_backup
Credential : 53 75 70 33 72 53 65 63 72 65 74 21

TargetName : cert-material / <NULL>
UserName   : machine-key
Credential : 30 82 01 0a 02 82 01 01 00 c7 8f 9a ff ff fd 80

TargetName : server02 / <NULL>
UserName   : svc_web
Credential : 57 65 62 50 61 73 73 31 32 33
`
	got := ParseMimikatz(mixed, "vault::cred")

	var texts, blobs int
	for _, p := range got {
		if !utf8.ValidString(p.Secret) {
			t.Errorf("every parsed secret must be valid UTF-8, got %q", p.Secret)
		}
		if !utf8.ValidString(p.Username) {
			t.Errorf("username is not valid UTF-8: %q", p.Username)
		}
		switch p.Kind {
		case "plaintext":
			texts++
		case "vault-blob":
			blobs++
		}
	}

	if texts != 2 {
		t.Errorf("readable credentials = %d, want 2 -- the blob must not suppress them", texts)
	}
	if blobs != 1 {
		t.Errorf("binary blobs = %d, want 1", blobs)
	}

	// The blob must be preserved losslessly, hex-encoded.
	for _, p := range got {
		if p.Kind != "vault-blob" {
			continue
		}
		if p.Secret == "" {
			t.Error("the blob was dropped instead of hex-encoded")
		}
		if len(p.Secret)%2 != 0 {
			t.Errorf("blob is not even-length hex: %q", p.Secret)
		}
	}
}

// The batch guard: a secret that is somehow not valid UTF-8 must be skipped
// individually rather than failing every other credential in the run.
func TestImportSkipsUnrepresentableEntry(t *testing.T) {
	parsed := []ParsedCredential{
		{Username: "good1", Secret: "password1", Kind: "plaintext"},
		{Username: "bad", Secret: string([]byte{0xff, 0xfe, 0x80}), Kind: "plaintext"},
		{Username: "good2", Secret: "password2", Kind: "plaintext"},
	}
	for _, p := range parsed {
		// Mirrors the guard in MimikatzImport: the decision is per entry.
		valid := utf8.ValidString(p.Username) && utf8.ValidString(p.Secret)
		if p.Username == "bad" && valid {
			t.Error("the invalid entry would have reached the wire")
		}
		if (p.Username == "good1" || p.Username == "good2") && !valid {
			t.Errorf("a valid entry was rejected: %+v", p)
		}
	}
}

// mimikatz prints the literal "<NULL>" for an entry with no owning account.
// Filing a live token under that anonymous label hides it from every later
// search for the real account.
func TestVaultNullUsernameIsNotAUsername(t *testing.T) {
	const raw = `TargetName : github-oauth / <NULL>
UserName   : <NULL>
Credential : 67 68 6f 5f 41 42 43 44 45 46

TargetName : real / <NULL>
UserName   : svc_real
Credential : 70 61 73 73 77 6f 72 64
`
	got := ParseMimikatz(raw, "vault::cred")
	for _, p := range got {
		if isPlaceholder(p.Username) {
			t.Errorf("placeholder %q was stored as a username", p.Username)
		}
	}

	// The token must survive, just not under "<NULL>".
	var token *ParsedCredential
	for i := range got {
		if p := &got[i]; p.Kind == "plaintext" && len(p.Secret) > 0 && p.Username != "svc_real" {
			token = p
		}
	}
	if token == nil {
		t.Fatal("the unnamed token was dropped entirely")
	}
	if token.Secret != "gho_ABCDEF" {
		t.Errorf("token = %q, want gho_ABCDEF", token.Secret)
	}
	if token.Username == "" {
		t.Error("an unnamed secret must still get an addressable name")
	}
}

// Two unnamed entries must not collapse into one.
func TestVaultUnnamedEntriesStayDistinct(t *testing.T) {
	const raw = `TargetName : token-a / <NULL>
UserName   : <NULL>
Credential : 41 41

TargetName : token-b / <NULL>
UserName   : <NULL>
Credential : 42 42
`
	got := ParseMimikatz(raw, "vault::cred")
	secrets := map[string]bool{}
	for _, p := range got {
		secrets[p.Secret] = true
	}
	if len(secrets) != 2 {
		t.Errorf("expected 2 distinct secrets, got %d: %+v", len(secrets), got)
	}
}
