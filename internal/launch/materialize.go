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
		if err := verifyExecutableFormat(target); err == nil {
			_ = os.Chmod(target, 0o755)
			return target, nil
		}
		// A file that exists but is not an executable for this host is either
		// a leftover from an earlier build or a truncated extraction. Both are
		// safe to discard: the name is content-addressed, so re-extracting
		// produces exactly the file this name promises.
		log.Printf("[launch] discarding %s: not a %s executable, re-extracting", target, runtime.GOOS)
		_ = os.Remove(target)
	}

	log.Printf("[launch] extracting embedded sliver server -> %s", target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp := target + ".part"
	if err := writeGzip(embed.Payload, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("extract embedded server: %w", err)
	}
	if err := verifyExecutableFormat(tmp); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("embedded server payload does not match this host: %w (build carries %s)",
			err, embeddedPlatforms())
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return "", err
	}
	return target, nil
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
	magicELF   = []byte{0x7F, 'E', 'L', 'F'}
	magicPE    = []byte{'M', 'Z'}
	magicMachO = []byte{0xFE, 0xED, 0xFA, 0xCE}
	magicMachO64 = []byte{0xFE, 0xED, 0xFA, 0xCF}
)

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
	return nil
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
func writeGzip(payload []byte, path string) error {
	zr, err := gzip.NewReader(newByteReader(payload))
	if err != nil {
		return err
	}
	defer zr.Close()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
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
