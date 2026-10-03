package sliver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bishopfox/sliver/protobuf/clientpb"
)

// A map rather than a switch, so that "is this a format I know?" and "which
// enum is it?" are the same question. The switch it replaces had no default,
// which meant an unrecognised format silently became EXECUTABLE -- the request
// was answered with a payload, just not the one that was asked for.
var outputFormats = map[string]clientpb.OutputFormat{
	"exe":        clientpb.OutputFormat_EXECUTABLE,
	"executable": clientpb.OutputFormat_EXECUTABLE,
	"service":    clientpb.OutputFormat_SERVICE,
	"shellcode":  clientpb.OutputFormat_SHELLCODE,
	"shared":     clientpb.OutputFormat_SHARED_LIB,
}

// buildTargets are the operating systems and architectures the console offers.
//
// Held here rather than only in the frontend because the API is reachable
// directly, and a request that names a platform the toolchain cannot build
// should fail with that fact rather than a compiler message from three layers
// down.
var buildTargets = map[string][]string{
	"windows": {"amd64", "386", "arm64"},
	"linux":   {"amd64", "386", "arm64", "arm"},
	"darwin":  {"amd64", "arm64"},
	"freebsd": {"amd64", "386", "arm64", "arm"},
}

// shellcodeArches records which architectures each OS can produce shellcode for.
//
// Sliver enforces this per platform in its own builders, with three different
// rules, and none of them are visible from the console's side:
//
//	windows  amd64, 386        (arms64 and arm are refused)
//	linux    amd64, arm64      (386 and arm are refused)
//	darwin   arm64 only        (amd64 is refused)
//	freebsd  none
//
// Without this the console offered "windows/arm64 shellcode" and the operator
// got "windows shellcode format is only supported for amd64 and 386
// architectures" -- after waiting for a build that was never possible. The
// frontend narrows its dropdown from the same table so the combination cannot
// be chosen at all.
var shellcodeArches = map[string][]string{
	"windows": {"amd64", "386"},
	"linux":   {"amd64", "arm64"},
	"darwin":  {"arm64"},
	"freebsd": {},
}

// sharedLibArches records the targets that can actually produce a c-shared
// library.
//
// The matrix run found five combinations that the console offered, accepted,
// and then failed with a bare "rpc error: code = Internal desc = exit status 1"
// after a compiler ran. Three separate causes, none of them a defect in this
// code, and all of them avoidable before the build starts:
//
//  1. Sliver's zig target table has no entry for linux/arm or any freebsd
//     target, so the CC it builds is "zig cc -target " with an empty target and
//     the compiler dies with "error: unknown architecture: ”".
//
//  2. Darwin cross-compilation shells out to a hardcoded osxcross path
//     (/opt/osxcross/...) that exists in Sliver's own Linux build container and
//     nowhere else -- certainly not on a Windows host running this console.
//
//  3. The Go toolchain itself does not support -buildmode=c-shared on
//     freebsd/386.
//
// Only windows and linux/amd64, linux/arm64 are reachable here. Notably
// linux/386 shared also depends on zig, which does have a target for it.
var sharedLibArches = map[string][]string{
	"windows": {"amd64", "386", "arm64"},
	"linux":   {"amd64", "386", "arm64"},
	"darwin":  {},
	"freebsd": {},
}

// validateBuildRequest reports the first thing about a request that cannot be
// built, or an empty string when it is fine.
//
// The message names the accepted values. An operator who typed "plan9" or
// "rom" needs to know what the alternatives are, and "invalid target" alone
// sends them to the source.
func validateBuildRequest(os, arch, format string) string {
	if os != "" {
		arches, ok := buildTargets[strings.ToLower(os)]
		if !ok {
			return fmt.Sprintf("unsupported os %q; supported: %s", os, sortedKeys(buildTargets))
		}
		if arch != "" {
			found := false
			for _, a := range arches {
				if strings.EqualFold(a, arch) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Sprintf("unsupported arch %q for %s; supported: %s", arch, os, strings.Join(arches, ", "))
			}
		}
	}
	if format != "" {
		if _, ok := outputFormats[strings.ToLower(format)]; !ok {
			return fmt.Sprintf("unsupported format %q; supported: %s", format, sortedKeys(outputFormats))
		}
	}

	// The format and the target have to be checked together: shellcode is
	// offered for every OS but only builds on some architectures of each.
	if strings.EqualFold(format, "shellcode") && os != "" && arch != "" {
		arches, ok := shellcodeArches[strings.ToLower(os)]
		if ok {
			allowed := false
			for _, a := range arches {
				if strings.EqualFold(a, arch) {
					allowed = true
					break
				}
			}
			if !allowed {
				if len(arches) == 0 {
					return fmt.Sprintf("%s does not support the shellcode format; use exe or shared", os)
				}
				return fmt.Sprintf("%s shellcode is not supported on %s; supported: %s",
					os, arch, strings.Join(arches, ", "))
			}
		}
	}

	// A shared library needs a C toolchain, and which ones this deployment can
	// reach is narrower than the target list. Checked here so the operator gets
	// a sentence instead of a compiler's exit code.
	if strings.EqualFold(format, "shared") && os != "" && arch != "" {
		arches, ok := sharedLibArches[strings.ToLower(os)]
		if ok {
			allowed := false
			for _, a := range arches {
				if strings.EqualFold(a, arch) {
					allowed = true
					break
				}
			}
			if !allowed {
				if len(arches) == 0 {
					return fmt.Sprintf("%s shared libraries cannot be built by this console "+
						"(they need an osxcross toolchain that exists only in Sliver's own build "+
						"container); use exe or shellcode", os)
				}
				return fmt.Sprintf("%s shared libraries are not supported on %s; supported: %s. "+
					"linux/arm and freebsd need a zig C target that this build does not define, "+
					"and freebsd/386 is refused by the Go toolchain itself",
					os, arch, strings.Join(arches, ", "))
			}
		}
	}
	return ""
}

// sortedKeys renders a map's keys for a message, in a stable order so the same
// mistake produces the same sentence every time.
func sortedKeys[V any](m map[string]V) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
