package stage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/meigma/release/internal/profile/goprof"
)

// Stage verifies a Go profile dist directory whose basename is root.
//
// It parses checksums.txt, streams every claimed payload through SHA-256,
// requires a nonempty regular checksums.txt.sigstore.json, selects exactly
// one linux/amd64 and one linux/arm64 Binary from artifacts.json, and
// confirms each selected path is a confined regular executable. Each
// selected binary is then streamed through SHA-256 so the report can
// carry its digest and filename. A nil filesystem is rejected.
func Stage(fsys fs.FS, root goprof.RootName) (Report, error) {
	if fsys == nil {
		return Report{}, errors.New("filesystem is nil")
	}

	return stage(fsys, root)
}

// stage verifies after the exported nil check.
func stage(fsys fs.FS, root goprof.RootName) (Report, error) {
	checksums, err := fsys.Open(checksumsName)
	if err != nil {
		return Report{}, fmt.Errorf("open %s: %w", checksumsName, err)
	}
	claim, err := parseChecksums(checksums)
	closeErr := checksums.Close()
	if err != nil {
		return Report{}, err
	}
	if closeErr != nil {
		return Report{}, fmt.Errorf("close %s: %w", checksumsName, closeErr)
	}
	if err = verifyBundle(fsys, claim); err != nil {
		return Report{}, err
	}

	artifacts, err := fsys.Open(artifactsName)
	if err != nil {
		return Report{}, fmt.Errorf("open %s: %w", artifactsName, err)
	}
	records, err := goprof.ParseArtifacts(artifacts)
	closeErr = artifacts.Close()
	if err != nil {
		return Report{}, err
	}
	if closeErr != nil {
		return Report{}, fmt.Errorf("close %s: %w", artifactsName, closeErr)
	}

	binaries, err := goprof.SelectBinaries(records, root)
	if err != nil {
		return Report{}, err
	}
	if err := goprof.VerifyBinaries(fsys, binaries); err != nil {
		return Report{}, err
	}

	report := Report{Assets: claim.Len()}
	for _, binary := range binaries {
		info, err := fs.Stat(fsys, binary.RelativePath.String())
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", binary.Path, err)
		}
		digest, err := hashBinary(fsys, binary.RelativePath.String())
		if err != nil {
			return Report{}, err
		}
		report.Binaries = append(report.Binaries, Binary{
			Arch:         binary.Arch.String(),
			Path:         binary.Path.String(),
			RelativePath: binary.RelativePath.String(),
			Name:         binary.Name.String(),
			Digest:       digest,
			Mode:         info.Mode().Perm(),
		})
	}

	return report, nil
}

// hashBinary streams name through SHA-256 and returns the lowercase hex digest.
func hashBinary(fsys fs.FS, name string) (Digest, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", name, err)
	}

	return Digest(hex.EncodeToString(sum.Sum(nil))), nil
}
