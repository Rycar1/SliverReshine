package sliver

import (
	"unicode/utf8"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// MimikatzImport adds parsed credentials to the vault, skipping duplicates.
//
// Re-running a harvest is normal — an operator sweeps logonpasswords again after
// a fresh logon — and without the duplicate check the vault would fill with
// copies of the same finding until it was useless for review.
func (c *Client) MimikatzImport(parsed []ParsedCredential, originUUID string) (int, error) {
	if len(parsed) == 0 {
		return 0, nil
	}

	existing, err := c.Creds()
	if err != nil {
		return 0, err
	}
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[credDedupeKey(e.Username, e.Plaintext, e.Hash)] = true
	}

	var batch []CredentialView
	for _, p := range parsed {
		name := storeUsername(p.Domain, p.Username)
		if name == "" {
			continue
		}
		view := CredentialView{
			Username:       name,
			Collection:     "mimikatz",
			OriginHostUUID: originUUID,
		}
		switch p.Kind {
		case "plaintext":
			view.Plaintext = p.Secret
		case "ntlm":
			view.Hash = p.Secret
			view.HashType = int32(clientpb.HashType_NTLM)
		case "sha1":
			view.Hash = p.Secret
			view.HashType = int32(clientpb.HashType_SHA1)
		case "vault-reference":
			// A stored entry whose secret blob is empty. There is no password to
			// import, but the account name is still intelligence -- it says this
			// host holds a persisted credential for that identity, which is what
			// decides whether a later `vault::list` or a token steal is worth it.
			view.Collection = "mimikatz (reference)"
		case "vault-blob":
			// A stored secret that is not text: DPAPI material, a certificate, an
			// opaque token. It cannot be sprayed and cannot be read as a password,
			// so it is filed under its own collection and kept hex-encoded. Losing
			// it would be worse than filing it oddly: it is often the only copy of
			// a key that unlocks something else on the host.
			view.Plaintext = p.Secret
			view.Collection = "mimikatz (binary)"
		default:
			continue
		}

		// Proto strings are UTF-8 by contract. One invalid value would fail the
		// marshal for the entire batch and lose every credential in it, so the
		// offending entry is dropped here rather than taking the rest with it.
		if !utf8.ValidString(view.Username) || !utf8.ValidString(view.Plaintext) ||
			!utf8.ValidString(view.Hash) {
			continue
		}

		key := credDedupeKey(view.Username, view.Plaintext, view.Hash)
		if seen[key] {
			continue
		}
		seen[key] = true
		batch = append(batch, view)
	}

	if len(batch) == 0 {
		return 0, nil
	}
	if err := c.CredsAdd(batch); err != nil {
		return 0, err
	}
	return len(batch), nil
}
