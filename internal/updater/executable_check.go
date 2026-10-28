package updater

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"strings"
)

// validateExecutable refuses a binary that is not built for this OS and, where
// the file format records it, not for this architecture.
//
// The SHA-256 check beside it proves the bytes are the ones that were published.
// It cannot prove they are executable here: a release that attaches the wrong
// platform under a correct digest would pass it and then be renamed over the
// running binary, leaving the operator with a gateway that no longer starts.
func validateExecutable(data []byte, goos, goarch string) error {
	if len(data) < 4 {
		return errors.New("downloaded file is too small to be an executable")
	}

	switch goos {
	case "windows":
		if !isPE(data) {
			return fmt.Errorf("asset is not a Windows executable (no MZ/PE header)")
		}
		f, err := pe.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("read PE header: %w", err)
		}
		defer f.Close()
		return checkArch(f.Machine, goarch, map[uint16]string{
			pe.IMAGE_FILE_MACHINE_AMD64: "amd64",
			pe.IMAGE_FILE_MACHINE_ARM64: "arm64",
			pe.IMAGE_FILE_MACHINE_I386:  "386",
		})
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("asset is not a Mach-O executable: %w", err)
		}
		defer f.Close()
		return checkArch(f.Cpu, goarch, map[macho.Cpu]string{
			macho.CpuAmd64: "amd64",
			macho.CpuArm64: "arm64",
		})
	case "linux", "freebsd", "netbsd", "openbsd":
		f, err := elf.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("asset is not an ELF executable: %w", err)
		}
		defer f.Close()
		return checkArch(f.Machine, goarch, map[elf.Machine]string{
			elf.EM_X86_64:  "amd64",
			elf.EM_AARCH64: "arm64",
			elf.EM_386:     "386",
			elf.EM_ARM:     "arm",
		})
	default:
		return nil
	}
}

// checkArch compares the format's machine field with GOARCH. An architecture
// the table does not name is accepted: the container check above already proved
// the file format, and refusing an unknown machine would block a platform the
// build supports before this table is updated.
func checkArch[T comparable](machine T, goarch string, known map[T]string) error {
	want, ok := known[machine]
	if !ok {
		return nil
	}
	arch := strings.ToLower(goarch)
	if arch == "x86_64" {
		arch = "amd64"
	}
	if arch == "aarch64" {
		arch = "arm64"
	}
	if arch != want {
		return fmt.Errorf("asset is built for %s, this machine is %s", want, goarch)
	}
	return nil
}
