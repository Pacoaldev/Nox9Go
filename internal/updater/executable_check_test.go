package updater

import (
	"runtime"
	"strings"
	"testing"
)

func hostGOOS() string   { return runtime.GOOS }
func hostGOARCH() string { return runtime.GOARCH }

// TestValidateExecutableRejectsForeignHostGates the install guard: a correct
// digest proves the bytes are the published ones, not that they run here.
func TestValidateExecutableRejectsForeignHost(t *testing.T) {
	elf := elfImage("amd64")
	mz := peImage("amd64")

	if err := validateExecutable(elf, "windows", "amd64"); err == nil {
		t.Error("an ELF image must be refused on windows")
	}
	if err := validateExecutable(mz, "linux", "amd64"); err == nil {
		t.Error("a PE image must be refused on linux")
	}
	if err := validateExecutable([]byte("x"), "linux", "amd64"); err == nil {
		t.Error("a file too short to be an executable must be refused")
	}
}

func TestValidateExecutableAcceptsHostImage(t *testing.T) {
	if err := validateExecutable(hostExecutableBytes(), hostGOOS(), hostGOARCH()); err != nil {
		t.Fatalf("host image refused on its own platform: %v", err)
	}
}

// TestValidateExecutableAcceptsEveryTargetPlatform walks every GOOS/GOARCH pair
// the release publishes, whatever host the test runs on. The host-image test
// above only ever exercised one of them, which is how an eight-byte ELF stub
// shipped green on Windows and failed the moment CI ran on Linux.
func TestValidateExecutableAcceptsEveryTargetPlatform(t *testing.T) {
	elfTargets := map[string]string{
		"linux":   "amd64",
		"freebsd": "arm64",
	}
	for goos, goarch := range elfTargets {
		if err := validateExecutable(elfImage(goarch), goos, goarch); err != nil {
			t.Errorf("%s/%s: valid image refused: %v", goos, goarch, err)
		}
	}
	if err := validateExecutable(peImage("amd64"), "windows", "amd64"); err != nil {
		t.Errorf("windows/amd64: valid image refused: %v", err)
	}
	if err := validateExecutable(peImage("arm64"), "windows", "amd64"); err == nil {
		t.Error("an arm64 PE must be refused for windows/amd64")
	}
	// A header naming another architecture must not pass for this one.
	if err := validateExecutable(elfImage("arm64"), "linux", "amd64"); err == nil {
		t.Error("an arm64 image must be refused for linux/amd64")
	}
	if err := validateExecutable(elfImage("amd64"), "linux", "arm64"); err == nil {
		t.Error("an amd64 image must be refused for linux/arm64")
	}
}

func TestValidateExecutableMessagesAreActionable(t *testing.T) {
	err := validateExecutable([]byte("not a binary at all"), "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "ELF") {
		t.Fatalf("error = %v, want it to name the expected format", err)
	}
}
