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
	elf := []byte("\x7fELF\x02\x01\x01\x00")
	mz := append(make([]byte, 0, 0x200), hostExecutableBytes()...)

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

func TestValidateExecutableMessagesAreActionable(t *testing.T) {
	err := validateExecutable([]byte("not a binary at all"), "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "ELF") {
		t.Fatalf("error = %v, want it to name the expected format", err)
	}
}
