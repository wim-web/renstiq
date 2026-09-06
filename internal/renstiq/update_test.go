package renstiq

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func makeArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "renstiq", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func updateServer(t *testing.T, tag, goos, goarch string, archive []byte, checksum string, assetRequests *atomic.Int32) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := fmt.Sprintf("renstiq_%s_%s_%s.tar.gz", tag, goos, goarch)
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(updateRelease{TagName: tag, Assets: []updateAsset{
				{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums"},
				{Name: name, BrowserDownloadURL: server.URL + "/archive"},
			}})
		case "/checksums":
			assetRequests.Add(1)
			fmt.Fprintf(w, "%s  %s\n", checksum, name)
		case "/archive":
			assetRequests.Add(1)
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func TestRunUpdateReplacesExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "renstiq")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeArchive(t, []byte("new"))
	var requests atomic.Int32
	server := updateServer(t, "v1.1.0", "linux", "amd64", archive, sha(archive), &requests)
	defer server.Close()

	result, err := runUpdate(context.Background(), "v1.0.0", updateOptions{
		Client: server.Client(), ReleaseURL: server.URL + "/latest", Executable: exe, GOOS: "linux", GOARCH: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.LatestVersion != "v1.1.0" || requests.Load() != 2 {
		t.Fatalf("result=%+v requests=%d", result, requests.Load())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestRunUpdateAlreadyCurrentDoesNotDownloadAssets(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "renstiq")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeArchive(t, []byte("new"))
	var requests atomic.Int32
	server := updateServer(t, "v1.0.0", "linux", "amd64", archive, sha(archive), &requests)
	defer server.Close()

	result, err := runUpdate(context.Background(), "v1.0.0", updateOptions{
		Client: server.Client(), ReleaseURL: server.URL + "/latest", Executable: exe, GOOS: "linux", GOARCH: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated || requests.Load() != 0 {
		t.Fatalf("result=%+v requests=%d", result, requests.Load())
	}
}

func TestRunUpdateChecksumFailureLeavesExecutableUntouched(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "renstiq")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeArchive(t, []byte("new"))
	var requests atomic.Int32
	server := updateServer(t, "v1.1.0", "linux", "amd64", archive, strings.Repeat("0", 64), &requests)
	defer server.Close()

	_, err := runUpdate(context.Background(), "v1.0.0", updateOptions{
		Client: server.Client(), ReleaseURL: server.URL + "/latest", Executable: exe, GOOS: "linux", GOARCH: "amd64",
	})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err=%v", err)
	}
	got, readErr := os.ReadFile(exe)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old" {
		t.Fatalf("got %q", got)
	}
}

func TestShouldKeepCurrent(t *testing.T) {
	cases := []struct {
		current, latest string
		keep            bool
	}{
		{"v1.0.0", "v1.0.0", true},
		{"v1.1.0", "v1.0.0", true},
		{"v1.0.0", "v1.1.0", false},
		{"v1.1.0-rc.1", "v1.1.0", false},
		{"dev", "v1.0.0", false},
	}
	for _, tc := range cases {
		if got := shouldKeepCurrent(tc.current, tc.latest); got != tc.keep {
			t.Errorf("shouldKeepCurrent(%q, %q)=%v want %v", tc.current, tc.latest, got, tc.keep)
		}
	}
}

func TestRunUpdateCLI(t *testing.T) {
	var out, log bytes.Buffer
	code := runTestUpdateCLI(context.Background(), nil, &out, &log, func(context.Context) (UpdateResult, error) {
		return UpdateResult{CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0", Updated: true}, nil
	})
	if code != 0 || out.String() != "updated renstiq v1.0.0 -> v1.1.0\n" || log.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), log.String())
	}
}

func TestRunUpdateCLIRejectsArguments(t *testing.T) {
	var out, log bytes.Buffer
	code := runTestUpdateCLI(context.Background(), []string{"extra"}, &out, &log, func(context.Context) (UpdateResult, error) {
		t.Fatal("updater must not be called")
		return UpdateResult{}, nil
	})
	if code != 2 || out.Len() != 0 || !strings.Contains(log.String(), "does not accept arguments") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), log.String())
	}
}
