package gpg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// defaultPath is resolved from PATH when [Options.Path] is empty.
	defaultPath = "gpg"
)

// Options configures a [Signer].
type Options struct {
	// Path overrides the gpg executable. Empty resolves "gpg" from PATH.
	Path string
	// Home is the absolute GnuPG home containing the aggregate metadata key.
	Home string
	// KeyID selects the aggregate metadata signing key.
	KeyID string
	// PassphraseFile is the absolute owner-only file containing the key passphrase.
	PassphraseFile string
	// Environ is the child process environment. Nil inherits the parent.
	Environ []string
	// Stderr receives live GnuPG diagnostics. Nil discards them.
	Stderr io.Writer
}

// Signer creates deterministic aggregate APT and RPM OpenPGP signatures.
//
// The passphrase is supplied by file path and never appears as an argument,
// environment value, returned error, or adapter output.
type Signer struct {
	// path is the gpg executable override.
	path string
	// home is the configured GnuPG home.
	home string
	// keyID is the configured aggregate signing key.
	keyID string
	// passphraseFile is the owner-only passphrase file.
	passphraseFile string
	// environ is the child process environment.
	environ []string
	// stderr receives live GnuPG diagnostics.
	stderr io.Writer
}

// New constructs a [Signer]. Configuration is validated when signing.
func New(options Options) *Signer {
	return &Signer{
		path:           options.Path,
		home:           options.Home,
		keyID:          options.KeyID,
		passphraseFile: options.PassphraseFile,
		environ:        options.Environ,
		stderr:         options.Stderr,
	}
}

// ClearSign implements [pkgrepo.Signer].
func (s *Signer) ClearSign(ctx context.Context, request pkgrepo.SignRequest) error {
	return s.sign(ctx, request, "--clearsign")
}

// DetachSign implements [pkgrepo.Signer].
func (s *Signer) DetachSign(ctx context.Context, request pkgrepo.SignRequest) error {
	return s.sign(ctx, request, "--armor", "--detach-sign")
}

// sign validates configuration and invokes GnuPG with fixed deterministic arguments.
func (s *Signer) sign(ctx context.Context, request pkgrepo.SignRequest, operation ...string) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if s == nil {
		return errors.New("GnuPG signer is nil")
	}
	if !filepath.IsAbs(s.home) {
		return fmt.Errorf("GnuPG home %q is not absolute", s.home)
	}
	if err := validateOwnerOnlyPath(s.home, "GnuPG home", true); err != nil {
		return err
	}
	if s.keyID == "" {
		return errors.New("GnuPG key ID is empty")
	}
	if !filepath.IsAbs(s.passphraseFile) {
		return fmt.Errorf("GnuPG passphrase file %q is not absolute", s.passphraseFile)
	}
	if err := validateOwnerOnlyPath(s.passphraseFile, "GnuPG passphrase file", false); err != nil {
		return err
	}
	if !filepath.IsAbs(request.Input) {
		return fmt.Errorf("signature input %q is not absolute", request.Input)
	}
	if !filepath.IsAbs(request.Output) {
		return fmt.Errorf("signature output %q is not absolute", request.Output)
	}
	if request.Time.IsZero() {
		return errors.New("signature time is zero")
	}

	args := []string{
		"--homedir", s.home,
		"--batch", "--yes",
		"--pinentry-mode", "loopback",
		"--passphrase-file", s.passphraseFile,
		"--faked-system-time", strconv.FormatInt(request.Time.Unix(), 10),
		"--local-user", s.keyID,
	}
	args = append(args, operation...)
	args = append(args, "--output", request.Output, request.Input)
	err := execx.Run(ctx, execx.Command{
		Program: defaultPath,
		Path:    s.path,
		Args:    args,
		Env:     s.environ,
		Stderr:  s.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return commandError(err)
	}

	return nil
}

// validateOwnerOnlyPath requires an existing owner-only regular file or directory.
func validateOwnerOnlyPath(name, label string, wantDirectory bool) error {
	info, err := os.Stat(name)
	if err != nil {
		return fmt.Errorf("stat %s: %w", label, err)
	}
	if info.IsDir() != wantDirectory {
		kind := "regular file"
		if wantDirectory {
			kind = "directory"
		}
		return fmt.Errorf("%s is not a %s", label, kind)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s permissions %04o allow group or other access", label, info.Mode().Perm())
	}

	return nil
}

// commandError maps one process failure without exposing arguments or environment.
func commandError(err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return err
	}
	if exitCode, exited := runErr.ExitCode(); exited {
		if tail := strings.TrimSpace(runErr.StderrTail()); tail != "" {
			return fmt.Errorf("gpg exited with code %d: %s", exitCode, tail)
		}
		return fmt.Errorf("gpg exited with code %d", exitCode)
	}

	return err
}
