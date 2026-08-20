package image

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifyELF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		arch    APKArch
		data    []byte
		wantErr string
	}{
		{
			name: "static amd64",
			arch: ArchX8664,
			data: staticELF(elf.EM_X86_64),
		},
		{
			name: "static arm64",
			arch: ArchAArch64,
			data: staticELF(elf.EM_AARCH64),
		},
		{
			name:    "wrong class",
			arch:    ArchX8664,
			data:    elf32(elf.EM_X86_64),
			wantErr: "ELF class ELFCLASS32, want ELFCLASS64",
		},
		{
			name:    "wrong endianness",
			arch:    ArchX8664,
			data:    elf64(elf.ELFDATA2MSB, elf.ET_EXEC, elf.EM_X86_64, binary.BigEndian, nil, nil, 0),
			wantErr: "ELF data ELFDATA2MSB, want ELFDATA2LSB",
		},
		{
			name:    "wrong machine for amd64",
			arch:    ArchX8664,
			data:    staticELF(elf.EM_AARCH64),
			wantErr: "ELF machine EM_AARCH64, want EM_X86_64",
		},
		{
			name:    "wrong machine for arm64",
			arch:    ArchAArch64,
			data:    staticELF(elf.EM_X86_64),
			wantErr: "ELF machine EM_X86_64, want EM_AARCH64",
		},
		{
			name:    "dynamic object type",
			arch:    ArchX8664,
			data:    elf64(elf.ELFDATA2LSB, elf.ET_DYN, elf.EM_X86_64, binary.LittleEndian, nil, nil, 0),
			wantErr: "ELF type ET_DYN, want ET_EXEC",
		},
		{
			name:    "PT_INTERP program header",
			arch:    ArchX8664,
			data:    elfWithInterp(elf.EM_X86_64),
			wantErr: "ELF for x86_64 has a PT_INTERP program header",
		},
		{
			name:    "DT_NEEDED dynamic entry",
			arch:    ArchX8664,
			data:    elfWithNeeded(elf.EM_X86_64),
			wantErr: "ELF for x86_64 is dynamically linked (DT_NEEDED libc.so.6)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "binary")
			require.NoError(t, os.WriteFile(path, test.data, 0o755))
			file, err := os.Open(path)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, file.Close())
			})

			err = verifyELF(test.arch, file)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestVerifyELFRejectsMissingReaderAt(t *testing.T) {
	t.Parallel()

	err := verifyELF(ArchX8664, nil)
	require.EqualError(t, err, "staged x86_64 binary does not provide io.ReaderAt")
}

// staticELF returns a minimal static 64-bit little-endian ET_EXEC for machine.
func staticELF(machine elf.Machine) []byte {
	return elf64(elf.ELFDATA2LSB, elf.ET_EXEC, machine, binary.LittleEndian, nil, nil, 0)
}

// elf32 returns a 32-bit little-endian ET_EXEC that NewFile accepts.
func elf32(machine elf.Machine) []byte {
	var hdr elf.Header32
	hdr.Ident[0] = 0x7f
	hdr.Ident[1] = 'E'
	hdr.Ident[2] = 'L'
	hdr.Ident[3] = 'F'
	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS32)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	hdr.Type = uint16(elf.ET_EXEC)
	hdr.Machine = uint16(machine)
	hdr.Version = uint32(elf.EV_CURRENT)
	hdr.Ehsize = uint16(binary.Size(hdr))
	hdr.Phentsize = uint16(binary.Size(elf.Prog32{}))
	hdr.Shentsize = uint16(binary.Size(elf.Section32{}))

	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, hdr); err != nil {
		panic(err)
	}

	return buf.Bytes()
}

// elfWithInterp returns a 64-bit ET_EXEC that carries a PT_INTERP header.
func elfWithInterp(machine elf.Machine) []byte {
	prog := elf.Prog64{Type: uint32(elf.PT_INTERP)}

	return elf64(elf.ELFDATA2LSB, elf.ET_EXEC, machine, binary.LittleEndian, []elf.Prog64{prog}, nil, 0)
}

// elfWithNeeded returns a 64-bit ET_EXEC whose dynamic section lists libc.
func elfWithNeeded(machine elf.Machine) []byte {
	const (
		dynOff    = 64
		dynSize   = 32
		strOff    = dynOff + dynSize
		strTab    = "\x00libc.so.6\x00"
		shOff     = 128
		dynLink   = 2
		sectionN  = 3
		neededVal = 1
	)

	dyn := make([]byte, 0, shOff)
	dyn = binary.LittleEndian.AppendUint64(dyn, uint64(elf.DT_NEEDED))
	dyn = binary.LittleEndian.AppendUint64(dyn, neededVal)
	dyn = append(dyn, make([]byte, dynSize-16)...)
	dyn = append(dyn, strTab...)
	dyn = append(dyn, make([]byte, shOff-strOff-len(strTab))...)

	sections := make([]elf.Section64, sectionN)
	sections[1] = elf.Section64{
		Type: uint32(elf.SHT_DYNAMIC),
		Off:  dynOff,
		Size: dynSize,
		Link: dynLink,
	}
	sections[2] = elf.Section64{
		Type: uint32(elf.SHT_STRTAB),
		Off:  strOff,
		Size: uint64(len(strTab)),
	}

	return elf64(elf.ELFDATA2LSB, elf.ET_EXEC, machine, binary.LittleEndian, nil, sections, shOff, dyn...)
}

// elf64 encodes a 64-bit ELF header, optional program headers, payload, and sections.
func elf64(
	data elf.Data,
	typ elf.Type,
	machine elf.Machine,
	order binary.ByteOrder,
	progs []elf.Prog64,
	sections []elf.Section64,
	shoff uint64,
	payload ...byte,
) []byte {
	var hdr elf.Header64
	hdr.Ident[0] = 0x7f
	hdr.Ident[1] = 'E'
	hdr.Ident[2] = 'L'
	hdr.Ident[3] = 'F'
	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(data)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	hdr.Type = uint16(typ)
	hdr.Machine = uint16(machine)
	hdr.Version = uint32(elf.EV_CURRENT)
	hdr.Ehsize = uint16(binary.Size(hdr))
	hdr.Phentsize = uint16(binary.Size(elf.Prog64{}))
	hdr.Phnum = uint16(len(progs))
	hdr.Shentsize = uint16(binary.Size(elf.Section64{}))
	hdr.Shnum = uint16(len(sections))
	hdr.Shoff = shoff
	if len(progs) > 0 {
		hdr.Phoff = uint64(binary.Size(hdr))
	}

	var buf bytes.Buffer
	if err := binary.Write(&buf, order, hdr); err != nil {
		panic(err)
	}
	for _, prog := range progs {
		if err := binary.Write(&buf, order, prog); err != nil {
			panic(err)
		}
	}
	if _, err := buf.Write(payload); err != nil {
		panic(err)
	}
	if shoff > 0 {
		if pad := int(shoff) - buf.Len(); pad > 0 {
			buf.Write(make([]byte, pad))
		}
	}
	for _, section := range sections {
		if err := binary.Write(&buf, order, section); err != nil {
			panic(err)
		}
	}

	return buf.Bytes()
}
