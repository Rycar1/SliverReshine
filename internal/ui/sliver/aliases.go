package sliver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// AliasDir is the directory where installed alias bundles are stored. The
// server process must be able to read/write this path.
var AliasDir = "aliases"

// AliasFile is one OS/Arch-specific artifact of an alias bundle.
type AliasFile struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Path string `json:"path"`
}

// AliasManifest mirrors the official alias.json schema.
type AliasManifest struct {
	Name           string       `json:"name"`
	Version        string       `json:"version"`
	CommandName    string       `json:"command_name"`
	OriginalAuthor string       `json:"original_author"`
	RepoURL        string       `json:"repo_url"`
	Help           string       `json:"help"`
	LongHelp       string       `json:"long_help"`
	Entrypoint     string       `json:"entrypoint"`
	AllowArgs      bool         `json:"allow_args"`
	DefaultArgs    string       `json:"default_args"`
	Files          []*AliasFile `json:"files"`
	IsReflective   bool         `json:"is_reflective"`
	IsAssembly     bool         `json:"is_assembly"`
}

// AliasView is the JSON shape returned by the API.
type AliasView struct {
	Name           string   `json:"Name"`
	Version        string   `json:"Version"`
	CommandName    string   `json:"CommandName"`
	OriginalAuthor string   `json:"OriginalAuthor"`
	RepoURL        string   `json:"RepoURL"`
	Help           string   `json:"Help"`
	Entrypoint     string   `json:"Entrypoint"`
	AllowArgs      bool     `json:"AllowArgs"`
	DefaultArgs    string   `json:"DefaultArgs"`
	Platforms      []string `json:"Platforms"`
	IsAssembly     bool     `json:"IsAssembly"`
	IsReflective   bool     `json:"IsReflective"`
}

var defaultAliasHostProc = map[string]string{
	"windows": `c:\windows\system32\notepad.exe`,
	"linux":   "/bin/bash",
	"darwin":  "/Applications/Safari.app/Contents/MacOS/SafariForWebKitDevelopment",
}

func aliasView(m *AliasManifest) AliasView {
	platforms := map[string]struct{}{}
	for _, f := range m.Files {
		if f != nil {
			platforms[f.OS+"/"+f.Arch] = struct{}{}
		}
	}
	plats := make([]string, 0, len(platforms))
	for p := range platforms {
		plats = append(plats, p)
	}
	return AliasView{
		Name:           m.Name,
		Version:        m.Version,
		CommandName:    m.CommandName,
		OriginalAuthor: m.OriginalAuthor,
		RepoURL:        m.RepoURL,
		Help:           m.Help,
		Entrypoint:     m.Entrypoint,
		AllowArgs:      m.AllowArgs,
		DefaultArgs:    m.DefaultArgs,
		Platforms:      plats,
		IsAssembly:     m.IsAssembly,
		IsReflective:   m.IsReflective,
	}
}

func aliasManifestPath(name string) string {
	return filepath.Join(AliasDir, name, "alias.json")
}

// ListAliases returns manifests for all installed aliases.
func ListAliases() ([]AliasView, error) {
	entries, err := os.ReadDir(AliasDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []AliasView{}, nil
		}
		return nil, err
	}
	out := []AliasView{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(AliasDir, e.Name(), "alias.json"))
		if err != nil {
			continue
		}
		m := &AliasManifest{}
		if err := json.Unmarshal(data, m); err != nil {
			continue
		}
		out = append(out, aliasView(m))
	}
	return out, nil
}

// InstallAlias installs an alias from a base64-encoded .tar.gz bundle
// containing alias.json plus the referenced artifacts.
func InstallAlias(bundleB64 string) (*AliasView, error) {
	bundle, err := base64.StdEncoding.DecodeString(bundleB64)
	if err != nil {
		return nil, errors.New("invalid bundle base64")
	}
	manifestData, files, err := readAliasTarGz(bundle)
	if err != nil {
		return nil, err
	}
	manifest := &AliasManifest{}
	if err := json.Unmarshal(manifestData, manifest); err != nil {
		return nil, err
	}
	if manifest.Name == "" || manifest.CommandName == "" {
		return nil, errors.New("invalid alias.json: name and command_name are required")
	}

	// The command name is the install directory, and it comes from a file inside
	// the uploaded archive -- so it is attacker-controlled by anyone who can hand
	// the operator a bundle. Validated before the RemoveAll below, because
	// discovering the name is unsafe after the delete has already run is not a
	// recovery, it is a second incident.
	if err := validateArtifactName(manifest.CommandName); err != nil {
		return nil, fmt.Errorf("invalid alias.json: command_name %q: %w", manifest.CommandName, err)
	}

	installPath := filepath.Join(AliasDir, manifest.CommandName)
	if err := mustStayInside(AliasDir, installPath); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(installPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(installPath, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(installPath, "alias.json"), manifestData, 0o600); err != nil {
		os.RemoveAll(installPath)
		return nil, err
	}

	for _, f := range manifest.Files {
		if f == nil || f.Path == "" {
			continue
		}
		data, ok := files[strings.TrimPrefix(f.Path, "/")]
		if !ok {
			os.RemoveAll(installPath)
			return nil, fmt.Errorf("alias artifact not found in bundle: %s", f.Path)
		}
		rel, err := safeAliasRelPath(f.Path)
		if err != nil {
			os.RemoveAll(installPath)
			return nil, err
		}
		dst := filepath.Join(installPath, rel)
		if _, err := os.Stat(filepath.Dir(dst)); os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(dst), 0o700)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			os.RemoveAll(installPath)
			return nil, err
		}
	}

	v := aliasView(manifest)
	return &v, nil
}

// safeAliasRelPath resolves a manifest file path into a safe relative path
// that cannot escape the install directory.
//
// The paths come from alias.json inside a bundle, written by whoever authored the
// alias rather than by the operator, so they are untrusted input.
//
// A Windows-style separator is normalised to '/' before anything else reads the
// string. path.Clean and the component split below both understand only '/', so
// `..\..\evil.exe` used to survive as a single component that is not equal to
// `..`, and filepath.FromSlash left the backslashes alone -- so the caller's
// filepath.Join then resolved them as separators and wrote outside the install
// directory. Forward-slash traversal was already safe because path.Clean
// collapses it; only the backslash form escaped.
//
// The two checks at the end cover what a component check cannot: a drive letter
// or UNC prefix is an ordinary component to path.Clean, but filepath treats it as
// a volume and it would replace the install directory entirely.
func safeAliasRelPath(p string) (string, error) {
	slashed := strings.ReplaceAll(p, `\`, "/")
	cleaned := path.Clean("/" + strings.TrimPrefix(slashed, "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "", errors.New("invalid alias artifact path")
	}
	parts := strings.Split(cleaned, "/")
	for _, part := range parts {
		if part == ".." {
			return "", errors.New("invalid alias artifact path")
		}
	}
	rel := filepath.FromSlash(cleaned)
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("invalid alias artifact path %q: must be relative to the alias directory", p)
	}
	return rel, nil
}

// Decompression limits for an alias bundle.
//
// The request body limit bounds the COMPRESSED size, and gzip reaches ratios of
// a thousand to one on repetitive data -- so a bundle comfortably inside that
// limit could still expand to more memory than the console has. A console that
// runs out of memory is a console the operator has lost mid-engagement, which is
// why these are hard caps rather than a warning.
//
// The numbers are generous against real bundles. The largest alias in the
// official armory is a few megabytes of .NET assembly; nothing legitimate comes
// close to any of these.
const (
	// maxAliasBundleBytes caps the total decompressed payload.
	maxAliasBundleBytes = 64 << 20
	// maxAliasEntryBytes caps one extracted file.
	maxAliasEntryBytes = 32 << 20
	// maxAliasBundleFiles caps the entry count, so a bundle of a million tiny
	// files cannot exhaust memory through per-entry overhead instead of bytes.
	maxAliasBundleFiles = 256
)

// readAliasTarGz extracts alias.json and every file from a tar.gz bundle.
//
// Every read is bounded. io.LimitReader is used rather than a counter so the
// limit is enforced on the read itself: a counter checked afterwards would
// already have allocated the memory the limit exists to protect.
func readAliasTarGz(bundle []byte) ([]byte, map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return nil, nil, errors.New("invalid gzip bundle")
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	var manifest []byte
	var total int64

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, errors.New("invalid tar bundle")
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		name = strings.TrimPrefix(name, "/")
		if name == "" || hdr.Typeflag != tar.TypeReg {
			continue
		}

		if len(files) >= maxAliasBundleFiles {
			return nil, nil, fmt.Errorf("bundle has more than %d files", maxAliasBundleFiles)
		}

		// One byte past the cap is read on purpose: reaching it proves the entry
		// is over the limit, where reading exactly the cap cannot tell "exactly
		// at the limit" from "truncated by the reader".
		data, err := io.ReadAll(io.LimitReader(tr, maxAliasEntryBytes+1))
		if err != nil {
			return nil, nil, err
		}
		if int64(len(data)) > maxAliasEntryBytes {
			return nil, nil, fmt.Errorf("bundle file %q is larger than %d bytes", name, maxAliasEntryBytes)
		}

		total += int64(len(data))
		if total > maxAliasBundleBytes {
			return nil, nil, fmt.Errorf("bundle expands to more than %d bytes", maxAliasBundleBytes)
		}

		if name == "alias.json" {
			manifest = data
		} else {
			files[name] = data
		}
	}
	if len(manifest) == 0 {
		return nil, nil, errors.New("no alias.json found in bundle")
	}
	return manifest, files, nil
}

// RemoveAlias deletes an installed alias.
//
// The name reaches os.RemoveAll, and this function used to join it onto AliasDir
// with nothing in between: a name of "../../../etc" escaped the directory and the
// recursive delete ran on whatever it landed on. The validator now runs before
// the join rather than after, so there is no escaped path left to reason about.
func RemoveAlias(name string) error {
	if err := validateArtifactName(name); err != nil {
		return fmt.Errorf("alias name %q: %w", name, err)
	}

	installPath := filepath.Join(AliasDir, name)

	// Belt and braces: even with the allowlist above, refuse to act on a path
	// that did not end up inside AliasDir. This is the guard that survives a
	// future edit to the validator, or AliasDir being pointed somewhere odd by an
	// embedder -- which is the situation that produced the original bug.
	if err := mustStayInside(AliasDir, installPath); err != nil {
		return err
	}

	if err := os.RemoveAll(installPath); err != nil {
		return err
	}
	return nil
}

// mustStayInside reports an error when child is not inside parent.
//
// Both paths are made absolute first, because "aliases/x" is not lexically
// inside the relative "aliases" as far as filepath.Rel is concerned once either
// side is absolute. The check is lexical rather than symlink-resolved on purpose:
// this is a last line of defence behind an allowlist, not the primary control,
// and resolving symlinks here would make the guard depend on filesystem state an
// attacker can change between the check and the call.
func mustStayInside(parent, child string) error {
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", parent, err)
	}
	absChild, err := filepath.Abs(child)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", child, err)
	}

	rel, err := filepath.Rel(absParent, absChild)
	if err != nil {
		return fmt.Errorf("refusing to act on %s: it is not inside %s", absChild, absParent)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to act on %s: it is not inside %s", absChild, absParent)
	}
	return nil
}

// RunAlias executes an installed alias against a session, dispatching to
// ExecuteAssembly / SpawnDll / Sideload based on the manifest.
func (c *Client) RunAlias(sessionID, name, args, process, arch, method, class string) (*AliasView, map[string]any, error) {
	data, err := os.ReadFile(aliasManifestPath(name))
	if err != nil {
		return nil, nil, fmt.Errorf("alias %q is not installed", name)
	}
	manifest := &AliasManifest{}
	if err := json.Unmarshal(data, manifest); err != nil {
		return nil, nil, err
	}

	sessions, err := c.Sessions()
	if err != nil {
		return nil, nil, err
	}
	var targetOS, targetArch string
	for _, s := range sessions {
		if s.ID == sessionID {
			targetOS = s.OS
			targetArch = s.Arch
			break
		}
	}
	if targetOS == "" {
		return nil, nil, fmt.Errorf("session %s not found", sessionID)
	}

	var binRel string
	for _, f := range manifest.Files {
		if f != nil && strings.EqualFold(f.OS, targetOS) && strings.EqualFold(f.Arch, targetArch) {
			binRel = f.Path
			break
		}
	}
	if binRel == "" {
		return nil, nil, fmt.Errorf("no alias file for %s/%s", targetOS, targetArch)
	}
	rel, err := safeAliasRelPath(binRel)
	if err != nil {
		return nil, nil, err
	}
	binData, err := os.ReadFile(filepath.Join(AliasDir, name, rel))
	if err != nil {
		return nil, nil, fmt.Errorf("alias file not found: %s", binRel)
	}

	extArgs := strings.Join(strings.Fields(args), " ")
	if extArgs == "" {
		extArgs = manifest.DefaultArgs
	}
	if process == "" {
		process = defaultAliasHostProc[targetOS]
	}
	isDLL := strings.EqualFold(filepath.Ext(binRel), ".dll")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	request := &commonpb.Request{SessionID: sessionID}

	output := ""
	if manifest.IsAssembly {
		if arch == "" {
			arch = "x84"
		}
		resp, err := c.RPC.ExecuteAssembly(ctx, &sliverpb.ExecuteAssemblyReq{
			Assembly:  binData,
			Arguments: []string{extArgs},
			Process:   process,
			IsDLL:     isDLL,
			Arch:      arch,
			Method:    method,
			ClassName: class,
			Request:   request,
		})
		if err != nil {
			return nil, nil, err
		}
		if resp.Response != nil && resp.Response.Err != "" {
			return nil, nil, errors.New(resp.Response.Err)
		}
		output = string(resp.Output)
	} else if manifest.IsReflective {
		resp, err := c.RPC.SpawnDll(ctx, &sliverpb.InvokeSpawnDllReq{
			Data:        binData,
			Args:        []string{strings.TrimSpace(extArgs)},
			ProcessName: process,
			EntryPoint:  manifest.Entrypoint,
			Kill:        true,
			Request:     request,
		})
		if err != nil {
			return nil, nil, err
		}
		if resp.Response != nil && resp.Response.Err != "" {
			return nil, nil, errors.New(resp.Response.Err)
		}
		output = resp.Result
	} else {
		resp, err := c.RPC.Sideload(ctx, &sliverpb.SideloadReq{
			Data:        binData,
			Args:        []string{extArgs},
			EntryPoint:  manifest.Entrypoint,
			ProcessName: process,
			IsDLL:       isDLL,
			Kill:        true,
			Request:     request,
		})
		if err != nil {
			return nil, nil, err
		}
		if resp.Response != nil && resp.Response.Err != "" {
			return nil, nil, errors.New(resp.Response.Err)
		}
		output = resp.Result
	}

	v := aliasView(manifest)
	return &v, map[string]any{
		"output":   output,
		"mode":     aliasMode(manifest),
		"command":  manifest.CommandName,
		"args":     extArgs,
		"process":  process,
		"platform": targetOS + "/" + targetArch,
	}, nil
}

func aliasMode(m *AliasManifest) string {
	if m.IsAssembly {
		return "assembly"
	}
	if m.IsReflective {
		return "dll"
	}
	return "sideload"
}
