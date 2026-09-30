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

	installPath := filepath.Join(AliasDir, manifest.CommandName)
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
func safeAliasRelPath(p string) (string, error) {
	cleaned := path.Clean("/" + strings.TrimPrefix(p, "/"))
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
	return filepath.FromSlash(cleaned), nil
}

// readAliasTarGz extracts alias.json and every file from a tar.gz bundle.
func readAliasTarGz(bundle []byte) ([]byte, map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return nil, nil, errors.New("invalid gzip bundle")
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	var manifest []byte
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
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, err
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
func RemoveAlias(name string) error {
	installPath := filepath.Join(AliasDir, name)
	if err := os.RemoveAll(installPath); err != nil {
		return err
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
