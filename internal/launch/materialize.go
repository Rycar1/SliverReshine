package launch

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"c2tool/internal/embed"
)

// MaterializeServer resolves the sliver-server binary to execute.
//
// Resolution order:
//
//  1. an already-extracted copy inside the launcher's own state directory
//     (fast path — decompression only happens once per embedded payload)
//  2. the gzip payload baked into the binary by the `embedserver` build tag,
//     for the platform this launcher was compiled for
//  3. a sliver-server sitting next to the executable, then $PATH
//
// Step 3 is what keeps development builds usable without the embed tag.
//
// Every candidate is format-checked before it is returned. The check exists
// because a launcher carrying a payload for the wrong platform — a Windows
// launcher holding the Linux server, say — used to reach exec() and fail with
// "This version of %1 is not compatible with the version of Windows you're
// running". That message names neither the payload nor the platform, so the
// cause was invisible. Failing here instead makes the launcher say what it has
// and what it needs.
func MaterializeServer() (string, error) {
	if len(embed.Payload) == 0 {
		return findExternalServer()
	}

	dir, err := launcherStateDir()
	if err != nil {
		return "", err
	}

	// The payload digest names the extracted file, so shipping a new server
	// binary transparently replaces the old one instead of reusing it.
	sum := sha256.Sum256(embed.Payload)
	name := "sliver-server-" + hex.EncodeToString(sum[:8])
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(dir, name)

	if st, err := os.Stat(target); err == nil && st.Size() > 0 {
		if reuseExtractedServer(target) {
			return target, nil
		}
		// A file that exists but is neither a host executable nor the expected
		// content is a leftover from an earlier build, a truncated extraction, or
		// something placed here deliberately. All are safe to discard: the name
		// is content-addressed, so re-extracting produces exactly the file this
		// name promises.
		log.Printf("[launch] discarding %s: not a usable %s server, re-extracting", target, runtime.GOOS)
		_ = os.Remove(target)
	}

	if err := extractEmbeddedServer(dir, target); err != nil {
		return "", err
	}
	return target, nil
}

// reuseExtractedServer reports whether the file already extracted at target is
// the server this launcher carries, marking it executable when it is.
//
// The file is verified against the payload it claims to be, not merely
// inspected for an executable header.
//
// The name is content-addressed and the launcher logs it, so an attacker who
// can create a file in this directory knows exactly what to call it. Four magic
// bytes are then trivially satisfied by any PE or ELF, and the file is executed
// as `sliver-server daemon`, `unpack` and `operator`. MkdirAll(0o700) does not
// narrow anything on Windows (Chmod there only toggles the read-only bit) and
// never tightens a pre-existing directory.
//
// Hashing the whole file costs one pass over it at startup, which is the price
// of not exec'ing an arbitrary binary.
func reuseExtractedServer(target string) bool {
	if err := verifyExecutableFormat(target); err != nil {
		return false
	}
	if err := verifyExtractedContent(target); err != nil {
		log.Printf("[launch] discarding %s: %v", target, err)
		return false
	}
	// A failed chmod is not fatal -- on Windows it only toggles the read-only
	// bit -- but on unix it leaves a server that cannot be executed, and the
	// launcher would otherwise report only that the process exited. Logging it
	// names the cause here.
	if err := os.Chmod(target, 0o755); err != nil {
		log.Printf("[launch] could not mark %s executable: %v", target, err)
	}
	return true
}

// extractEmbeddedServer unpacks the embedded payload into target, verifying
// the result before it is handed back as the server to execute.
func extractEmbeddedServer(dir, target string) error {
	log.Printf("[launch] extracting embedded sliver server -> %s", target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// A unique temp name in the same directory, created with O_EXCL so an
	// existing file or a symlink placed at a predictable path cannot be
	// truncated or followed. The old name was target+".part" -- fixed, and
	// written with O_CREATE|O_TRUNC|O_WRONLY, so a hardlink planted there had
	// its inode rewritten with the extracted server.
	tmp, err := tempExtractPath(dir)
	if err != nil {
		return err
	}
	if err := writeGzip(embed.Payload, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("extract embedded server: %w", err)
	}
	if err := verifyExecutableFormat(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("embedded server payload does not match this host: %w (build carries %s)",
			err, embeddedPlatforms())
	}
	// The freshly written file is checked against the payload too, so a truncated
	// or corrupted extraction is caught here rather than becoming a binary that is
	// reused and executed on every later start.
	if err := verifyExtractedContent(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("extracted server does not match the embedded payload: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(target, 0o755)
}

// embeddedPlatforms renders the platforms this launcher carries, for error
// messages. It reads as "windows/amd64" or "none".
func embeddedPlatforms() string {
	if len(embed.Embedded) == 0 {
		return "none"
	}
	return strings.Join(embed.Embedded, ", ")
}

// Executable magic numbers. Only the formats the launcher can actually be built
// for are recognised: an unrecognised file is rejected rather than assumed good.
var (
	magicELF     = []byte{0x7F, 'E', 'L', 'F'}
	magicPE      = []byte{'M', 'Z'}
	magicMachO   = []byte{0xFE, 0xED, 0xFA, 0xCE}
	magicMachO64 = []byte{0xFE, 0xED, 0xFA, 0xCF}
)

// embeddedServerDigest is the SHA-256 of the *uncompressed* server binary the
// launcher carries. It is computed once from the embedded payload, which is the
// same bytes the extraction produces, so a reused file can be compared against it.
var embeddedServerDigest, embeddedServerSize = func() ([]byte, int64) {
	if len(embed.Payload) == 0 {
		return nil, 0
	}
	zr, err := gzip.NewReader(bytes.NewReader(embed.Payload))
	if err != nil {
		return nil, 0
	}
	defer zr.Close()
	h := sha256.New()
	n, err := io.Copy(h, zr)
	if err != nil {
		return nil, 0
	}
	return h.Sum(nil), n
}()

// verifyExtractedContent reports whether the file at path is byte-identical to
// the server this launcher carries.
//
// It exists because the extracted file is executed, and its name is derived from
// the payload digest and printed in the log -- so the name is guessable and an
// executable-header check is not a defence. A digest comparison is.
//
// When the launcher carries no payload, or the digest could not be computed,
// there is nothing to compare against and the check does not fail: the caller
// only reaches here in the embedded-server path, and refusing there would turn a
// build without an embedded server into a startup failure.
func verifyExtractedContent(path string) error {
	if embeddedServerDigest == nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() != int64(embeddedServerSize) {
		return fmt.Errorf("size is %d bytes, expected %d", st.Size(), embeddedServerSize)
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), embeddedServerDigest) {
		return fmt.Errorf("sha256 does not match the embedded server")
	}
	return nil
}

// verifyExecutableFormat checks that path begins with a magic number the host
// OS can execute.
//
// The check is deliberately limited to the leading bytes: it is not a
// substitute for parsing the file, it exists to catch the one mistake that has
// actually happened — a server binary for a different operating system shipped
// inside the launcher. Rejecting a mismatch early turns an opaque loader error
// into a sentence that names the problem.
func verifyExecutableFormat(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	head := make([]byte, 4)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF {
		return fmt.Errorf("read %s: %w", path, err)
	}
	head = head[:n]

	switch runtime.GOOS {
	case "windows":
		if !bytes.HasPrefix(head, magicPE) {
			return fmt.Errorf("%s is not a Windows PE image (magic %s)", filepath.Base(path), hexMagic(head))
		}
	case "linux":
		if !bytes.HasPrefix(head, magicELF) {
			return fmt.Errorf("%s is not a Linux ELF image (magic %s)", filepath.Base(path), hexMagic(head))
		}
	case "darwin":
		if !bytes.HasPrefix(head, magicMachO) && !bytes.HasPrefix(head, magicMachO64) {
			return fmt.Errorf("%s is not a Mach-O image (magic %s)", filepath.Base(path), hexMagic(head))
		}
	default:
		// An unknown host cannot be checked; the kernel will have its own say.
		return nil
	}

	// A 64-bit host cannot execute a 32-bit image, and the failure mode is the
	// same opaque loader error. Checked from the ELF/PE headers directly rather
	// than by inspecting strings.
	if want64(runtime.GOARCH) && is32BitImage(head, path) {
		return fmt.Errorf("%s is a 32-bit image; this host is %s", filepath.Base(path), runtime.GOARCH)
	}

	// Bit width is not the only way two executables differ. An ARM64 PE is a
	// 64-bit PE32+ image, so it passes every check above on an amd64 host and
	// only fails once the loader has it -- with the message this function exists
	// to replace. The machine field is the one that says which CPU the image is
	// for, and it is what the reported failure was actually about.
	if err := verifyMachine(path, head); err != nil {
		return err
	}
	return nil
}

// PE COFF Machine values, and the ELF ones, for the architectures the launcher
// can be built for. The tables are explicit rather than a formula because the
// numbering is historical: PE has no relationship between 0x8664 and amd64 that
// a formula could recover, and guessing is how a wrong-but-plausible constant
// gets shipped.
const (
	peMachineI386  = 0x014C
	peMachineAMD64 = 0x8664
	peMachineARM   = 0x01C0
	peMachineARM64 = 0xAA64

	elfMachineI386  = 3   // EM_386
	elfMachineAMD64 = 62  // EM_X86_64
	elfMachineARM   = 40  // EM_ARM
	elfMachineARM64 = 183 // EM_AARCH64
)

// verifyMachine checks that the image is built for this host's CPU.
//
// An architecture with no entry in the table is not refused: the launcher can be
// built for a platform this table does not know, and refusing there would break
// a working deployment to catch a mistake it cannot make. The check is
// best-effort in the direction of silence, never in the direction of a false
// rejection.
func verifyMachine(path string, head []byte) error {
	var machine uint16
	var name string

	switch {
	case bytes.HasPrefix(head, magicPE):
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		// The Machine field is the first two bytes of the COFF header, which
		// starts right after the 4-byte PE signature.
		var off [4]byte
		if _, err := f.ReadAt(off[:], 0x3C); err != nil {
			return nil // Header unreadable; the caller has already accepted it.
		}
		peOff := int64(uint32(off[0]) | uint32(off[1])<<8 | uint32(off[2])<<16 | uint32(off[3])<<24)
		var m [2]byte
		if _, err := f.ReadAt(m[:], peOff+4); err != nil {
			return nil
		}
		machine = uint16(m[0]) | uint16(m[1])<<8
		name = peMachineName(machine)

	case bytes.HasPrefix(head, magicELF):
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		// e_machine is a two-byte field at offset 18, stored in the file's own
		// byte order. Only little-endian hosts are targeted here, which is what
		// makes reading it directly acceptable; a big-endian target would read
		// as its own swapped value and fall through to "table does not know it",
		// which is the safe direction.
		var m [2]byte
		if _, err := f.ReadAt(m[:], 18); err != nil {
			return nil
		}
		machine = uint16(m[0]) | uint16(m[1])<<8
		name = elfMachineName(machine)

	default:
		return nil
	}

	// 0 is never a valid machine and is what a short or odd file yields.
	if machine == 0 || name == "" {
		return nil
	}

	if !machineMatchesHost(machine) {
		return fmt.Errorf("%s is a %s image, but this host is %s; the embedded payload is for the wrong CPU architecture",
			filepath.Base(path), name, runtime.GOARCH)
	}
	return nil
}

// machineMatchesHost reports whether an image machine value is this host's.
//
// Several Go architectures map to one CPU family (386 and amd64 are both x86),
// but an image for either runs only on the matching width, which is32BitImage
// has already checked by the time this is called. So the comparison here is
// exact.
func machineMatchesHost(machine uint16) bool {
	switch runtime.GOARCH {
	case "amd64":
		return machine == peMachineAMD64 || machine == elfMachineAMD64
	case "386":
		return machine == peMachineI386 || machine == elfMachineI386
	case "arm64":
		return machine == peMachineARM64 || machine == elfMachineARM64
	case "arm":
		return machine == peMachineARM || machine == elfMachineARM
	default:
		// An architecture with no mapping cannot be verified, so it is not
		// refused. Returning false here would break every deployment on a
		// platform this table has not been taught about.
		return true
	}
}

func peMachineName(machine uint16) string {
	switch machine {
	case peMachineI386:
		return "386"
	case peMachineAMD64:
		return "amd64"
	case peMachineARM:
		return "arm"
	case peMachineARM64:
		return "arm64"
	default:
		return ""
	}
}

func elfMachineName(machine uint16) string {
	switch machine {
	case elfMachineI386:
		return "386"
	case elfMachineAMD64:
		return "amd64"
	case elfMachineARM:
		return "arm"
	case elfMachineARM64:
		return "arm64"
	default:
		return ""
	}
}

// hexMagic renders leading bytes for an error message: "4d 5a 90 00".
func hexMagic(b []byte) string {
	if len(b) == 0 {
		return "empty"
	}
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = hex.EncodeToString([]byte{c})
	}
	return strings.Join(parts, " ")
}

// want64 reports whether an architecture is 64-bit.
func want64(goarch string) bool {
	switch goarch {
	case "amd64", "arm64", "ppc64", "ppc64le", "riscv64", "s390x", "mips64", "mips64le", "loong64":
		return true
	}
	return false
}

// is32BitImage reads the class byte out of an ELF or PE header. Mach-O is
// already distinguished by its magic, so it is not handled here.
func is32BitImage(head []byte, path string) bool {
	switch {
	case bytes.HasPrefix(head, magicELF) && len(head) >= 4:
		// EI_CLASS is byte 4; it is outside the 4 bytes read above, so the
		// file is reopened. This branch is rare (only reached for ELF hosts).
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		var class [1]byte
		if _, err := f.ReadAt(class[:], 4); err != nil {
			return false
		}
		return class[0] == 1 // ELFCLASS32
	case bytes.HasPrefix(head, magicPE):
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		var off [4]byte
		if _, err := f.ReadAt(off[:], 0x3C); err != nil {
			return false
		}
		peOff := int64(uint32(off[0]) | uint32(off[1])<<8 | uint32(off[2])<<16 | uint32(off[3])<<24)
		var magic [2]byte
		if _, err := f.ReadAt(magic[:], peOff+24); err != nil {
			return false
		}
		// 0x10B is PE32, 0x20B is PE32+.
		return magic[0] == 0x0B && magic[1] == 0x01
	}
	return false
}

// writeGzip decompresses a gzip stream to path.
// tempExtractPath reserves a unique file in dir for the extraction.
//
// O_EXCL is the point: it fails if the path already exists, so a file or symlink
// planted at the name cannot be opened and truncated. The name is unpredictable,
// which is what makes the exclusive create meaningful rather than a race against
// a guessable path.
func tempExtractPath(dir string) (string, error) {
	f, err := os.CreateTemp(dir, ".sliver-server-*.part")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func writeGzip(payload []byte, path string) error {
	zr, err := gzip.NewReader(newByteReader(payload))
	if err != nil {
		return err
	}
	defer zr.Close()

	// The file already exists -- it was created exclusively by tempExtractPath --
	// so this opens it for writing without O_CREATE, and 0600 until the rename,
	// rather than leaving a world-readable partially-written binary in place.
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, zr); err != nil {
		return err
	}
	return f.Sync()
}

// findExternalServer looks for a sliver-server binary beside the launcher and
// then on PATH. Used by development builds, which embed no payload.
//
// Candidates are still format-checked, so a development launcher on Windows
// does not pick up a Linux server binary that happens to share the directory —
// the same mistake the release path used to make.
func findExternalServer() (string, error) {
	names := []string{"sliver-server"}
	if runtime.GOOS == "windows" {
		names = []string{"sliver-server.exe", "sliver-server"}
	}

	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}

	var rejected []string
	for _, dir := range dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if st, err := os.Stat(candidate); err != nil || st.IsDir() {
				continue
			}
			if err := verifyExecutableFormat(candidate); err != nil {
				rejected = append(rejected, err.Error())
				continue
			}
			return candidate, nil
		}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			if err := verifyExecutableFormat(p); err != nil {
				rejected = append(rejected, err.Error())
				continue
			}
			return p, nil
		}
	}

	if len(rejected) > 0 {
		return "", fmt.Errorf("no usable sliver-server for %s: %s; build with -tags embedserver for a self-contained binary",
			platformAssetName(), strings.Join(rejected, "; "))
	}
	return "", fmt.Errorf("no embedded sliver-server payload for %s and no sliver-server binary next to the launcher or on PATH (build with -tags embedserver for a self-contained binary)",
		platformAssetName())
}

// launcherStateDir is where the embedded payload is unpacked.
//
// It sits inside the launcher's own directory — <exe dir>/data/bin — rather
// than in the OS cache. The console is deployed by copying one file to a host
// and running it, and an operator who does that expects everything it creates,
// including the unpacked server, to land next to that file: that is what makes
// the whole install relocatable, removable and inspectable in one place. The
// OS cache hid the extracted 300 MB binary in a per-user directory that
// differed per platform, which made "why is it running that server" hard to
// answer.
//
// C2TOOL_HOME still overrides it, and a directory that cannot be written falls
// back to the OS cache rather than refusing to start: a launcher read from a
// read-only medium still has to work.
func launcherStateDir() (string, error) {
	if dir := os.Getenv("C2TOOL_HOME"); dir != "" {
		return filepath.Join(dir, "bin"), nil
	}

	if base, ok := executableDir(); ok {
		dir := filepath.Join(base, "data", "bin")
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir, nil
		} else {
			log.Printf("[launch] cannot use %s (%v); falling back to the user cache", dir, err)
		}
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "c2tool", "bin"), nil
}

// HomeDir resolves the state directory the console uses, preferring one beside
// the executable.
//
// The order is: C2TOOL_HOME, then <exe dir>/data, then ~/.c2tool. Placing the
// state beside the binary is what makes a deployment relocatable — the whole
// install is one directory that can be tarred up, copied to another host and
// run — and it keeps the config, the login record and the loot together with
// the thing that produced them.
//
// The home directory remains the fallback for the case that actually needs it:
// a binary that lives somewhere unwritable (a read-only mount, a system path)
// still has to start.
func HomeDir() (string, error) {
	if v := os.Getenv("C2TOOL_HOME"); v != "" {
		return v, nil
	}
	if base, ok := executableDir(); ok {
		dir := filepath.Join(base, "data")
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir, nil
		} else {
			log.Printf("[launch] cannot use %s (%v); falling back to the home directory", dir, err)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".c2tool"), nil
}

// executableDir returns the directory the running binary lives in.
//
// os.Executable resolves symlinks, which is what we want: a launcher reached
// through a symlink should keep its state beside the real file, not beside the
// link. The one case it gets wrong is a binary replaced while running, which
// is not a state this console has to survive.
func executableDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(exe)
	if dir == "" {
		return "", false
	}
	return dir, true
}
