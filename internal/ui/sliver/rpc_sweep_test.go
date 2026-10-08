package sliver

import (
	"context"
	"errors"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// sweepStub is a live-looking sliver-server for the console's orchestration
// helpers.
//
// The console's RPC wrappers are thin by design: they build a request, send it,
// unwrap the reply and map it onto a JSON view. Their value is in the mapping
// and the error plumbing, and before this stub none of it had a signal -- the
// whole file set read as 0% covered. The stub answers every call these helpers
// make with a plausible reply, so the mapping is exercised; unaryErr and
// respErr then drive the two failure shapes (transport error, server-side
// Response.Err) through the same code.
//
// The embedded interface is nil, so any RPC a test does not override panics
// instead of quietly returning a zero value.
type sweepStub struct {
	rpcpb.SliverRPCClient

	// sessionOS is reported for the one session and beacon this stub lists.
	sessionOS string
	// unaryErr, when set, is returned instead of a reply by every call.
	unaryErr error
	// respErr, when set, is placed in every reply's Response.Err.
	respErr string
}

func (s *sweepStub) os() string {
	if s.sessionOS == "" {
		return "windows"
	}
	return s.sessionOS
}

func (s *sweepStub) resp() *commonpb.Response {
	return &commonpb.Response{Err: s.respErr}
}

// --- sessions / beacons / operators ---

func (s *sweepStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Sessions{Sessions: []*clientpb.Session{{
		ID: "s-1", Name: "sess", Hostname: "host", OS: s.os(), Arch: "amd64",
		Transport: "mtls", ActiveC2: "mtls://10.0.0.1:8888",
	}}}, nil
}

func (s *sweepStub) GetBeacons(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Beacons, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Beacons{Beacons: []*clientpb.Beacon{{ID: "b-1", OS: s.os(), Arch: "amd64"}}}, nil
}

func (s *sweepStub) GetOperators(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Operators, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Operators{Operators: []*clientpb.Operator{{Name: "op", Online: true}}}, nil
}

// --- process / network / environment ---

func (s *sweepStub) Ifconfig(_ context.Context, _ *sliverpb.IfconfigReq, _ ...grpc.CallOption) (*sliverpb.Ifconfig, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Ifconfig{
		NetInterfaces: []*sliverpb.NetInterface{{
			Index: 1, Name: "eth0", MAC: "aa:bb", IPAddresses: []string{"10.0.0.2"},
		}},
		Response: s.resp(),
	}, nil
}

func (s *sweepStub) Ps(_ context.Context, _ *sliverpb.PsReq, _ ...grpc.CallOption) (*sliverpb.Ps, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Ps{
		Processes: []*commonpb.Process{{
			Pid: 10, Ppid: 1, Executable: "x.exe", Owner: "u", SessionID: 1,
			CmdLine: []string{"x", "--flag"},
		}},
		Response: s.resp(),
	}, nil
}

func (s *sweepStub) Terminate(_ context.Context, _ *sliverpb.TerminateReq, _ ...grpc.CallOption) (*sliverpb.Terminate, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Terminate{Response: s.resp()}, nil
}

func (s *sweepStub) Netstat(_ context.Context, _ *sliverpb.NetstatReq, _ ...grpc.CallOption) (*sliverpb.Netstat, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Netstat{
		Entries: []*sliverpb.SockTabEntry{{
			Protocol:  "tcp",
			SkState:   "ESTABLISHED",
			UID:       1,
			LocalAddr: &sliverpb.SockTabEntry_SockAddr{Ip: "0.0.0.0", Port: 80},
			RemoteAddr: &sliverpb.SockTabEntry_SockAddr{
				Ip: "1.2.3.4", Port: 443,
			},
			Process: &commonpb.Process{Executable: "svc"},
		}},
		Response: s.resp(),
	}, nil
}

func (s *sweepStub) SetEnv(_ context.Context, _ *sliverpb.SetEnvReq, _ ...grpc.CallOption) (*sliverpb.SetEnv, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.SetEnv{Response: s.resp()}, nil
}

func (s *sweepStub) UnsetEnv(_ context.Context, _ *sliverpb.UnsetEnvReq, _ ...grpc.CallOption) (*sliverpb.UnsetEnv, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.UnsetEnv{Response: s.resp()}, nil
}

// --- injection / migration / token ---

func (s *sweepStub) ExecuteAssembly(_ context.Context, _ *sliverpb.ExecuteAssemblyReq, _ ...grpc.CallOption) (*sliverpb.ExecuteAssembly, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.ExecuteAssembly{Output: []byte("assembly out"), Response: s.resp()}, nil
}

func (s *sweepStub) Sideload(_ context.Context, _ *sliverpb.SideloadReq, _ ...grpc.CallOption) (*sliverpb.Sideload, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Sideload{Result: "sideload out", Response: s.resp()}, nil
}

func (s *sweepStub) SpawnDll(_ context.Context, _ *sliverpb.InvokeSpawnDllReq, _ ...grpc.CallOption) (*sliverpb.SpawnDll, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.SpawnDll{Result: "spawndll out", Response: s.resp()}, nil
}

func (s *sweepStub) Migrate(_ context.Context, _ *clientpb.MigrateReq, _ ...grpc.CallOption) (*sliverpb.Migrate, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Migrate{Success: true, Pid: 4242, Response: s.resp()}, nil
}

func (s *sweepStub) ProcessDump(_ context.Context, _ *sliverpb.ProcessDumpReq, _ ...grpc.CallOption) (*sliverpb.ProcessDump, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.ProcessDump{Data: []byte("minidump"), Response: s.resp()}, nil
}

func (s *sweepStub) Impersonate(_ context.Context, _ *sliverpb.ImpersonateReq, _ ...grpc.CallOption) (*sliverpb.Impersonate, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Impersonate{Response: s.resp()}, nil
}

func (s *sweepStub) MakeToken(_ context.Context, _ *sliverpb.MakeTokenReq, _ ...grpc.CallOption) (*sliverpb.MakeToken, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.MakeToken{Response: s.resp()}, nil
}

func (s *sweepStub) RevToSelf(_ context.Context, _ *sliverpb.RevToSelfReq, _ ...grpc.CallOption) (*sliverpb.RevToSelf, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.RevToSelf{Response: s.resp()}, nil
}

func (s *sweepStub) Ping(_ context.Context, _ *sliverpb.Ping, _ ...grpc.CallOption) (*sliverpb.Ping, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Ping{Nonce: 42, Response: s.resp()}, nil
}

// --- implant builds ---

func (s *sweepStub) Regenerate(_ context.Context, _ *clientpb.RegenerateReq, _ ...grpc.CallOption) (*clientpb.Generate, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Generate{File: &commonpb.File{Name: "imp", Data: []byte("bin")}}, nil
}

func (s *sweepStub) ImplantBuilds(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.ImplantBuilds, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.ImplantBuilds{Configs: map[string]*clientpb.ImplantConfig{"imp": {}}}, nil
}

// --- registry ---

func (s *sweepStub) RegistryCreateKey(_ context.Context, _ *sliverpb.RegistryCreateKeyReq, _ ...grpc.CallOption) (*sliverpb.RegistryCreateKey, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.RegistryCreateKey{Response: s.resp()}, nil
}

func (s *sweepStub) RegistryDeleteKey(_ context.Context, _ *sliverpb.RegistryDeleteKeyReq, _ ...grpc.CallOption) (*sliverpb.RegistryDeleteKey, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.RegistryDeleteKey{Response: s.resp()}, nil
}

func (s *sweepStub) RegistryReadHive(_ context.Context, _ *sliverpb.RegistryReadHiveReq, _ ...grpc.CallOption) (*sliverpb.RegistryReadHive, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.RegistryReadHive{Data: []byte("hive"), Encoder: "gzip", Response: s.resp()}, nil
}

// --- credential vault ---

func (s *sweepStub) CredsRm(_ context.Context, _ *clientpb.Credentials, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

func (s *sweepStub) CredsUpdate(_ context.Context, _ *clientpb.Credentials, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

func (s *sweepStub) GetCredByID(_ context.Context, _ *clientpb.Credential, _ ...grpc.CallOption) (*clientpb.Credential, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Credential{ID: "c-1", Username: "admin", Hash: "deadbeef"}, nil
}

func (s *sweepStub) CredsSniffHashType(_ context.Context, _ *clientpb.Credential, _ ...grpc.CallOption) (*clientpb.Credential, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Credential{Hash: "deadbeef", HashType: clientpb.HashType_NTLM}, nil
}

func (s *sweepStub) GetCredsByHashType(_ context.Context, _ *clientpb.Credential, _ ...grpc.CallOption) (*clientpb.Credentials, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Credentials{Credentials: []*clientpb.Credential{{ID: "c-1"}}}, nil
}

func (s *sweepStub) GetPlaintextCredsByHashType(_ context.Context, _ *clientpb.Credential, _ ...grpc.CallOption) (*clientpb.Credentials, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.Credentials{Credentials: []*clientpb.Credential{{ID: "c-2", Plaintext: "pw"}}}, nil
}

// --- memfiles / file attributes / grep ---

func (s *sweepStub) MemfilesAdd(_ context.Context, _ *sliverpb.MemfilesAddReq, _ ...grpc.CallOption) (*sliverpb.MemfilesAdd, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.MemfilesAdd{Fd: 7, Response: s.resp()}, nil
}

func (s *sweepStub) MemfilesList(_ context.Context, _ *sliverpb.MemfilesListReq, _ ...grpc.CallOption) (*sliverpb.Ls, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Ls{
		Path: "/mem", Exists: true,
		Files:    []*sliverpb.FileInfo{{Name: "m1", Size: 1, Mode: "-rw"}},
		Response: s.resp(),
	}, nil
}

func (s *sweepStub) MemfilesRm(_ context.Context, _ *sliverpb.MemfilesRmReq, _ ...grpc.CallOption) (*sliverpb.MemfilesRm, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.MemfilesRm{Response: s.resp()}, nil
}

func (s *sweepStub) Chmod(_ context.Context, _ *sliverpb.ChmodReq, _ ...grpc.CallOption) (*sliverpb.Chmod, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Chmod{Response: s.resp()}, nil
}

func (s *sweepStub) Chown(_ context.Context, _ *sliverpb.ChownReq, _ ...grpc.CallOption) (*sliverpb.Chown, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Chown{Response: s.resp()}, nil
}

func (s *sweepStub) Chtimes(_ context.Context, _ *sliverpb.ChtimesReq, _ ...grpc.CallOption) (*sliverpb.Chtimes, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Chtimes{Response: s.resp()}, nil
}

// --- process spawns ---
//
// Chown and the ACL helpers on a Windows target are expressed as an icacls
// spawn rather than as an RPC of their own, so the sweep needs an execute
// surface. The reply is a plain success: what these cases check is that the
// wrapper returns at all, not what icacls printed.

func (s *sweepStub) ExecuteWindows(_ context.Context, _ *sliverpb.ExecuteWindowsReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Execute{Stdout: []byte("processed"), Response: s.resp()}, nil
}

func (s *sweepStub) Execute(_ context.Context, _ *sliverpb.ExecuteReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Execute{Stdout: []byte("processed"), Response: s.resp()}, nil
}

func (s *sweepStub) Grep(_ context.Context, _ *sliverpb.GrepReq, _ ...grpc.CallOption) (*sliverpb.Grep, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Grep{
		SearchPathAbsolute: "/etc",
		Results: map[string]*sliverpb.GrepResultsForFile{
			"a.txt": {FileResults: []*sliverpb.GrepResult{{
				LineNumber: 3, Line: "secret", LinesBefore: []string{"b"}, LinesAfter: []string{"a"},
			}}},
		},
		Response: s.resp(),
	}, nil
}

// --- monitoring providers ---

func (s *sweepStub) MonitorAddConfig(_ context.Context, _ *clientpb.MonitoringProvider, _ ...grpc.CallOption) (*commonpb.Response, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return s.resp(), nil
}

func (s *sweepStub) MonitorDelConfig(_ context.Context, _ *clientpb.MonitoringProvider, _ ...grpc.CallOption) (*commonpb.Response, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return s.resp(), nil
}

func (s *sweepStub) MonitorListConfig(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.MonitoringProviders, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.MonitoringProviders{Providers: []*clientpb.MonitoringProvider{{
		ID: "p", Type: "t", APIKey: "k",
	}}}, nil
}

func (s *sweepStub) MonitorStart(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*commonpb.Response, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return s.resp(), nil
}

func (s *sweepStub) MonitorStop(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

// --- C2 profiles and encoders ---

func (s *sweepStub) GetHTTPC2Profiles(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.HTTPC2Configs, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.HTTPC2Configs{Configs: []*clientpb.HTTPC2Config{{
		ID: "1", Name: "prof", Created: 1,
		ImplantConfig: &clientpb.HTTPC2ImplantConfig{UserAgent: "UA"},
	}}}, nil
}

func (s *sweepStub) GetHTTPC2ProfileByName(_ context.Context, _ *clientpb.C2ProfileReq, _ ...grpc.CallOption) (*clientpb.HTTPC2Config, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.HTTPC2Config{
		ID: "1", Name: "prof",
		ServerConfig:  &clientpb.HTTPC2ServerConfig{},
		ImplantConfig: &clientpb.HTTPC2ImplantConfig{},
	}, nil
}

func (s *sweepStub) TrafficEncoderMap(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.TrafficEncoderMap, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.TrafficEncoderMap{Encoders: map[string]*clientpb.TrafficEncoder{
		"enc": {ID: 5, TestID: "t"},
	}}, nil
}

func (s *sweepStub) TrafficEncoderAdd(_ context.Context, _ *clientpb.TrafficEncoder, _ ...grpc.CallOption) (*clientpb.TrafficEncoderTests, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.TrafficEncoderTests{
		Encoder:       &clientpb.TrafficEncoder{ID: 9},
		Tests:         []*clientpb.TrafficEncoderTest{{Name: "x", Completed: true, Success: true, Duration: 1}},
		TotalTests:    1,
		TotalDuration: 1,
	}, nil
}

func (s *sweepStub) TrafficEncoderRm(_ context.Context, _ *clientpb.TrafficEncoder, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

func (s *sweepStub) ShellcodeEncoderMap(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.ShellcodeEncoderMap, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.ShellcodeEncoderMap{Encoders: map[string]*clientpb.ShellcodeEncoderArchMap{
		"amd64": {Encoders: map[string]clientpb.ShellcodeEncoder{"xor": clientpb.ShellcodeEncoder_XOR}},
	}}, nil
}

func (s *sweepStub) ShellcodeEncoder(_ context.Context, _ *clientpb.ShellcodeEncodeReq, _ ...grpc.CallOption) (*clientpb.ShellcodeEncode, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.ShellcodeEncode{Data: []byte("encoded"), Response: s.resp()}, nil
}

// --- certificates / tunnels / services ---

func (s *sweepStub) GetCertificateAuthorityInfo(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.CertificateAuthorityInfo, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.CertificateAuthorityInfo{Info: []*clientpb.CertificateAuthorityData{{
		CN: "ca", ValidityExpiry: "2030", KeyAlgorithm: "ed25519", Type: "CA",
	}}}, nil
}

func (s *sweepStub) GetCertificateInfo(_ context.Context, _ *clientpb.CertificatesReq, _ ...grpc.CallOption) (*clientpb.CertificateInfo, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.CertificateInfo{Info: []*clientpb.CertificateData{{
		CN: "leaf", ValidityExpiry: "2030", KeyAlgorithm: "rsa", Type: "CA",
	}}}, nil
}

func (s *sweepStub) CreateTunnel(_ context.Context, _ *sliverpb.Tunnel, _ ...grpc.CallOption) (*sliverpb.Tunnel, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Tunnel{TunnelID: 3}, nil
}

func (s *sweepStub) CloseTunnel(_ context.Context, _ *sliverpb.Tunnel, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

func (s *sweepStub) ServiceDetail(_ context.Context, _ *sliverpb.ServiceDetailReq, _ ...grpc.CallOption) (*sliverpb.ServiceDetail, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.ServiceDetail{
		Detail: &sliverpb.ServiceDetails{
			Name: "svc", DisplayName: "Svc", Description: "d",
			Status: 4, StartupType: 2, BinPath: "x.exe", Account: "LocalSystem",
		},
		Response: s.resp(),
	}, nil
}

func (s *sweepStub) StartServiceByName(_ context.Context, _ *sliverpb.StartServiceByNameReq, _ ...grpc.CallOption) (*sliverpb.ServiceInfo, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.ServiceInfo{Response: s.resp()}, nil
}

// --- WireGuard ---

func (s *sweepStub) GenerateWGClientConfig(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.WGClientConfig, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.WGClientConfig{ServerPubKey: "sp", ClientPrivateKey: "cp", ClientPubKey: "p", ClientIP: "10.0.0.2"}, nil
}

func (s *sweepStub) GenerateUniqueIP(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.UniqueWGIP, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.UniqueWGIP{IP: "10.0.0.9"}, nil
}

func (s *sweepStub) WGListForwarders(_ context.Context, _ *sliverpb.WGTCPForwardersReq, _ ...grpc.CallOption) (*sliverpb.WGTCPForwarders, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.WGTCPForwarders{
		Forwarders: []*sliverpb.WGTCPForwarder{{ID: 1, LocalAddr: "l", RemoteAddr: "r"}},
		Response:   s.resp(),
	}, nil
}

func (s *sweepStub) WGStartPortForward(_ context.Context, _ *sliverpb.WGPortForwardStartReq, _ ...grpc.CallOption) (*sliverpb.WGPortForward, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.WGPortForward{
		Forwarder: &sliverpb.WGTCPForwarder{ID: 1, LocalAddr: "l", RemoteAddr: "r"},
		Response:  s.resp(),
	}, nil
}

func (s *sweepStub) WGStopPortForward(_ context.Context, _ *sliverpb.WGPortForwardStopReq, _ ...grpc.CallOption) (*sliverpb.WGPortForward, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.WGPortForward{
		Forwarder: &sliverpb.WGTCPForwarder{ID: 1, LocalAddr: "l", RemoteAddr: "r"},
		Response:  s.resp(),
	}, nil
}

func (s *sweepStub) WGListSocksServers(_ context.Context, _ *sliverpb.WGSocksServersReq, _ ...grpc.CallOption) (*sliverpb.WGSocksServers, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.WGSocksServers{
		Servers:  []*sliverpb.WGSocksServer{{ID: 1, LocalAddr: "l"}},
		Response: s.resp(),
	}, nil
}

func (s *sweepStub) WGStartSocks(_ context.Context, _ *sliverpb.WGSocksStartReq, _ ...grpc.CallOption) (*sliverpb.WGSocks, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.WGSocks{
		Server:   &sliverpb.WGSocksServer{ID: 1, LocalAddr: "l"},
		Response: s.resp(),
	}, nil
}

func (s *sweepStub) WGStopSocks(_ context.Context, _ *sliverpb.WGSocksStopReq, _ ...grpc.CallOption) (*sliverpb.WGSocks, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.WGSocks{
		Server:   &sliverpb.WGSocksServer{ID: 1, LocalAddr: "l"},
		Response: s.resp(),
	}, nil
}

// --- beacons / sessions ---

func (s *sweepStub) Rename(_ context.Context, _ *clientpb.RenameReq, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

func (s *sweepStub) RmBeacon(_ context.Context, _ *clientpb.Beacon, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

func (s *sweepStub) GetBeaconTasks(_ context.Context, _ *clientpb.Beacon, _ ...grpc.CallOption) (*clientpb.BeaconTasks, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &clientpb.BeaconTasks{Tasks: []*clientpb.BeaconTask{{
		ID: "t-1", BeaconID: "b-1", State: "sent", Description: "d", Response: []byte("ok"),
	}}}, nil
}

func (s *sweepStub) Reconfigure(_ context.Context, _ *sliverpb.ReconfigureReq, _ ...grpc.CallOption) (*sliverpb.Reconfigure, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &sliverpb.Reconfigure{Response: s.resp()}, nil
}

func (s *sweepStub) CloseSession(_ context.Context, _ *sliverpb.CloseSession, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.unaryErr != nil {
		return nil, s.unaryErr
	}
	return &commonpb.Empty{}, nil
}

func sweepClient() *Client { return &Client{RPC: &sweepStub{}} }

func mustNoErr(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", label, err)
	}
}

// TestOrchestrationSuccess drives every wrapper against a healthy server and
// checks the reply is mapped onto the console's view type rather than dropped.
func TestOrchestrationSuccess(t *testing.T) {
	c := sweepClient()

	ifaces, err := c.Ifconfig("s-1")
	mustNoErr(t, "Ifconfig", err)
	if len(ifaces) != 1 || ifaces[0].Name != "eth0" || len(ifaces[0].IPAddresses) != 1 {
		t.Fatalf("Ifconfig mapped %+v, want one eth0 with one address", ifaces)
	}

	procs, err := c.Ps("s-1")
	mustNoErr(t, "Ps", err)
	if len(procs) != 1 || procs[0].PID != 10 || procs[0].Executable != "x.exe" {
		t.Fatalf("Ps mapped %+v, want pid 10 x.exe", procs)
	}

	mustNoErr(t, "KillProcess", c.KillProcess("s-1", 10, true))

	socks, err := c.Netstat("s-1")
	mustNoErr(t, "Netstat", err)
	if len(socks) != 1 || socks[0].Protocol != "tcp" || socks[0].RemotePort != 443 || socks[0].ProcessName != "svc" {
		t.Fatalf("Netstat mapped %+v, want one tcp entry to :443 from svc", socks)
	}

	mustNoErr(t, "SetEnv", c.SetEnv("s-1", "K", "V"))
	mustNoErr(t, "UnsetEnv", c.UnsetEnv("s-1", "K"))

	asm, err := c.ExecuteAssembly("s-1", []byte("a"), "-x", "p.exe")
	mustNoErr(t, "ExecuteAssembly", err)
	if asm.Output != "assembly out" {
		t.Errorf("ExecuteAssembly output = %q", asm.Output)
	}

	sl, err := c.Sideload("s-1", []byte("d"), "p.exe", "", "ep")
	mustNoErr(t, "Sideload", err)
	if sl.Result != "sideload out" {
		t.Errorf("Sideload result = %q", sl.Result)
	}

	sd, err := c.SpawnDll("s-1", []byte("d"), "p.exe", "", "ep")
	mustNoErr(t, "SpawnDll", err)
	if sd.Result != "spawndll out" {
		t.Errorf("SpawnDll result = %q", sd.Result)
	}

	mustNoErr(t, "Migrate", c.Migrate("s-1", 4242, "p.exe"))

	dump, err := c.ProcessDump("s-1", 10)
	mustNoErr(t, "ProcessDump", err)
	if string(dump.Data) != "minidump" {
		t.Errorf("ProcessDump data = %q", dump.Data)
	}

	mustNoErr(t, "Impersonate", c.Impersonate("s-1", "u"))
	mustNoErr(t, "MakeToken", c.MakeToken("s-1", "u", "p", "d"))
	mustNoErr(t, "RevToSelf", c.RevToSelf("s-1"))

	pong, err := c.Ping("s-1")
	mustNoErr(t, "Ping", err)
	if pong.Nonce != 42 {
		t.Errorf("Ping nonce = %d", pong.Nonce)
	}

	reg, err := c.Regenerate("imp")
	mustNoErr(t, "Regenerate", err)
	// The stub answers with a bare "imp" name and a zero-value ImplantConfig;
	// Regenerate completes the extension from the build config, so the mapped
	// name is "imp" plus the shared-library extension (.so for an empty GOOS).
	if !reg.Success || reg.Name != "imp.so" || reg.Data == "" {
		t.Errorf("Regenerate mapped %+v", reg)
	}

	mustNoErr(t, "RegistryCreateKey", c.RegistryCreateKey("s-1", "HKLM", "Software", "k"))
	mustNoErr(t, "RegistryDeleteKey", c.RegistryDeleteKey("s-1", "HKLM", "Software", "k"))

	hive, enc, err := c.RegistryReadHive("s-1", "HKLM", "SAM")
	mustNoErr(t, "RegistryReadHive", err)
	if string(hive) != "hive" || enc != "gzip" {
		t.Errorf("RegistryReadHive = %q/%q", hive, enc)
	}

	mustNoErr(t, "CredsRm", c.CredsRm([]string{"c-1"}))
	mustNoErr(t, "CredsUpdate", c.CredsUpdate([]CredentialView{{ID: "c-1"}}))
	cred, err := c.GetCredByID("c-1")
	mustNoErr(t, "GetCredByID", err)
	if cred.Username != "admin" || cred.Hash != "deadbeef" {
		t.Errorf("GetCredByID mapped %+v", cred)
	}
	sniffed, err := c.CredsSniffHashType("deadbeef")
	mustNoErr(t, "CredsSniffHashType", err)
	if sniffed.Hash != "deadbeef" {
		t.Errorf("CredsSniffHashType mapped %+v", sniffed)
	}
	byHash, err := c.GetCredsByHashType(1000)
	mustNoErr(t, "GetCredsByHashType", err)
	if len(byHash) != 1 {
		t.Errorf("GetCredsByHashType returned %d", len(byHash))
	}
	plain, err := c.GetPlaintextCredsByHashType(1000)
	mustNoErr(t, "GetPlaintextCredsByHashType", err)
	if len(plain) != 1 || plain[0].Plaintext != "pw" {
		t.Errorf("GetPlaintextCredsByHashType mapped %+v", plain)
	}

	fd, err := c.MemfilesAdd("s-1")
	mustNoErr(t, "MemfilesAdd", err)
	if fd != 7 {
		t.Errorf("MemfilesAdd fd = %d", fd)
	}
	dir, err := c.MemfilesList("s-1")
	mustNoErr(t, "MemfilesList", err)
	if dir == nil || len(dir.Files) != 1 {
		t.Errorf("MemfilesList mapped %+v", dir)
	}
	mustNoErr(t, "MemfilesRm", c.MemfilesRm("s-1", 7))

	mustNoErr(t, "Chmod", c.Chmod("s-1", "/x", "0644", false))
	mustNoErr(t, "Chown", c.Chown("s-1", "/x", "0", "0", true))
	mustNoErr(t, "Chtimes", c.Chtimes("s-1", "/x", 1, 2))

	grep, err := c.Grep("s-1", "secret", "/etc", true, 1, 1)
	mustNoErr(t, "Grep", err)
	if grep.SearchPath != "/etc" || len(grep.Results) != 1 || len(grep.Results[0].Matches) != 1 {
		t.Errorf("Grep mapped %+v", grep)
	}

	mustNoErr(t, "MonitorAddConfig", c.MonitorAddConfig(MonitorProviderView{ID: "p"}))
	mustNoErr(t, "MonitorDelConfig", c.MonitorDelConfig(MonitorProviderView{ID: "p"}))
	provs, err := c.MonitorListConfig()
	mustNoErr(t, "MonitorListConfig", err)
	if len(provs) != 1 || provs[0].APIKey != "k" {
		t.Errorf("MonitorListConfig mapped %+v", provs)
	}

	profiles, err := c.HTTPC2Profiles()
	mustNoErr(t, "HTTPC2Profiles", err)
	if len(profiles) != 1 || profiles[0].UserAgent != "UA" {
		t.Errorf("HTTPC2Profiles mapped %+v", profiles)
	}
	full, err := c.HTTPC2Profile("prof")
	mustNoErr(t, "HTTPC2Profile", err)
	if full == nil || full.Name != "prof" {
		t.Errorf("HTTPC2Profile mapped %+v", full)
	}

	encoders, err := c.TrafficEncoders()
	mustNoErr(t, "TrafficEncoders", err)
	if len(encoders) != 1 || encoders[0] != "enc" {
		t.Errorf("TrafficEncoders = %v", encoders)
	}
	withID, err := c.TrafficEncodersWithID()
	mustNoErr(t, "TrafficEncodersWithID", err)
	if withID["enc"] != 5 {
		t.Errorf("TrafficEncodersWithID = %v", withID)
	}
	rep, err := c.TrafficEncoderAdd("enc", []byte("wasm"), false)
	mustNoErr(t, "TrafficEncoderAdd", err)
	if rep.EncoderID != 9 || rep.TotalTests != 1 || len(rep.Tests) != 1 {
		t.Errorf("TrafficEncoderAdd mapped %+v", rep)
	}
	mustNoErr(t, "TrafficEncoderRm", c.TrafficEncoderRm("enc"))

	shellEnc, err := c.ShellcodeEncoders()
	mustNoErr(t, "ShellcodeEncoders", err)
	if len(shellEnc) != 1 || shellEnc[0].Arch != "amd64" {
		t.Errorf("ShellcodeEncoders mapped %+v", shellEnc)
	}
	encoded, err := c.ShellcodeEncode("xor", "amd64", []byte("sc"), 1, nil)
	mustNoErr(t, "ShellcodeEncode", err)
	if string(encoded) != "encoded" {
		t.Errorf("ShellcodeEncode = %q", encoded)
	}

	ca, err := c.CertificateAuthority()
	mustNoErr(t, "CertificateAuthority", err)
	if len(ca) != 1 || !ca[0].IsCA {
		t.Errorf("CertificateAuthority mapped %+v", ca)
	}
	certs, err := c.Certificates(0, "leaf")
	mustNoErr(t, "Certificates", err)
	if len(certs) != 1 || !certs[0].IsCA {
		t.Errorf("Certificates mapped %+v", certs)
	}

	tid, err := c.CreateTunnel("s-1")
	mustNoErr(t, "CreateTunnel", err)
	if tid != 3 {
		t.Errorf("CreateTunnel id = %d", tid)
	}
	mustNoErr(t, "CloseTunnel", c.CloseTunnel(3, "s-1"))

	svc, err := c.ServiceDetail("s-1", "svc", "")
	mustNoErr(t, "ServiceDetail", err)
	if svc.Name != "svc" || svc.BinPath != "x.exe" {
		t.Errorf("ServiceDetail mapped %+v", svc)
	}
	mustNoErr(t, "StartServiceByName", c.StartServiceByName("s-1", "svc", ""))

	wgCfg, err := c.GenerateWGClientConfig()
	mustNoErr(t, "GenerateWGClientConfig", err)
	if wgCfg.ClientIP != "10.0.0.2" {
		t.Errorf("GenerateWGClientConfig = %+v", wgCfg)
	}
	ip, err := c.GenerateUniqueIP()
	mustNoErr(t, "GenerateUniqueIP", err)
	if ip != "10.0.0.9" {
		t.Errorf("GenerateUniqueIP = %q", ip)
	}
	fwd, err := c.WGForwarders("s-1")
	mustNoErr(t, "WGForwarders", err)
	if len(fwd) != 1 || fwd[0].ID != 1 {
		t.Errorf("WGForwarders mapped %+v", fwd)
	}
	f, async, err := c.WGStartPortForward("s-1", 8080, "r")
	mustNoErr(t, "WGStartPortForward", err)
	if f.ID != 1 || async {
		t.Errorf("WGStartPortForward = %+v/%v", f, async)
	}
	f, _, err = c.WGStopPortForward("s-1", 1)
	mustNoErr(t, "WGStopPortForward", err)
	if f.ID != 1 {
		t.Errorf("WGStopPortForward = %+v", f)
	}
	wgSocks, err := c.WGSocksServers("s-1")
	mustNoErr(t, "WGSocksServers", err)
	if len(wgSocks) != 1 {
		t.Errorf("WGSocksServers mapped %+v", wgSocks)
	}
	s, _, err := c.WGStartSocks("s-1", 1080)
	mustNoErr(t, "WGStartSocks", err)
	if s.ID != 1 {
		t.Errorf("WGStartSocks = %+v", s)
	}
	s, _, err = c.WGStopSocks("s-1", 1)
	mustNoErr(t, "WGStopSocks", err)
	if s.ID != 1 {
		t.Errorf("WGStopSocks = %+v", s)
	}

	mustNoErr(t, "RenameSession", c.RenameSession("s-1", "new"))
	mustNoErr(t, "RenameBeacon", c.RenameBeacon("b-1", "new"))
	mustNoErr(t, "RmBeacon", c.RmBeacon("b-1"))
	tasks, err := c.BeaconTasks("b-1")
	mustNoErr(t, "BeaconTasks", err)
	if len(tasks) != 1 || tasks[0].ID != "t-1" || tasks[0].ResponseB64 == "" {
		t.Errorf("BeaconTasks mapped %+v", tasks)
	}
	mustNoErr(t, "ReconfigureSession", c.ReconfigureSession("s-1", 60))
	mustNoErr(t, "CloseSession", c.CloseSession("s-1"))
	mustNoErr(t, "MonitorStart", c.MonitorStart())
	mustNoErr(t, "MonitorStop", c.MonitorStop())

	ops, err := c.GetOperators()
	mustNoErr(t, "GetOperators", err)
	if len(ops) != 1 || !ops[0].Online {
		t.Errorf("GetOperators mapped %+v", ops)
	}
}

// TestOrchestrationPropagatesTransportErrors pins that a gRPC failure is
// returned rather than swallowed, which is the whole contract of a wrapper.
func TestOrchestrationPropagatesTransportErrors(t *testing.T) {
	c := &Client{RPC: &sweepStub{unaryErr: errors.New("transport down")}}

	calls := map[string]func() error{
		"Ifconfig":                    func() error { _, err := c.Ifconfig("s-1"); return err },
		"Ps":                          func() error { _, err := c.Ps("s-1"); return err },
		"KillProcess":                 func() error { return c.KillProcess("s-1", 1, false) },
		"Netstat":                     func() error { _, err := c.Netstat("s-1"); return err },
		"SetEnv":                      func() error { return c.SetEnv("s-1", "k", "v") },
		"UnsetEnv":                    func() error { return c.UnsetEnv("s-1", "k") },
		"ExecuteAssembly":             func() error { _, err := c.ExecuteAssembly("s-1", []byte("a"), "", ""); return err },
		"Sideload":                    func() error { _, err := c.Sideload("s-1", []byte("d"), "", "", ""); return err },
		"SpawnDll":                    func() error { _, err := c.SpawnDll("s-1", []byte("d"), "", "", ""); return err },
		"Migrate":                     func() error { return c.Migrate("s-1", 1, "p") },
		"ProcessDump":                 func() error { _, err := c.ProcessDump("s-1", 1); return err },
		"Impersonate":                 func() error { return c.Impersonate("s-1", "u") },
		"MakeToken":                   func() error { return c.MakeToken("s-1", "u", "p", "d") },
		"RevToSelf":                   func() error { return c.RevToSelf("s-1") },
		"Ping":                        func() error { _, err := c.Ping("s-1"); return err },
		"Regenerate":                  func() error { _, err := c.Regenerate("imp"); return err },
		"RegistryCreateKey":           func() error { return c.RegistryCreateKey("s-1", "HKLM", "p", "k") },
		"RegistryDeleteKey":           func() error { return c.RegistryDeleteKey("s-1", "HKLM", "p", "k") },
		"RegistryReadHive":            func() error { _, _, err := c.RegistryReadHive("s-1", "HKLM", "SAM"); return err },
		"CredsRm":                     func() error { return c.CredsRm([]string{"c-1"}) },
		"CredsUpdate":                 func() error { return c.CredsUpdate([]CredentialView{{ID: "c-1"}}) },
		"GetCredByID":                 func() error { _, err := c.GetCredByID("c-1"); return err },
		"CredsSniffHashType":          func() error { _, err := c.CredsSniffHashType("h"); return err },
		"GetCredsByHashType":          func() error { _, err := c.GetCredsByHashType(1); return err },
		"GetPlaintextCredsByHashType": func() error { _, err := c.GetPlaintextCredsByHashType(1); return err },
		"MemfilesAdd":                 func() error { _, err := c.MemfilesAdd("s-1"); return err },
		"MemfilesList":                func() error { _, err := c.MemfilesList("s-1"); return err },
		"MemfilesRm":                  func() error { return c.MemfilesRm("s-1", 1) },
		"Chmod":                       func() error { return c.Chmod("s-1", "/x", "0644", false) },
		"Chown":                       func() error { return c.Chown("s-1", "/x", "0", "0", false) },
		"Chtimes":                     func() error { return c.Chtimes("s-1", "/x", 1, 2) },
		"Grep":                        func() error { _, err := c.Grep("s-1", "p", "/x", false, 0, 0); return err },
		"MonitorAddConfig":            func() error { return c.MonitorAddConfig(MonitorProviderView{ID: "p"}) },
		"MonitorDelConfig":            func() error { return c.MonitorDelConfig(MonitorProviderView{ID: "p"}) },
		"MonitorListConfig":           func() error { _, err := c.MonitorListConfig(); return err },
		"HTTPC2Profiles":              func() error { _, err := c.HTTPC2Profiles(); return err },
		"HTTPC2Profile":               func() error { _, err := c.HTTPC2Profile("p"); return err },
		"TrafficEncoders":             func() error { _, err := c.TrafficEncoders(); return err },
		"TrafficEncodersWithID":       func() error { _, err := c.TrafficEncodersWithID(); return err },
		"TrafficEncoderAdd":           func() error { _, err := c.TrafficEncoderAdd("e", []byte("w"), false); return err },
		"ShellcodeEncoders":           func() error { _, err := c.ShellcodeEncoders(); return err },
		"ShellcodeEncode":             func() error { _, err := c.ShellcodeEncode("xor", "amd64", []byte("s"), 1, nil); return err },
		"CertificateAuthority":        func() error { _, err := c.CertificateAuthority(); return err },
		"Certificates":                func() error { _, err := c.Certificates(0, ""); return err },
		"CreateTunnel":                func() error { _, err := c.CreateTunnel("s-1"); return err },
		"CloseTunnel":                 func() error { return c.CloseTunnel(1, "s-1") },
		"ServiceDetail":               func() error { _, err := c.ServiceDetail("s-1", "s", ""); return err },
		"StartServiceByName":          func() error { return c.StartServiceByName("s-1", "s", "") },
		"GenerateWGClientConfig":      func() error { _, err := c.GenerateWGClientConfig(); return err },
		"GenerateUniqueIP":            func() error { _, err := c.GenerateUniqueIP(); return err },
		"WGForwarders":                func() error { _, err := c.WGForwarders("s-1"); return err },
		"WGStartPortForward":          func() error { _, _, err := c.WGStartPortForward("s-1", 1, "r"); return err },
		"WGStopPortForward":           func() error { _, _, err := c.WGStopPortForward("s-1", 1); return err },
		"WGSocksServers":              func() error { _, err := c.WGSocksServers("s-1"); return err },
		"WGStartSocks":                func() error { _, _, err := c.WGStartSocks("s-1", 1); return err },
		"WGStopSocks":                 func() error { _, _, err := c.WGStopSocks("s-1", 1); return err },
		"RenameSession":               func() error { return c.RenameSession("s-1", "n") },
		"RenameBeacon":                func() error { return c.RenameBeacon("b-1", "n") },
		"RmBeacon":                    func() error { return c.RmBeacon("b-1") },
		"BeaconTasks":                 func() error { _, err := c.BeaconTasks("b-1"); return err },
		"ReconfigureSession":          func() error { return c.ReconfigureSession("s-1", 60) },
		"CloseSession":                func() error { return c.CloseSession("s-1") },
		"MonitorStart":                func() error { return c.MonitorStart() },
		"MonitorStop":                 func() error { return c.MonitorStop() },
		"GetOperators":                func() error { _, err := c.GetOperators(); return err },
	}

	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s: a transport error was swallowed", name)
		}
	}
}

// TestOrchestrationSurfacesResponseErrors covers the second failure shape: the
// RPC succeeds at the transport level but the server's embedded Response
// carries an error, which must not be reported as success.
func TestOrchestrationSurfacesResponseErrors(t *testing.T) {
	c := &Client{RPC: &sweepStub{respErr: "server said no"}}

	calls := map[string]func() error{
		"Ifconfig":           func() error { _, err := c.Ifconfig("s-1"); return err },
		"Ps":                 func() error { _, err := c.Ps("s-1"); return err },
		"KillProcess":        func() error { return c.KillProcess("s-1", 1, false) },
		"Netstat":            func() error { _, err := c.Netstat("s-1"); return err },
		"SetEnv":             func() error { return c.SetEnv("s-1", "k", "v") },
		"UnsetEnv":           func() error { return c.UnsetEnv("s-1", "k") },
		"ExecuteAssembly":    func() error { _, err := c.ExecuteAssembly("s-1", []byte("a"), "", ""); return err },
		"Sideload":           func() error { _, err := c.Sideload("s-1", []byte("d"), "", "", ""); return err },
		"SpawnDll":           func() error { _, err := c.SpawnDll("s-1", []byte("d"), "", "", ""); return err },
		"Migrate":            func() error { return c.Migrate("s-1", 1, "p") },
		"ProcessDump":        func() error { _, err := c.ProcessDump("s-1", 1); return err },
		"Impersonate":        func() error { return c.Impersonate("s-1", "u") },
		"MakeToken":          func() error { return c.MakeToken("s-1", "u", "p", "d") },
		"RevToSelf":          func() error { return c.RevToSelf("s-1") },
		"Ping":               func() error { _, err := c.Ping("s-1"); return err },
		"RegistryCreateKey":  func() error { return c.RegistryCreateKey("s-1", "HKLM", "p", "k") },
		"RegistryDeleteKey":  func() error { return c.RegistryDeleteKey("s-1", "HKLM", "p", "k") },
		"RegistryReadHive":   func() error { _, _, err := c.RegistryReadHive("s-1", "HKLM", "SAM"); return err },
		"MemfilesAdd":        func() error { _, err := c.MemfilesAdd("s-1"); return err },
		"MemfilesList":       func() error { _, err := c.MemfilesList("s-1"); return err },
		"MemfilesRm":         func() error { return c.MemfilesRm("s-1", 1) },
		"Chmod":              func() error { return c.Chmod("s-1", "/x", "0644", false) },
		"Chown":              func() error { return c.Chown("s-1", "/x", "0", "0", false) },
		"Chtimes":            func() error { return c.Chtimes("s-1", "/x", 1, 2) },
		"Grep":               func() error { _, err := c.Grep("s-1", "p", "/x", false, 0, 0); return err },
		"MonitorAddConfig":   func() error { return c.MonitorAddConfig(MonitorProviderView{ID: "p"}) },
		"MonitorDelConfig":   func() error { return c.MonitorDelConfig(MonitorProviderView{ID: "p"}) },
		"MonitorStart":       func() error { return c.MonitorStart() },
		"ServiceDetail":      func() error { _, err := c.ServiceDetail("s-1", "s", ""); return err },
		"StartServiceByName": func() error { return c.StartServiceByName("s-1", "s", "") },
		"WGForwarders":       func() error { _, err := c.WGForwarders("s-1"); return err },
		"WGStartPortForward": func() error { _, _, err := c.WGStartPortForward("s-1", 1, "r"); return err },
		"WGStopPortForward":  func() error { _, _, err := c.WGStopPortForward("s-1", 1); return err },
		"WGSocksServers":     func() error { _, err := c.WGSocksServers("s-1"); return err },
		"WGStartSocks":       func() error { _, _, err := c.WGStartSocks("s-1", 1); return err },
		"WGStopSocks":        func() error { _, _, err := c.WGStopSocks("s-1", 1); return err },
		"ReconfigureSession": func() error { return c.ReconfigureSession("s-1", 60) },
		"ShellcodeEncode":    func() error { _, err := c.ShellcodeEncode("xor", "amd64", []byte("s"), 1, nil); return err },
	}

	for name, call := range calls {
		err := call()
		if err == nil {
			t.Errorf("%s: Response.Err was dropped", name)
			continue
		}
		if err.Error() != "server said no" {
			t.Errorf("%s: error = %q, want the server's message", name, err.Error())
		}
	}
}
