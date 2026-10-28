package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"debug/elf"
	"encoding/binary"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.1", "1.0.0", 1},
		{"v1.2.3", "1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.8.6-rc1", "1.8.5", 1},
		{"1.8.5", "1.8.6", -1},
	}

	for _, tt := range tests {
		got := CompareVersions(tt.v1, tt.v2)
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestCompareVersions_PrereleasePrecedence(t *testing.T) {
	tests := []struct {
		name string
		v1   string
		v2   string
		want int
	}{
		{name: "final outranks its own prerelease", v1: "1.9.7", v2: "1.9.7-rc1", want: 1},
		{name: "prerelease ranks below its own final", v1: "1.9.7-rc1", v2: "1.9.7", want: -1},
		{name: "numeric prerelease identifiers order numerically", v1: "1.9.7-rc2", v2: "1.9.7-rc1", want: 1},
		{name: "numeric identifiers are not compared as text", v1: "1.9.7-10", v2: "1.9.7-9", want: 1},
		{name: "a prerelease of the next core loses to it", v1: "1.9.7-rc1", v2: "1.9.8", want: -1},
		{name: "shorter prerelease ranks lower", v1: "1.9.7-rc", v2: "1.9.7-rc.1", want: -1},
		{name: "shared identifiers equal, longer wins", v1: "1.9.7-rc.1", v2: "1.9.7-rc", want: 1},
		{name: "numeric identifier ranks below alphanumeric", v1: "1.9.7-1", v2: "1.9.7-alpha", want: -1},
		{name: "alphanumeric identifiers compare as text", v1: "1.9.7-beta", v2: "1.9.7-alpha", want: 1},
		{name: "dotted identifiers are compared left to right", v1: "1.9.7-alpha.2", v2: "1.9.7-alpha.1", want: 1},
		{name: "build metadata raises nothing", v1: "1.9.7+build.9", v2: "1.9.7", want: 0},
		{name: "build metadata lowers nothing", v1: "1.9.7", v2: "1.9.7-rc1+build.9", want: 1},
		{name: "build metadata differs but precedence is equal", v1: "1.9.7+aaa", v2: "1.9.7+zzz", want: 0},
		{name: "core still wins over prerelease", v1: "1.10.0-rc1", v2: "1.9.9", want: 1},
		{name: "v prefix does not hide the prerelease", v1: "v1.9.7", v2: "v1.9.7-rc1", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompareVersions(tt.v1, tt.v2); got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

func TestAutoApplyAllowed(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "final user is not moved onto a prerelease", current: "1.9.7", latest: "1.9.8-rc1", want: false},
		{name: "final user still gets the final release", current: "1.9.7", latest: "1.9.8", want: true},
		{name: "prerelease user still rolls to the next prerelease", current: "1.9.8-rc1", latest: "1.9.8-rc2", want: true},
		{name: "prerelease user still rolls to the final release", current: "1.9.8-rc1", latest: "1.9.8", want: true},
		{name: "release candidate is not auto-applied either", current: "1.9.7", latest: "1.9.8-rc.1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoApplyAllowed(tt.current, tt.latest); got != tt.want {
				t.Errorf("autoApplyAllowed(%q, %q) = %t, want %t", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestGetCachedInfo(t *testing.T) {
	info := GetCachedInfo()
	if info == nil {
		t.Fatal("expected non-nil UpdateInfo")
	}
	if info.CurrentVersion == "" {
		t.Error("expected non-empty CurrentVersion")
	}
}

func TestCheckUpdate_Manifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The manifest publishes one asset per platform. A bare downloadUrl is
		// not consulted at all any more: it described a Windows executable and
		// was handed to every OS that had no entry of its own.
		manifest := map[string]any{
			"latestVersion": "2.0.0",
			"downloadUrls": map[string]string{
				platformKey(runtime.GOOS, runtime.GOARCH): "https://example.com/downloads/nox9go-" + platformKey(runtime.GOOS, runtime.GOARCH),
			},
			"releaseNotes": "Major release 2.0.0",
			"sha256":       "abcdef123456",
		}
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, manifest)
	}))
	defer server.Close()

	os.Setenv("UPDATE_URL", server.URL)
	defer os.Unsetenv("UPDATE_URL")

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}

	if !info.HasUpdate {
		t.Errorf("expected hasUpdate=true for version 2.0.0 vs %s", CurrentVersion)
	}
	if info.LatestVersion != "2.0.0" {
		t.Errorf("expected latestVersion 2.0.0, got %s", info.LatestVersion)
	}
	if want := "https://example.com/downloads/nox9go-" + platformKey(runtime.GOOS, runtime.GOARCH); info.DownloadURL != want {
		t.Errorf("downloadUrl = %q, want %q", info.DownloadURL, want)
	}
}

func TestCheckUpdate_GitHubReleasesFallback(t *testing.T) {
	// Mock failing manifest endpoint
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer manifestServer.Close()

	os.Setenv("UPDATE_URL", manifestServer.URL)
	defer os.Unsetenv("UPDATE_URL")

	// Directly test checkGitHubReleases
	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"tag_name": "v3.0.0",
			"name":     "Release 3.0.0",
			"body":     "Awesome new features",
			"assets": []map[string]any{
				{
					"name":                 "nox9go_darwin_arm64.tar.gz",
					"browser_download_url": "https://github.com/releases/nox9go_darwin_arm64.tar.gz",
				},
				{
					"name":                 "nox9go_linux_amd64.tar.gz",
					"browser_download_url": "https://github.com/releases/nox9go_linux_amd64.tar.gz",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, resp)
	}))
	defer ghServer.Close()

	info, err := checkGitHubReleases(context.Background(), ghServer.URL)
	if err != nil {
		t.Fatalf("checkGitHubReleases failed: %v", err)
	}

	if info.LatestVersion != "3.0.0" {
		t.Errorf("expected latestVersion 3.0.0, got %s", info.LatestVersion)
	}
	if !info.HasUpdate {
		t.Errorf("expected hasUpdate=true")
	}
	if info.Source != "github_releases" {
		t.Errorf("expected source github_releases, got %s", info.Source)
	}
}

func TestExtractExecutableBytes_TarGz(t *testing.T) {
	// Create a dummy .tar.gz containing a nox9go binary payload
	binaryContent := bytes.Repeat([]byte("BINARY_PAYLOAD_CONTENT_TEST_EXEC_DATA"), 100)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "nox9go",
		Mode: 0755,
		Size: int64(len(binaryContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	tw.Close()
	gw.Close()

	extracted, err := extractExecutableBytes(buf.Bytes(), "nox9go_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("extractExecutableBytes failed: %v", err)
	}

	if !bytes.Equal(extracted, binaryContent) {
		t.Errorf("extracted content does not match expected payload")
	}
}

func TestExtractExecutableBytes_Zip(t *testing.T) {
	binaryContent := bytes.Repeat([]byte("ZIP_BINARY_PAYLOAD_TEST_DATA"), 100)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f, err := zw.Create("nox9go.exe")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write(binaryContent); err != nil {
		t.Fatalf("write zip content: %v", err)
	}
	zw.Close()

	extracted, err := extractExecutableBytes(buf.Bytes(), "nox9go_windows_amd64.zip")
	if err != nil {
		t.Fatalf("extract zip failed: %v", err)
	}

	if !bytes.Equal(extracted, binaryContent) {
		t.Errorf("extracted zip content does not match payload")
	}
}

func TestMatchReleaseAsset(t *testing.T) {
	assets := []releaseAsset{
		{Name: "nox9go_linux_amd64.tar.gz", BrowserDownloadURL: "url-linux-amd64"},
		{Name: "nox9go_darwin_arm64.tar.gz", BrowserDownloadURL: "url-darwin-arm64"},
		{Name: "nox9go_windows_amd64.zip", BrowserDownloadURL: "url-windows-amd64"},
		{Name: "checksums.txt", BrowserDownloadURL: "url-checksums"},
	}

	if url := matchReleaseAsset(assets, "darwin", "arm64"); url != "url-darwin-arm64" {
		t.Errorf("expected url-darwin-arm64, got %s", url)
	}
	if url := matchReleaseAsset(assets, "linux", "amd64"); url != "url-linux-amd64" {
		t.Errorf("expected url-linux-amd64, got %s", url)
	}
	if url := matchReleaseAsset(assets, "windows", "amd64"); url != "url-windows-amd64" {
		t.Errorf("expected url-windows-amd64, got %s", url)
	}
	// A platform the release does not carry is not offered anything. The old
	// fallback returned "the first asset that is not a checksum", which handed a
	// Linux host the Windows executable under a matching digest.
	if url := matchReleaseAsset(assets, "freebsd", "amd64"); url != "" {
		t.Errorf("unmatched platform = %q, want no asset", url)
	}
	if url := matchReleaseAsset(nil, "linux", "amd64"); url != "" {
		t.Errorf("empty release = %q, want no asset", url)
	}
	if url := matchReleaseAsset(assets, "linux", "amd64"); url != "url-linux-amd64" {
		t.Errorf("expected url-windows-amd64, got %s", url)
	}
}

func TestAutoUpdate_StatusAndToggle(t *testing.T) {
	SetAutoUpdate(true)
	if !IsAutoUpdateEnabled() {
		t.Errorf("expected autoUpdate to be true")
	}

	status := GetStatus()
	if !status.AutoUpdateEnabled {
		t.Errorf("expected status.AutoUpdateEnabled to be true")
	}
	if status.CurrentVersion != CurrentVersion {
		t.Errorf("expected currentVersion %s, got %s", CurrentVersion, status.CurrentVersion)
	}

	SetAutoUpdate(false)
	if IsAutoUpdateEnabled() {
		t.Errorf("expected autoUpdate to be false")
	}
}

// hostExecutableBytes is a tiny program-image header for the test host: an "MZ"
// stub on Windows, an ELF image elsewhere. PerformSelfUpdate refuses anything
// that is not a runnable image for this OS, so a test payload has to look like
// one before the bytes that follow it.
func hostExecutableBytes() []byte {
	if runtime.GOOS == "windows" {
		// debug/pe walks the DOS stub, the file header and the whole optional
		// header, so the stub is padded past all of them.
		img := make([]byte, 0x200)
		copy(img, []byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff\x00\x00\xb8\x00\x00\x00\x00\x00\x00\x00\x40\x00\x00\x00\x00\x00\x00\x00"))
		const peOff = 0x80
		copy(img[0x3c:], []byte{0x80, 0, 0, 0})
		// Offsets are relative to the file header (peOff), not to the signature.
		copy(img[peOff:], []byte{0x50, 0x45, 0, 0})          // "PE\0\0"
		copy(img[peOff+4:], []byte{0x64, 0x86, 0x00, 0x00})  // IMAGE_FILE_MACHINE_AMD64
		copy(img[peOff+20:], []byte{0xf0, 0x00})             // SizeOfOptionalHeader
		copy(img[peOff+24:], []byte{0x0b, 0x02})             // PE32+ magic
		copy(img[peOff+24+108:], []byte{0x10, 0x00, 0x00, 0x00}) // 16 data directories
		return img
	}
	return elfImage(runtime.GOARCH)
}

// elfImage builds the smallest 64-bit ELF header debug/elf will parse, for the
// host architecture. An eight-byte "\x7fELF" magic used to stand in here and
// passed on Windows only because the Windows branch of validateExecutable never
// parsed it that far: on the Linux runner the real parser reported "cannot read
// ELF identifier: EOF" and every test that needed a believable binary failed.
// e_ident and e_machine are the only fields it validates before returning.
func elfImage(goarch string) []byte {
	machine := map[string]elf.Machine{
		"amd64": elf.EM_X86_64,
		"arm64": elf.EM_AARCH64,
		"386":   elf.EM_386,
		"arm":   elf.EM_ARM,
	}[goarch]
	img := make([]byte, 64)
	copy(img, []byte{
		0x7f, 'E', 'L', 'F',
		2,    // ELFCLASS64
		1,    // ELFDATA2LSB
		1,    // EV_CURRENT
		0,    // ELFOSABI_SYSV
	})
	// e_type = ET_EXEC (2), e_machine, e_version = EV_CURRENT (1).
	binary.LittleEndian.PutUint16(img[16:], 2)
	binary.LittleEndian.PutUint16(img[18:], uint16(machine))
	binary.LittleEndian.PutUint32(img[20:], 1)
	return img
}

// updatePayload is the binary content a release server in these tests serves.
func updatePayload() []byte {
	return append(hostExecutableBytes(), bytes.Repeat([]byte("NEW_BINARY_PAYLOAD"), 200)...)
}

// platformBinaryAsset is the release asset name for the platform the tests run
// on, which is the entry the checksum manifest has to carry.
func platformBinaryAsset() string {
	name := fmt.Sprintf("nox9go_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// releaseServer serves the release endpoints the cycle needs; the handlers
// capture `base` after the call, so the asset URLs can point back here.
func releaseServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	return server
}

func TestCheckGitHubReleases_PublishesPlatformChecksum(t *testing.T) {
	platformAsset := platformBinaryAsset()
	wantDigest := strings.Repeat("ab", 32)
	var base string
	var releaseRequests, checksumRequests int
	serve := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/"+checksumsAssetName) {
			checksumRequests++
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "%s  nox9go-linux-amd64\n", strings.Repeat("11", 32))
			fmt.Fprintf(w, "%s *%s\n", wantDigest, platformAsset)
			return
		}
		releaseRequests++
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, map[string]any{
			"tag_name": "v9.9.9",
			"body":     "notes",
			"assets": []map[string]any{
				{"name": platformAsset, "browser_download_url": base + platformAsset},
				{"name": checksumsAssetName, "browser_download_url": base + checksumsAssetName},
			},
		})
	})
	base = serve.URL + "/"

	info, err := checkGitHubReleases(context.Background(), serve.URL)
	if err != nil {
		t.Fatalf("checkGitHubReleases failed: %v", err)
	}
	if info.DownloadURL != base+platformAsset {
		t.Fatalf("unexpected download URL %q", info.DownloadURL)
	}
	if info.SHA256 != wantDigest {
		t.Errorf("SHA256 = %q, want the digest of %s (%q)", info.SHA256, platformAsset, wantDigest)
	}
	if checksumRequests != 1 {
		t.Errorf("checksum asset fetched %d times, want 1", checksumRequests)
	}
	if releaseRequests != 1 {
		t.Errorf("release API fetched %d times, want 1", releaseRequests)
	}
}

func TestCheckGitHubReleases_ChecksumLookupFailures(t *testing.T) {
	platformAsset := fmt.Sprintf("nox9go_%s_%s", runtime.GOOS, runtime.GOARCH)

	tests := []struct {
		name  string
		lines []string
	}{
		{name: "release has no SHA256SUMS.txt asset"},
		{
			name:  "SHA256SUMS.txt has no entry for this platform",
			lines: []string{strings.Repeat("ab", 32) + "  nox9go-plan9-mips.tar.gz"},
		},
		{
			name:  "SHA256SUMS.txt lines are unreadable",
			lines: []string{"", "not a checksum line", strings.Repeat("zz", 32) + "  " + platformAsset},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var base string
			serve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/"+checksumsAssetName) {
					w.Header().Set("Content-Type", "text/plain")
					for _, line := range tt.lines {
						fmt.Fprintln(w, line)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				assets := []map[string]any{{"name": platformAsset, "browser_download_url": base + platformAsset}}
				if tt.lines != nil {
					assets = append(assets, map[string]any{"name": checksumsAssetName, "browser_download_url": base + checksumsAssetName})
				}
				json.MarshalWrite(w, map[string]any{"tag_name": "v9.9.9", "body": "notes", "assets": assets})
			}))
			defer serve.Close()
			base = serve.URL + "/"

			info, err := checkGitHubReleases(context.Background(), serve.URL)
			if err != nil {
				t.Fatalf("a failed checksum lookup must not fail the check: %v", err)
			}
			if info.SHA256 != "" {
				t.Errorf("SHA256 = %q, want empty", info.SHA256)
			}
			if !info.HasUpdate {
				t.Errorf("the update must still be reported as available")
			}
			if info.LatestVersion != "9.9.9" {
				t.Errorf("latestVersion = %q, want 9.9.9", info.LatestVersion)
			}
			if info.DownloadURL != base+platformAsset {
				t.Errorf("downloadUrl = %q, want %q", info.DownloadURL, base+platformAsset)
			}
		})
	}
}

func TestParseChecksumLine(t *testing.T) {
	digest := strings.Repeat("9a", 32)
	tests := []struct {
		name      string
		line      string
		wantName  string
		wantEmpty bool
	}{
		{name: "two space text mode", line: digest + "  nox9go-linux-amd64", wantName: "nox9go-linux-amd64"},
		{name: "single space separator", line: digest + " nox9go-linux-amd64", wantName: "nox9go-linux-amd64"},
		{name: "star binary mode", line: digest + " *nox9go-linux-amd64", wantName: "nox9go-linux-amd64"},
		{name: "leading ./ prefix", line: digest + "  ./nox9go-linux-amd64", wantName: "./nox9go-linux-amd64"},
		{name: "digest too short", line: "abc123  nox9go-linux-amd64", wantEmpty: true},
		{name: "digest is not hex", line: strings.Repeat("zz", 32) + "  nox9go-linux-amd64", wantEmpty: true},
		{name: "no filename", line: digest, wantEmpty: true},
		{name: "blank line", line: "", wantEmpty: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDigest, gotName := parseChecksumLine(tt.line)
			if tt.wantEmpty {
				if gotDigest != "" || gotName != "" {
					t.Fatalf("parseChecksumLine(%q) = (%q, %q), want both empty", tt.line, gotDigest, gotName)
				}
				return
			}
			if gotDigest != digest {
				t.Errorf("parseChecksumLine(%q) digest = %q, want %q", tt.line, gotDigest, digest)
			}
			if gotName != tt.wantName {
				t.Errorf("parseChecksumLine(%q) name = %q, want %q", tt.line, gotName, tt.wantName)
			}
		})
	}
}

func TestAssetFileName(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{url: "https://example.test/nox9go-linux-amd64", want: "nox9go-linux-amd64"},
		{url: "https://example.test/nox9go-windows-amd64.exe?token=abc", want: "nox9go-windows-amd64.exe"},
		{url: "https://example.test/dir/", want: "dir"},
		{url: "nox9go-darwin-arm64.tar.gz", want: "nox9go-darwin-arm64.tar.gz"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := assetFileName(tt.url); got != tt.want {
				t.Errorf("assetFileName(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// updateAssetTarGz packs payload as the single entry of a .tar.gz, so the
// checksum a release publishes for the archive is the digest of the extracted
// binary PerformSelfUpdate compares against.
func updateAssetTarGz(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "nox9go", Mode: 0755, Size: int64(len(payload))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// stubRunningBinary stands in for the executable PerformSelfUpdate swaps, so the
// tests can observe the file it leaves behind without renaming the running test
// binary itself.
func stubRunningBinary(t *testing.T, payload []byte) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "nox9go")
	if err := os.WriteFile(stub, payload, 0o755); err != nil {
		t.Fatalf("write stub binary: %v", err)
	}
	previous := selfExecutable
	selfExecutable = func() (string, error) { return stub, nil }
	t.Cleanup(func() { selfExecutable = previous })
	return stub
}

func TestPerformSelfUpdate_RefusesInstallWithoutChecksum(t *testing.T) {
	stub := stubRunningBinary(t, []byte("ORIGINAL_RUNNING_BINARY"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(updateAssetTarGz(t, bytes.Repeat([]byte("NEW_BINARY_PAYLOAD"), 200)))
	}))
	defer server.Close()

	if err := PerformSelfUpdate(server.URL+"/nox9go_linux_amd64.tar.gz", ""); err == nil {
		t.Fatal("expected an error when the release carries no checksum")
	}

	got, err := os.ReadFile(stub)
	if err != nil {
		t.Fatalf("read stub binary: %v", err)
	}
	if string(got) != "ORIGINAL_RUNNING_BINARY" {
		t.Errorf("stub binary = %q, want it left untouched", got)
	}
}

func TestPerformSelfUpdate_KeepsBinaryOnChecksumMismatch(t *testing.T) {
	stub := stubRunningBinary(t, []byte("ORIGINAL_RUNNING_BINARY"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(updateAssetTarGz(t, bytes.Repeat([]byte("NEW_BINARY_PAYLOAD"), 200)))
	}))
	defer server.Close()

	err := PerformSelfUpdate(server.URL+"/nox9go_linux_amd64.tar.gz", strings.Repeat("ff", 32))
	if err == nil {
		t.Fatal("expected an error on checksum mismatch")
	}
	if !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Errorf("error = %v, want a SHA256 mismatch", err)
	}

	got, err := os.ReadFile(stub)
	if err != nil {
		t.Fatalf("read stub binary: %v", err)
	}
	if string(got) != "ORIGINAL_RUNNING_BINARY" {
		t.Errorf("stub binary = %q, want it left untouched", got)
	}
}

func TestRunCheckCycle_SkipsPrereleaseForFinalInstall(t *testing.T) {
	stub := stubRunningBinary(t, []byte("ORIGINAL_RUNNING_BINARY"))

	previousVersion := CurrentVersion
	previousURL, hadURL := os.LookupEnv("UPDATE_URL")
	previousRepo, hadRepo := os.LookupEnv("UPDATE_REPO")
	previousAuto, hadAuto := os.LookupEnv("AUTO_UPDATE")
	restoreEnv := func(key, value string, wasSet bool) {
		if wasSet {
			os.Setenv(key, value)
			return
		}
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		CurrentVersion = previousVersion
		restoreEnv("UPDATE_URL", previousURL, hadURL)
		restoreEnv("UPDATE_REPO", previousRepo, hadRepo)
		restoreEnv("AUTO_UPDATE", previousAuto, hadAuto)
	})

	payload := updatePayload()
	digest := ComputeSHA256(payload)
	assetName := platformBinaryAsset()
	var release, base string
	server := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			json.MarshalWrite(w, map[string]any{
				"tag_name": "v" + release,
				"body":     "notes",
				"assets": []map[string]any{
					{"name": assetName, "browser_download_url": base + assetName},
					{"name": checksumsAssetName, "browser_download_url": base + checksumsAssetName},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/"+checksumsAssetName):
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "%s  %s\n", digest, assetName)
		case r.URL.Path == "/"+assetName:
			w.Write(updateAssetTarGz(t, payload))
		default:
			http.NotFound(w, r)
		}
	})
	base = server.URL + "/"

	// The manifest URL 404s, so the cycle falls through to the GitHub Releases
	// path — the only one carrying a real digest for the asset it installs.
	previousAPI := githubReleasesAPI
	githubReleasesAPI = server.URL + "/repos/%s/releases/latest"
	t.Cleanup(func() { githubReleasesAPI = previousAPI })
	os.Setenv("UPDATE_URL", server.URL+"/manifest.json")
	os.Setenv("UPDATE_REPO", "owner/repo")
	os.Setenv("AUTO_UPDATE", "true")
	CurrentVersion = "1.9.7"

	// RestartSelf only schedules a child once the listener is closed; outside a
	// daemon nothing is registered, so it would exit the test process.
	var restarts atomic.Int32
	previousRestart := restartSelf
	restartSelf = func() { restarts.Add(1) }
	t.Cleanup(func() { restartSelf = previousRestart })

	// A prerelease carrying a valid checksum is still not applied: without the
	// guard the cycle downloads, verifies, swaps in an RC and restarts.
	release = "1.9.8-rc1"
	runCheckCycle(context.Background())

	info, err := checkGitHubReleases(context.Background(), fmt.Sprintf(githubReleasesAPI, "owner/repo"))
	if err != nil {
		t.Fatalf("checkGitHubReleases failed: %v", err)
	}
	if !info.HasUpdate || info.LatestVersion != "1.9.8-rc1" {
		t.Fatalf("expected the rc to be reported as available, got %+v", info)
	}
	if info.SHA256 != digest {
		t.Fatalf("release checksum = %q, want %q", info.SHA256, digest)
	}
	if _, err := os.Stat(stub); err != nil {
		t.Fatalf("prerelease was auto-applied: %v", err)
	}
	if got := restarts.Load(); got != 0 {
		t.Fatalf("restarts after the rc = %d, want 0", got)
	}

	// The final release is applied: the binary is replaced and the process is
	// scheduled for a restart.
	release = "1.9.8"
	runCheckCycle(context.Background())

	installed, err := os.ReadFile(stub)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if !bytes.Equal(installed, payload) {
		t.Error("installed binary does not match the release payload")
	}
	if got := restarts.Load(); got != 1 {
		t.Errorf("restarts after the final release = %d, want 1", got)
	}
}

// version.json ships three keys — downloadUrl, latestVersion, releaseNotes — and
// no sha256. Since #72 made the digest mandatory, returning that manifest on
// sight meant the one path that CAN fill the digest from the release's
// SHA256SUMS.txt never ran: every install ended in "release carries no
// checksum", and `nox9go update` failed outright. This pins the manifest to
// being a lead rather than an answer when it describes an update it cannot
// verify.
func TestCheckUpdate_ManifestWithoutDigestFallsThroughToReleaseDigest(t *testing.T) {
	payload := updatePayload()
	digest := ComputeSHA256(payload)
	assetName := platformBinaryAsset()

	previousVersion := CurrentVersion
	previousAPI := githubReleasesAPI
	t.Cleanup(func() {
		CurrentVersion = previousVersion
		githubReleasesAPI = previousAPI
	})
	CurrentVersion = "1.9.6"

	var base string
	releaseRequests := 0
	server := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/version.json":
			// Exactly the shape this repo publishes: no sha256 key at all.
			w.Header().Set("Content-Type", "application/json")
			json.MarshalWrite(w, map[string]any{
				"downloadUrl":  "https://example.invalid/releases/latest",
				"latestVersion": "1.9.7",
				"releaseNotes":  "notes",
			})
		case r.URL.Path == "/repos/owner/repo/releases/latest":
			releaseRequests++
			w.Header().Set("Content-Type", "application/json")
			json.MarshalWrite(w, map[string]any{
				"tag_name": "v1.9.7",
				"body":     "notes",
				"assets": []map[string]any{
					{"name": assetName, "browser_download_url": base + assetName},
					{"name": checksumsAssetName, "browser_download_url": base + checksumsAssetName},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/"+checksumsAssetName):
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "%s  %s\n", digest, assetName)
		default:
			http.NotFound(w, r)
		}
	})
	base = server.URL + "/"

	githubReleasesAPI = server.URL + "/repos/%s/releases/latest"
	t.Setenv("UPDATE_URL", server.URL+"/version.json")
	t.Setenv("UPDATE_REPO", "owner/repo")

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}
	if releaseRequests == 0 {
		t.Fatal("manifest answered without ever consulting the release digest; the manifest carries no sha256")
	}
	if info.SHA256 != digest {
		t.Errorf("SHA256 = %q, want the release digest %q", info.SHA256, digest)
	}
	if info.Source != "github_releases" {
		t.Errorf("Source = %q, want github_releases", info.Source)
	}
	if !info.HasUpdate || info.LatestVersion != "1.9.7" {
		t.Fatalf("expected 1.9.7 to be reported as available, got %+v", info)
	}
}

// The fallback must not become a regression of its own: a manifest that does
// carry a digest is still the faster, authoritative answer, and one reporting
// no update needs no digest to be useful.
func TestCheckUpdate_ManifestWithDigestStillAnswersWithoutGitHub(t *testing.T) {
	digest := strings.Repeat("cd", 32)
	var releaseRequests int
	server := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version.json" {
			w.Header().Set("Content-Type", "application/json")
			json.MarshalWrite(w, map[string]any{
				"latestVersion": "1.9.7",
				"downloadUrl":   "https://example.invalid/bin.zip",
				"sha256":        digest,
			})
			return
		}
		releaseRequests++
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, map[string]any{"tag_name": "v1.9.7", "assets": []map[string]any{}})
	})

	previousAPI := githubReleasesAPI
	githubReleasesAPI = server.URL + "/repos/%s/releases/latest"
	t.Cleanup(func() { githubReleasesAPI = previousAPI })
	t.Setenv("UPDATE_URL", server.URL+"/version.json")

	previousVersion := CurrentVersion
	CurrentVersion = "1.9.6"
	t.Cleanup(func() { CurrentVersion = previousVersion })

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}
	if info.SHA256 != digest {
		t.Errorf("SHA256 = %q, want the manifest digest %q", info.SHA256, digest)
	}
	if info.Source != "manifest" {
		t.Errorf("Source = %q, want manifest", info.Source)
	}
	if releaseRequests != 0 {
		t.Errorf("consulted the GitHub fallback %d times for a manifest that already had a digest", releaseRequests)
	}
}

// A digest-less manifest that reports no update is still a fine answer — there
// is nothing to install, so it must not drag the release API into the answer.
func TestCheckUpdate_UpToDateManifestNeedsNoDigest(t *testing.T) {
	server := releaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version.json" {
			w.Header().Set("Content-Type", "application/json")
			json.MarshalWrite(w, map[string]any{"latestVersion": CurrentVersion})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})

	previousAPI := githubReleasesAPI
	githubReleasesAPI = server.URL + "/repos/%s/releases/latest"
	t.Cleanup(func() { githubReleasesAPI = previousAPI })
	t.Setenv("UPDATE_URL", server.URL+"/version.json")

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}
	if info.HasUpdate {
		t.Errorf("expected no update, got %+v", info)
	}
	if info.Source != "manifest" {
		t.Errorf("Source = %q, want manifest", info.Source)
	}
}
