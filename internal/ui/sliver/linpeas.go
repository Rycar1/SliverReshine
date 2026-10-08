package sliver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The enumeration helper the privilege-escalation run uploads.
//
// A privesc run is only as good as its enumeration, and the PEASS-ng helpers
// are the ones operators already reach for. Rather than shipping a copy in the
// binary -- which would age badly and bloat every build -- the console fetches
// the current release asset, keeps it in the operator's cache directory, and
// uploads it to the target. The download is therefore part of starting a run,
// and a run that cannot reach the release says so and falls back to the model
// enumerating by hand.
//
// The tool is chosen by the target's platform, because there is no one binary
// that covers both: a POSIX target gets the linpeas shell script and a Windows
// target gets a winPEAS executable.

// linpeasReleaseBase is the PEASS-ng release download root. "latest/download"
// is a permanent redirect to the newest tag, so the console never has to know
// a version number.
const linpeasReleaseBase = "https://github.com/peass-ng/PEASS-ng/releases/latest/download/"

const (
	// linpeasCacheTTL is how long a fetched asset is reused. The release
	// changes on the order of weeks, and re-downloading eleven megabytes of
	// winPEAS on every run would make starting a run feel broken.
	linpeasCacheTTL = 12 * time.Hour
	// linpeasHTTPTimeout bounds the download itself. The largest asset is
	// about eleven megabytes; a slow link still finishes well inside this.
	linpeasHTTPTimeout = 5 * time.Minute
	// linpeasMaxBytes is the ceiling on one download, so a misbehaving
	// redirect cannot stream until the console runs out of memory.
	linpeasMaxBytes = 64 << 20
)

// linpeasBaseEnv names the environment variable that overrides where release
// assets are fetched from. A deployment whose egress cannot reach the release
// host points this at a mirror it trusts; the value is a comma-separated list
// and each entry is tried in order before the default root. Trust matters here:
// whatever the root serves is uploaded to a target and executed, so the console
// deliberately ships no mirror of its own and leaves the choice to the operator.
const linpeasBaseEnv = "SLIVERRESHINE_LINPEAS_BASE_URL"

// linpeasBases is the ordered list of download roots: the operator's entries
// first, the default release root last.
func linpeasBases() []string {
	var bases []string
	for _, part := range strings.Split(os.Getenv(linpeasBaseEnv), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.HasSuffix(part, "/") {
			part += "/"
		}
		bases = append(bases, part)
	}
	return append(bases, linpeasReleaseBase)
}

// linpeasReleaseURL is the address reported to the operator for an asset. It is
// the first configured root, so a deployment that mirrors the release reports
// the address it actually used rather than the upstream one.
func linpeasReleaseURL(tool linpeasTool) string {
	return linpeasBases()[0] + tool.Asset
}

// linpeasTool is one PEASS-ng release asset and how it is used on a target.
type linpeasTool struct {
	// Asset is the file name inside the release.
	Asset string
	// Name is the file name given to the copy on the target. It is short and
	// neutral on purpose: the target sees "linpeas.sh" or "linpeas.exe", not a
	// version string a defender could match against a known-tool feed.
	Name string
	// Windows selects the Windows command line for the uploaded copy.
	Windows bool
}

// linpeasToolFor picks the asset for a target platform.
//
// An unknown or empty OS falls back to the POSIX script: it is the common case
// for this console, and a Windows target whose OS field is momentarily empty
// still resolves through the explicit "windows" case once the session list is
// read again.
func linpeasToolFor(goos, arch string) (linpeasTool, error) {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		switch strings.ToLower(strings.TrimSpace(arch)) {
		case "386", "x86", "i386", "i686":
			return linpeasTool{Asset: "winPEASx86.exe", Name: "linpeas.exe", Windows: true}, nil
		case "arm64", "aarch64":
			// There is no native arm64 build; the "any" binary runs under the
			// emulation layer Windows ships for x86-64.
			return linpeasTool{Asset: "winPEASany.exe", Name: "linpeas.exe", Windows: true}, nil
		default:
			return linpeasTool{Asset: "winPEASx64.exe", Name: "linpeas.exe", Windows: true}, nil
		}
	case "linux", "darwin", "freebsd", "openbsd", "":
		return linpeasTool{Asset: "linpeas.sh", Name: "linpeas.sh"}, nil
	default:
		return linpeasTool{}, fmt.Errorf("no enumeration helper is published for %s/%s", goos, arch)
	}
}

// linpeasCacheDir is where fetched assets live between runs.
//
// It is the per-user cache directory rather than the state directory: the
// assets are reproducible downloads, not state, and a deployment that wipes
// its state directory should not have to fetch eleven megabytes again.
func linpeasCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "sliverreshine", "linpeas")
	}
	return filepath.Join(os.TempDir(), "sliverreshine-linpeas")
}

// fetchLinpeas returns the asset's bytes, downloading it unless a fresh copy is
// already cached. The cached flag tells the caller which happened so the run
// can say so rather than implying a download that did not occur.
//
// The bytes are stored by writeCache, which keeps a download interrupted
// halfway from becoming the copy the next run picks up.
//
// Every configured root is tried before giving up, and a stale cached copy is
// preferred over nothing: a run that cannot reach any root is better served by
// the previous release than by the model enumerating blind. The note says which
// of those happened, because "used the cached copy" and "used a copy from last
// week because the download failed" are different things to an operator.
func fetchLinpeas(ctx context.Context, tool linpeasTool, refresh bool) (data []byte, cached bool, note string, err error) {
	dir := linpeasCacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, "", fmt.Errorf("prepare cache directory: %w", err)
	}
	cachePath := filepath.Join(dir, tool.Asset)

	if !refresh {
		if fi, statErr := os.Stat(cachePath); statErr == nil && fi.Size() > 0 && time.Since(fi.ModTime()) < linpeasCacheTTL {
			if blob, readErr := os.ReadFile(cachePath); readErr == nil && len(blob) > 0 {
				return blob, true, "", nil
			}
		}
	}

	var failures []string
	for _, base := range linpeasBases() {
		blob, fetchErr := downloadLinpeas(ctx, base+tool.Asset)
		if fetchErr != nil {
			failures = append(failures, fetchErr.Error())
			continue
		}
		if err := writeCache(cachePath, blob); err != nil {
			return nil, false, "", fmt.Errorf("cache %s: %w", tool.Asset, err)
		}
		return blob, false, "", nil
	}

	if fi, statErr := os.Stat(cachePath); statErr == nil && fi.Size() > 0 {
		if blob, readErr := os.ReadFile(cachePath); readErr == nil && len(blob) > 0 {
			return blob, true, fmt.Sprintf("download failed (%s); using the copy cached on %s",
				strings.Join(failures, "; "), fi.ModTime().Format(time.RFC3339)), nil
		}
	}
	return nil, false, "", fmt.Errorf("download %s: %s", tool.Asset, strings.Join(failures, "; "))
}

// downloadLinpeas fetches one asset from one root and bounds what it accepts.
func downloadLinpeas(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: linpeasHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}

	blob, err := io.ReadAll(io.LimitReader(resp.Body, linpeasMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if len(blob) == 0 {
		return nil, fmt.Errorf("%s: empty response body", url)
	}
	if len(blob) > linpeasMaxBytes {
		return nil, fmt.Errorf("%s: larger than the %d byte ceiling", url, linpeasMaxBytes)
	}
	return blob, nil
}

// writeCache stores blob at path.
//
// The preferred path is a sibling temporary file renamed into place, so a
// reader never observes a half-written asset. That is not always available: a
// cache directory that is a reparse point -- a redirected AppData, for example
// -- rejects the rename with a cross-volume error even though the temporary
// file is a sibling in the very same directory. The fallback writes the buffer
// directly, which is safe here because blob is already complete in memory: the
// write either lands whole or reports an error and leaves no file behind.
func writeCache(path string, blob []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err == nil {
		tmpName := tmp.Name()
		_, writeErr := tmp.Write(blob)
		closeErr := tmp.Close()
		if writeErr == nil && closeErr == nil {
			if renameErr := os.Rename(tmpName, path); renameErr == nil {
				return nil
			}
		}
		os.Remove(tmpName)
	}
	return os.WriteFile(path, blob, 0o600)
}
