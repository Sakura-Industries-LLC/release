package ghattest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// defaultPath is resolved from PATH when [Options.Path] is empty.
	defaultPath = "gh"
	// tokenVariable carries the GitHub token to the child process.
	//
	// The value is a variable name, not a credential.
	tokenVariable = "GH_TOKEN"
)

var (
	// commitPattern accepts one full lowercase source commit SHA.
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Options configures a [Verifier].
type Options struct {
	// Path is the gh executable. Empty resolves "gh" from PATH.
	Path string
	// Token authenticates attestation lookup and is supplied only through GH_TOKEN.
	Token rel.Secret
	// Environ is the child process environment. Nil inherits [os.Environ].
	Environ []string
	// Stderr receives live gh diagnostics. Nil discards them.
	Stderr io.Writer
}

// Verifier invokes gh attestation verify with exact source and signer constraints.
type Verifier struct {
	// path is the configured gh executable.
	path string
	// environ returns the child environment with GH_TOKEN applied.
	environ func() []string
	// stderr receives live gh diagnostics.
	stderr io.Writer
}

// New constructs a GitHub attestation verifier.
func New(options Options) *Verifier {
	token := options.Token.Reveal()
	base := options.Environ

	return &Verifier{
		path: options.Path,
		environ: func() []string {
			environ := base
			if environ == nil {
				environ = os.Environ()
			}

			return applyToken(environ, token)
		},
		stderr: options.Stderr,
	}
}

// Verify implements [pkgrepo.Attestor].
//
// It requires a regular absolute artifact path and invokes `gh attestation
// verify` with exact repository, signer workflow, tag ref, and source digest
// constraints plus `--deny-self-hosted-runners`. The token is never included
// in argv or returned errors.
func (v *Verifier) Verify(ctx context.Context, request pkgrepo.AttestationRequest) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if v == nil || v.environ == nil {
		return errors.New("attestation verifier is nil")
	}
	if !filepath.IsAbs(request.Path) {
		return fmt.Errorf("attestation path %q is not absolute", request.Path)
	}
	info, err := os.Stat(request.Path)
	if err != nil {
		return fmt.Errorf("stat attestation payload: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("attestation payload is not a regular file")
	}
	if _, parseErr := pkgrepo.ParseRepository(string(request.Repository)); parseErr != nil {
		return parseErr
	}
	tag, found := strings.CutPrefix(request.SourceRef, "refs/tags/")
	if !found {
		return fmt.Errorf("source ref %q is not an exact tag ref", request.SourceRef)
	}
	if _, parseErr := pkgrepo.ParseTag(tag); parseErr != nil {
		return parseErr
	}
	if !commitPattern.MatchString(request.SourceDigest) {
		return fmt.Errorf("source digest %q is not a full lowercase SHA", request.SourceDigest)
	}
	workflowPrefix := string(request.Repository) + "/.github/workflows/"
	if !strings.HasPrefix(request.SignerWorkflow, workflowPrefix) ||
		strings.Contains(strings.TrimPrefix(request.SignerWorkflow, workflowPrefix), "/") {
		return fmt.Errorf("signer workflow %q does not belong to %q", request.SignerWorkflow, request.Repository)
	}

	err = execx.Run(ctx, execx.Command{
		Program: defaultPath,
		Path:    v.path,
		Args: []string{
			"attestation", "verify", request.Path,
			"--repo", string(request.Repository),
			"--signer-workflow", request.SignerWorkflow,
			"--source-ref", request.SourceRef,
			"--source-digest", request.SourceDigest,
			"--deny-self-hosted-runners",
		},
		Env:    v.environ(),
		Stderr: v.stderr,
	})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("verify attestation: %w", contextErr)
		}

		return commandError(err)
	}

	return nil
}

// applyToken returns a copy of environ with exactly one GH_TOKEN entry.
func applyToken(environ []string, token string) []string {
	prefix := tokenVariable + "="
	child := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		child = append(child, entry)
	}
	child = append(child, prefix+token)

	return child
}

// commandError maps one process failure without exposing arguments or environment.
func commandError(err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("verify attestation: %w", err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("verify attestation: %w", err)
	}
	if tail := strings.TrimSpace(runErr.StderrTail()); tail != "" {
		return fmt.Errorf("gh exited with code %d: %s", exitCode, tail)
	}

	return fmt.Errorf("gh exited with code %d", exitCode)
}
