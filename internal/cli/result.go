package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Schema is the versioned JSON envelope identifier.
const Schema = "release.dev/result/v1"

// Protocol is the workflow/binary contract integer.
//
// It is a source constant, not an ldflag, and is guarded by
// scripts/check-protocol-stamp.sh against EXPECTED_PROTOCOL in the
// setup-release-cli composite action.
const Protocol = 1

const (
	// exitSuccess is a successful command.
	exitSuccess = 0
	// exitFailure is a contract or verification failure.
	exitFailure = 1
	// exitUsage is a usage or configuration error.
	exitUsage = 2
)

// ErrUsage marks a usage or configuration error (exit 2).
var ErrUsage = errors.New("usage")

// Envelope is the single JSON document emitted under --json.
//
// Schema is always [Schema]. Command is the verb path ("stage", "plan tags",
// "version", or "verify handoff"). OK is true only on success. Result is
// command-specific and must not be nil in a written document. A zero
// Envelope is invalid and is never encoded.
type Envelope struct {
	// Schema identifies the envelope version.
	Schema string `json:"schema"`
	// Command is the verb path that produced the document.
	Command string `json:"command"`
	// OK is true when the command succeeded.
	OK bool `json:"ok"`
	// Result is the command-specific payload.
	Result any `json:"result"`
}

// VersionResult is the --json payload for version.
type VersionResult struct {
	// Version is the stamped release version.
	Version string `json:"version"`
	// Commit is the stamped source commit.
	Commit string `json:"commit"`
	// Protocol is the workflow/binary contract integer.
	Protocol int `json:"protocol"`
}

// HomebrewTapInitResult is the --json payload for init homebrew-tap.
type HomebrewTapInitResult struct {
	// Tap is the initialized owner/homebrew-name repository.
	Tap string `json:"tap"`
	// Output is the local scaffold directory.
	Output string `json:"output"`
	// Files lists every generated slash-separated path in lexical order.
	Files []string `json:"files"`
}

// BinaryResult describes one verified canonical binary.
type BinaryResult struct {
	// Path is the original GoReleaser path, including the --dist basename prefix.
	Path string `json:"path"`
	// Mode is the observed permission bits as an octal string.
	Mode string `json:"mode"`
}

// StageResult is the --json payload for stage.
type StageResult struct {
	// Assets is the number of checksummed payloads that matched.
	Assets int `json:"assets"`
	// Binaries maps GOARCH onto the verified binary path and mode.
	Binaries map[string]BinaryResult `json:"binaries"`
}

// ArtifactHandoffResult is one verified Actions artifact.
type ArtifactHandoffResult struct {
	// ID is the GitHub artifact identifier.
	ID int64 `json:"id"`
	// Name is the GitHub artifact name.
	Name string `json:"name"`
	// Digest is the GitHub-reported digest, always sha256-prefixed.
	Digest string `json:"digest"`
	// SizeBytes is the reported archive size.
	SizeBytes int64 `json:"size_bytes"`
	// RunID is the workflow run that owns the artifact.
	RunID int64 `json:"run_id"`
	// ExpiresAt is the RFC3339 expiry instant, or empty when GitHub omitted it.
	ExpiresAt string `json:"expires_at"`
}

// HandoffResult is the --json payload for verify handoff.
type HandoffResult struct {
	// Artifact is the verified Actions artifact metadata.
	Artifact ArtifactHandoffResult `json:"artifact"`
}

// ErrorResult is the --json payload for a failed command.
type ErrorResult struct {
	// Error is the diagnostic string also written to stderr.
	Error string `json:"error"`
}

// ExitCode maps err onto the process contract.
//
// nil is 0. Errors wrapping [ErrUsage] are 2. Every other error is 1.
func ExitCode(err error) int {
	if err == nil {
		return exitSuccess
	}
	if errors.Is(err, ErrUsage) {
		return exitUsage
	}

	return exitFailure
}

// UsageError wraps err as a usage or configuration failure.
func UsageError(err error) error {
	if err == nil {
		return ErrUsage
	}

	return fmt.Errorf("%w: %w", ErrUsage, err)
}

// writeEnvelope writes one JSON result document to w.
func writeEnvelope(w io.Writer, command string, ok bool, result any) error {
	payload, err := json.Marshal(Envelope{
		Schema:  Schema,
		Command: command,
		OK:      ok,
		Result:  result,
	})
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(payload)); err != nil {
		return fmt.Errorf("write result: %w", err)
	}

	return nil
}

// writeCommandResult emits the --json envelope for a completed command.
//
// Flag-parse failures never reach this helper. A nil result with a nil error
// writes nothing, which is the silent success path.
func writeCommandResult(options Options, command string, result any, err error) error {
	if options.settings == nil || !options.settings.JSON {
		return err
	}
	if err != nil {
		if writeErr := writeEnvelope(options.Out, command, false, ErrorResult{Error: err.Error()}); writeErr != nil {
			return errors.Join(err, fmt.Errorf("write result: %w", writeErr))
		}
		return err
	}
	if result == nil {
		return nil
	}

	return writeEnvelope(options.Out, command, true, result)
}
