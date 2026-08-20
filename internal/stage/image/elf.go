package image

import (
	"debug/elf"
	"fmt"
	"io"
)

// verifyELF reports whether r is a static 64-bit little-endian executable for arch.
//
// The file must be ELFCLASS64, ELFDATA2LSB, and ET_EXEC. The machine must be
// EM_X86_64 for [ArchX8664] or EM_AARCH64 for [ArchAArch64]. A PT_INTERP
// program header or a DT_NEEDED dynamic entry means the binary is not
// statically linked. r must implement [io.ReaderAt]; callers obtain that from
// the [*os.Root]-opened staged file.
func verifyELF(arch APKArch, r io.ReaderAt) error {
	if r == nil {
		return fmt.Errorf("staged %s binary does not provide io.ReaderAt", arch)
	}

	file, err := elf.NewFile(r)
	if err != nil {
		return fmt.Errorf("parse ELF: %w", err)
	}
	defer file.Close()

	if file.Class != elf.ELFCLASS64 {
		return fmt.Errorf("ELF class %s, want ELFCLASS64", file.Class)
	}
	if file.Data != elf.ELFDATA2LSB {
		return fmt.Errorf("ELF data %s, want ELFDATA2LSB", file.Data)
	}
	if file.Type != elf.ET_EXEC {
		return fmt.Errorf("ELF type %s, want ET_EXEC", file.Type)
	}

	wantMachine, err := expectedMachine(arch)
	if err != nil {
		return err
	}
	if file.Machine != wantMachine {
		return fmt.Errorf("ELF machine %s, want %s", file.Machine, wantMachine)
	}

	for _, prog := range file.Progs {
		if prog.Type == elf.PT_INTERP {
			return fmt.Errorf("ELF for %s has a PT_INTERP program header", arch)
		}
	}

	needed, err := file.ImportedLibraries()
	if err != nil {
		return fmt.Errorf("read DT_NEEDED entries: %w", err)
	}
	if len(needed) > 0 {
		return fmt.Errorf("ELF for %s is dynamically linked (DT_NEEDED %s)", arch, needed[0])
	}

	return nil
}

// expectedMachine returns the ELF machine that arch must use.
func expectedMachine(arch APKArch) (elf.Machine, error) {
	switch arch {
	case ArchX8664:
		return elf.EM_X86_64, nil
	case ArchAArch64:
		return elf.EM_AARCH64, nil
	default:
		return elf.EM_NONE, fmt.Errorf("unsupported APK architecture %q", arch)
	}
}
