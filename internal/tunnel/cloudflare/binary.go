package cloudflare

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/constants"
)

const (
	githubReleaseBase = "https://github.com/cloudflare/cloudflared/releases/latest/download"
	minBinarySize     = 1 << 20 // 1 MiB — truncated downloads are rejected
)

var (
	dlMu          sync.Mutex
	dlDownloading bool
	dlProgress    int
	ensureMu      sync.Mutex
)

// DownloadStatus reports an in-flight cloudflared download for the status API.
func DownloadStatus() (downloading bool, progress int) {
	dlMu.Lock()
	defer dlMu.Unlock()
	return dlDownloading, dlProgress
}

func setDownload(downloading bool, progress int) {
	dlMu.Lock()
	dlDownloading = downloading
	dlProgress = progress
	dlMu.Unlock()
}

func binDir() string {
	return filepath.Join(config.ResolveDataDir(), "bin")
}

func binPath() string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(binDir(), name)
}

func downloadAssetName() (string, error) {
	switch runtime.GOOS {
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			return "cloudflared-windows-amd64.exe", nil
		case "386", "arm64":
			return "cloudflared-windows-386.exe", nil
		}
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "cloudflared-darwin-arm64.tgz", nil
		}
		return "cloudflared-darwin-amd64.tgz", nil
	case "linux":
		if runtime.GOARCH == "arm64" {
			return "cloudflared-linux-arm64", nil
		}
		return "cloudflared-linux-amd64", nil
	}
	return "", fmt.Errorf("cloudflare: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
}

// EnsureBinary downloads cloudflared into the data dir when missing or invalid.
func EnsureBinary() (string, error) {
	ensureMu.Lock()
	defer ensureMu.Unlock()
	return ensureBinaryLocked()
}

func ensureBinaryLocked() (string, error) {
	if err := os.MkdirAll(binDir(), constants.FilePermDir); err != nil {
		return "", fmt.Errorf("cloudflare: mkdir bin: %w", err)
	}
	tmpPath := binPath() + ".tmp"
	_ = os.Remove(tmpPath)

	if _, err := os.Stat(binPath()); err == nil {
		if isValidBinary(binPath()) {
			if runtime.GOOS != "windows" {
				_ = os.Chmod(binPath(), 0o755)
			}
			return binPath(), nil
		}
		_ = os.Remove(binPath())
	}

	asset, err := downloadAssetName()
	if err != nil {
		return "", err
	}
	url := githubReleaseBase + "/" + asset
	dest := tmpPath
	isArchive := strings.HasSuffix(asset, ".tgz")
	if isArchive {
		dest = filepath.Join(binDir(), "cloudflared.tgz.tmp")
	}

	if err := downloadFile(url, dest); err != nil {
		return "", err
	}

	if isArchive {
		if err := extractTgzBinary(dest, binPath()); err != nil {
			_ = os.Remove(dest)
			return "", err
		}
		_ = os.Remove(dest)
	} else if err := os.Rename(dest, binPath()); err != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("cloudflare: install binary: %w", err)
	}

	if runtime.GOOS != "windows" {
		_ = os.Chmod(binPath(), 0o755)
	}
	if !isValidBinary(binPath()) {
		_ = os.Remove(binPath())
		return "", fmt.Errorf("cloudflare: downloaded binary failed validation")
	}
	return binPath(), nil
}

func isValidBinary(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.Size() < minBinarySize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	switch runtime.GOOS {
	case "windows":
		return magic[0] == 'M' && magic[1] == 'Z'
	case "darwin":
		return (magic[0] == 0xcf && magic[1] == 0xfa && magic[2] == 0xed && magic[3] == 0xfe) ||
			(magic[0] == 0xca && magic[1] == 0xfe && magic[2] == 0xba && magic[3] == 0xbe) ||
			(magic[0] == 0xce && magic[1] == 0xfa && magic[2] == 0xed && magic[3] == 0xfe)
	default:
		return magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
	}
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("cloudflare: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cloudflare: download status %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(dest)
		}
	}()

	total := resp.ContentLength
	setDownload(true, 0)
	defer func() {
		if ok {
			setDownload(false, 100)
		} else {
			setDownload(false, 0)
		}
	}()

	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if total > 0 {
				setDownload(true, int(written*100/total))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	ok = true
	return nil
}

func extractTgzBinary(tgzPath, dest string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("cloudflare: cloudflared not found in archive")
		}
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		if base != "cloudflared" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
}
