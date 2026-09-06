package renstiq

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	updateReleaseURL = "https://api.github.com/repos/wim-web/renstiq/releases/latest"
	maxReleaseJSON   = 2 << 20
	maxChecksumFile  = 1 << 20
	maxArchive       = 128 << 20
	maxBinary        = 128 << 20
)

type UpdateResult struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Updated        bool   `json:"updated"`
	Path           string `json:"path,omitempty"`
}

type updateAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

type updateRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []updateAsset `json:"assets"`
}

type updateOptions struct {
	Client     *http.Client
	ReleaseURL string
	Executable string
	GOOS       string
	GOARCH     string
	Token      string
}

func selfUpdate(ctx context.Context) (UpdateResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return UpdateResult{CurrentVersion: currentVersion()}, fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	token := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	return runUpdate(ctx, currentVersion(), updateOptions{
		Client:     &http.Client{Timeout: 2 * time.Minute},
		ReleaseURL: updateReleaseURL,
		Executable: exe,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		Token:      token,
	})
}

func runUpdate(ctx context.Context, current string, opts updateOptions) (UpdateResult, error) {
	result := UpdateResult{CurrentVersion: current, Path: opts.Executable}
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.ReleaseURL == "" {
		opts.ReleaseURL = updateReleaseURL
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if !supportedUpdatePlatform(opts.GOOS, opts.GOARCH) {
		return result, fmt.Errorf("self-update is not available for %s/%s", opts.GOOS, opts.GOARCH)
	}
	if opts.Executable == "" {
		return result, errors.New("executable path is empty")
	}

	b, err := download(ctx, opts.Client, opts.ReleaseURL, maxReleaseJSON, opts.Token)
	if err != nil {
		return result, fmt.Errorf("fetch latest release: %w", err)
	}
	var release updateRelease
	if err := json.Unmarshal(b, &release); err != nil {
		return result, fmt.Errorf("decode latest release: %w", err)
	}
	latest := strings.TrimSpace(release.TagName)
	result.LatestVersion = latest
	if _, ok := parseSemVersion(latest); !ok {
		return result, fmt.Errorf("latest release has invalid version %q", latest)
	}
	if shouldKeepCurrent(current, latest) {
		return result, nil
	}

	archiveName := fmt.Sprintf("renstiq_%s_%s_%s.tar.gz", latest, opts.GOOS, opts.GOARCH)
	archiveAsset, ok := findUpdateAsset(release.Assets, archiveName)
	if !ok {
		return result, fmt.Errorf("release %s has no asset for %s/%s", latest, opts.GOOS, opts.GOARCH)
	}
	checksumsAsset, ok := findUpdateAsset(release.Assets, "checksums.txt")
	if !ok {
		return result, fmt.Errorf("release %s has no checksums.txt", latest)
	}

	checksums, err := download(ctx, opts.Client, checksumsAsset.BrowserDownloadURL, maxChecksumFile, "")
	if err != nil {
		return result, fmt.Errorf("download checksums.txt: %w", err)
	}
	if err := verifyAssetDigest(checksums, checksumsAsset.Digest); err != nil {
		return result, fmt.Errorf("verify checksums.txt: %w", err)
	}
	expected, err := checksumFor(checksums, archiveName)
	if err != nil {
		return result, err
	}

	archive, err := download(ctx, opts.Client, archiveAsset.BrowserDownloadURL, maxArchive, "")
	if err != nil {
		return result, fmt.Errorf("download %s: %w", archiveName, err)
	}
	if err := verifySHA256(archive, expected); err != nil {
		return result, fmt.Errorf("verify %s: %w", archiveName, err)
	}
	if err := verifyAssetDigest(archive, archiveAsset.Digest); err != nil {
		return result, fmt.Errorf("verify release asset digest: %w", err)
	}

	binary, mode, err := extractUpdateBinary(archive)
	if err != nil {
		return result, fmt.Errorf("extract %s: %w", archiveName, err)
	}
	if err := replaceExecutable(opts.Executable, binary, mode); err != nil {
		return result, fmt.Errorf("replace %s: %w", opts.Executable, err)
	}
	result.Updated = true
	return result, nil
}

func supportedUpdatePlatform(goos, goarch string) bool {
	return (goos == "linux" || goos == "darwin") && (goarch == "amd64" || goarch == "arm64")
}

func findUpdateAsset(assets []updateAsset, name string) (updateAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name && asset.BrowserDownloadURL != "" {
			return asset, true
		}
	}
	return updateAsset{}, false
}

func download(ctx context.Context, client *http.Client, url string, limit int64, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "renstiq")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return b, nil
}

func checksumFor(checksums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		filename := strings.TrimPrefix(fields[len(fields)-1], "*")
		if filename != name {
			continue
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum for %s", name)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", fmt.Errorf("invalid checksum for %s", name)
		}
		return digest, nil
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}

func verifySHA256(data []byte, expected string) error {
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

func verifyAssetDigest(data []byte, digest string) error {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return nil
	}
	algorithm, expected, ok := strings.Cut(digest, ":")
	if !ok || !strings.EqualFold(algorithm, "sha256") {
		return fmt.Errorf("unsupported digest %q", digest)
	}
	return verifySHA256(data, expected)
}

func extractUpdateBinary(archive []byte) ([]byte, os.FileMode, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if path.Clean(h.Name) != "renstiq" {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return nil, 0, errors.New("renstiq archive entry is not a regular file")
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxBinary+1))
		if err != nil {
			return nil, 0, err
		}
		if len(b) == 0 {
			return nil, 0, errors.New("renstiq archive entry is empty")
		}
		if len(b) > maxBinary {
			return nil, 0, errors.New("renstiq archive entry is too large")
		}
		mode := os.FileMode(h.Mode).Perm()
		if mode&0o111 == 0 {
			mode |= 0o755
		}
		return b, mode, nil
	}
	return nil, 0, errors.New("archive does not contain renstiq")
}

func replaceExecutable(filename string, binary []byte, mode os.FileMode) error {
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("current executable is not a regular file")
	}
	if currentMode := info.Mode().Perm(); currentMode&0o111 != 0 {
		mode = currentMode
	}
	if mode&0o111 == 0 {
		mode |= 0o755
	}

	tmp, err := os.CreateTemp(filepath.Dir(filename), ".renstiq-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filename); err != nil {
		return err
	}
	cleanup = false
	return nil
}

type semVersion struct {
	major uint64
	minor uint64
	patch uint64
	pre   []string
}

func parseSemVersion(s string) (semVersion, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre []string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if i == len(s)-1 {
			return semVersion{}, false
		}
		pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semVersion{}, false
	}
	values := [3]uint64{}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semVersion{}, false
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semVersion{}, false
		}
		values[i] = value
	}
	for _, identifier := range pre {
		if identifier == "" {
			return semVersion{}, false
		}
		for _, r := range identifier {
			if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-') {
				return semVersion{}, false
			}
		}
		if isNumeric(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return semVersion{}, false
		}
	}
	return semVersion{major: values[0], minor: values[1], patch: values[2], pre: pre}, true
}

func compareSemVersion(a, b semVersion) int {
	if a.major != b.major {
		return compareUint(a.major, b.major)
	}
	if a.minor != b.minor {
		return compareUint(a.minor, b.minor)
	}
	if a.patch != b.patch {
		return compareUint(a.patch, b.patch)
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if a.pre[i] == b.pre[i] {
			continue
		}
		aNum, bNum := isNumeric(a.pre[i]), isNumeric(b.pre[i])
		if aNum && bNum {
			av, _ := strconv.ParseUint(a.pre[i], 10, 64)
			bv, _ := strconv.ParseUint(b.pre[i], 10, 64)
			return compareUint(av, bv)
		}
		if aNum {
			return -1
		}
		if bNum {
			return 1
		}
		if a.pre[i] < b.pre[i] {
			return -1
		}
		return 1
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}

func compareUint(a, b uint64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func shouldKeepCurrent(current, latest string) bool {
	latestVersion, latestOK := parseSemVersion(latest)
	if !latestOK {
		return false
	}
	currentVersion, currentOK := parseSemVersion(current)
	if !currentOK {
		return false
	}
	return compareSemVersion(currentVersion, latestVersion) >= 0
}
