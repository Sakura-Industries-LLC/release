package pubgh_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	ghactmocks "github.com/meigma/release/internal/adapter/ghact/mocks"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	validDigest   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherDigest   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	unprefixedHex = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

func TestParseArtifactID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr string
	}{
		{name: "positive identifier", input: "42", want: 42},
		{name: "trims surrounding space", input: "  7  ", want: 7},
		{name: "empty", input: "", wantErr: "artifact id is empty"},
		{name: "zero", input: "0", wantErr: "not positive"},
		{name: "negative", input: "-1", wantErr: "not positive"},
		{name: "not an integer", input: "1.5", wantErr: "invalid artifact id"},
		{name: "above safe integer range", input: "9007199254740992", wantErr: "exceeds the safe integer range"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := pubgh.ParseArtifactID(test.input)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.Int64())
		})
	}
}

func TestParseRunID(t *testing.T) {
	t.Parallel()

	got, err := pubgh.ParseRunID("99")
	require.NoError(t, err)
	assert.Equal(t, int64(99), got.Int64())

	_, err = pubgh.ParseRunID("0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not positive")
}

func TestParseArtifactDigest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "prefixed lowercase",
			input: validDigest,
			want:  validDigest,
		},
		{
			name:  "unprefixed uppercase is normalized",
			input: unprefixedHex,
			want:  validDigest,
		},
		{
			name:  "prefixed uppercase is normalized",
			input: "SHA256:" + unprefixedHex,
			want:  validDigest,
		},
		{
			name:    "empty digest",
			input:   "",
			wantErr: "artifact digest is empty",
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: "artifact digest is empty",
		},
		{
			name:    "wrong length",
			input:   "sha256:aaaa",
			wantErr: "has length 4, want 64",
		},
		{
			name:    "non hex",
			input:   "sha256:zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			wantErr: "is not hexadecimal",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := pubgh.ParseArtifactDigest(test.input)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}

func TestParseRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "owner name pair", input: "meigma/release", want: "meigma/release"},
		{name: "empty", input: "", wantErr: "repository is empty"},
		{name: "missing name", input: "meigma/", wantErr: "not an owner/name pair"},
		{name: "missing owner", input: "/release", wantErr: "not an owner/name pair"},
		{name: "extra slash", input: "meigma/release/extra", wantErr: "not an owner/name pair"},
		{name: "no slash", input: "release", wantErr: "not an owner/name pair"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := pubgh.ParseRepository(test.input)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}

func TestVerifyHandoff(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		ctx      func() context.Context
		setup    func(meta *ghactmocks.MockArtifactMeta)
		expected pubgh.Handoff
		wantErr  string
		wantSent error
	}{
		{
			name: "matching same-run artifact",
			setup: func(meta *ghactmocks.MockArtifactMeta) {
				meta.EXPECT().
					Get(mock.Anything, expected.Repository, expected.Artifact).
					Return(pubgh.ArtifactMetadata{
						ID:        expected.Artifact,
						Name:      "release-assets",
						Digest:    expected.Digest,
						SizeBytes: 12,
						HasRun:    true,
						Run:       expected.Run,
						ExpiresAt: now,
					}, nil).
					Once()
			},
			expected: expected,
		},
		{
			name: "expired artifact",
			setup: func(meta *ghactmocks.MockArtifactMeta) {
				meta.EXPECT().
					Get(mock.Anything, expected.Repository, expected.Artifact).
					Return(pubgh.ArtifactMetadata{
						ID:      expected.Artifact,
						Digest:  expected.Digest,
						HasRun:  true,
						Run:     expected.Run,
						Expired: true,
					}, nil).
					Once()
			},
			expected: expected,
			wantErr:  "has expired",
			wantSent: pubgh.ErrHandoffMismatch,
		},
		{
			name: "wrong run",
			setup: func(meta *ghactmocks.MockArtifactMeta) {
				meta.EXPECT().
					Get(mock.Anything, expected.Repository, expected.Artifact).
					Return(pubgh.ArtifactMetadata{
						ID:     expected.Artifact,
						Digest: expected.Digest,
						HasRun: true,
						Run:    mustRunID(t, 200),
					}, nil).
					Once()
			},
			expected: expected,
			wantErr:  "belongs to workflow run 200, expected 100",
			wantSent: pubgh.ErrHandoffMismatch,
		},
		{
			name: "nil workflow-run metadata",
			setup: func(meta *ghactmocks.MockArtifactMeta) {
				meta.EXPECT().
					Get(mock.Anything, expected.Repository, expected.Artifact).
					Return(pubgh.ArtifactMetadata{
						ID:     expected.Artifact,
						Digest: expected.Digest,
					}, nil).
					Once()
			},
			expected: expected,
			wantErr:  "no workflow-run metadata",
			wantSent: pubgh.ErrHandoffMismatch,
		},
		{
			name: "digest mismatch",
			setup: func(meta *ghactmocks.MockArtifactMeta) {
				meta.EXPECT().
					Get(mock.Anything, expected.Repository, expected.Artifact).
					Return(pubgh.ArtifactMetadata{
						ID:     expected.Artifact,
						Digest: mustDigest(t, otherDigest),
						HasRun: true,
						Run:    expected.Run,
					}, nil).
					Once()
			},
			expected: expected,
			wantErr:  "expected " + validDigest + ", got " + otherDigest,
			wantSent: pubgh.ErrHandoffMismatch,
		},
		{
			name: "absent artifact",
			setup: func(meta *ghactmocks.MockArtifactMeta) {
				meta.EXPECT().
					Get(mock.Anything, expected.Repository, expected.Artifact).
					Return(pubgh.ArtifactMetadata{}, errors.New("artifact not found")).
					Once()
			},
			expected: expected,
			wantErr:  "artifact not found",
		},
		{
			name: "cancelled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			setup:    func(_ *ghactmocks.MockArtifactMeta) {},
			expected: expected,
			wantErr:  context.Canceled.Error(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			meta := ghactmocks.NewMockArtifactMeta(t)
			test.setup(meta)

			ctx := context.Background()
			if test.ctx != nil {
				ctx = test.ctx()
			}

			got, err := pubgh.VerifyHandoff(ctx, meta, test.expected, instantSleep)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				if test.wantSent != nil {
					require.ErrorIs(t, err, test.wantSent)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, expected.Artifact, got.ID)
			assert.Equal(t, expected.Digest, got.Digest)
			assert.Equal(t, expected.Run, got.Run)
		})
	}
}

func TestVerifyHandoffRejectsNilPort(t *testing.T) {
	t.Parallel()

	_, err := pubgh.VerifyHandoff(
		context.Background(),
		nil,
		mustHandoff(t, 1),
		instantSleep,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact metadata port is nil")
}

func TestNewHandoffRejectsZeroValues(t *testing.T) {
	t.Parallel()

	_, err := pubgh.NewHandoff(pubgh.Repository{}, 0, 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository is empty")
}

// mustHandoff constructs a valid expected tuple or fails the test.
func mustHandoff(t *testing.T, run int64) pubgh.Handoff {
	t.Helper()

	repo, err := pubgh.ParseRepository("meigma/release")
	require.NoError(t, err)
	runID, err := pubgh.RunIDFromInt(run)
	require.NoError(t, err)
	artifactID, err := pubgh.ArtifactIDFromInt(1)
	require.NoError(t, err)
	parsed, err := pubgh.ParseArtifactDigest(validDigest)
	require.NoError(t, err)
	handoff, err := pubgh.NewHandoff(repo, runID, artifactID, parsed)
	require.NoError(t, err)

	return handoff
}

// mustRunID constructs a RunID or fails the test.
func mustRunID(t *testing.T, value int64) pubgh.RunID {
	t.Helper()

	id, err := pubgh.RunIDFromInt(value)
	require.NoError(t, err)

	return id
}

// mustDigest constructs an ArtifactDigest or fails the test.
func mustDigest(t *testing.T, value string) pubgh.ArtifactDigest {
	t.Helper()

	digest, err := pubgh.ParseArtifactDigest(value)
	require.NoError(t, err)

	return digest
}

func TestArtifactIDFromIntRejectsUnsafe(t *testing.T) {
	t.Parallel()

	_, err := pubgh.ArtifactIDFromInt(0)
	require.Error(t, err)
	_, err = pubgh.ArtifactIDFromInt(1 << 53)
	require.Error(t, err)
}

func TestVerifyHandoffWrapsPortError(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{}, errors.New("boom")).
		Once()

	_, err := pubgh.VerifyHandoff(context.Background(), meta, expected, instantSleep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get artifact 1")
	assert.Contains(t, err.Error(), "boom")
}

func TestVerifyHandoffRetriesRetryableThenSucceeds(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{}, pubgh.ErrRetryable).
		Times(3)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{
			ID:     expected.Artifact,
			Digest: expected.Digest,
			HasRun: true,
			Run:    expected.Run,
		}, nil).
		Once()

	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	got, err := pubgh.VerifyHandoff(context.Background(), meta, expected, sleep)
	require.NoError(t, err)
	assert.Equal(t, expected.Artifact, got.ID)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, waits)
}

func TestVerifyHandoffRetryBudgetExhausted(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{}, pubgh.ErrRetryable).
		Times(4)

	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	_, err := pubgh.VerifyHandoff(context.Background(), meta, expected, sleep)
	require.Error(t, err)
	require.ErrorIs(t, err, pubgh.ErrRetryable)
	assert.Contains(t, err.Error(), "after 4 attempts")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, waits)
}

func TestVerifyHandoffDoesNotRetryAbsent(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{}, errors.New("artifact not found")).
		Once()

	_, err := pubgh.VerifyHandoff(context.Background(), meta, expected, instantSleep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact not found")
	assert.NotErrorIs(t, err, pubgh.ErrRetryable)
}

func TestVerifyHandoffCancelDuringBackoff(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{}, pubgh.ErrRetryable).
		Once()

	ctx, cancel := context.WithCancel(context.Background())
	sleep := func(_ context.Context, _ time.Duration) error {
		cancel()
		return context.Canceled
	}
	_, err := pubgh.VerifyHandoff(ctx, meta, expected, sleep)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestVerifyHandoffSleepFailureKeepsPrefix(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{}, pubgh.ErrRetryable).
		Once()

	sleepErr := errors.New("clock stopped")
	sleep := func(_ context.Context, _ time.Duration) error {
		return sleepErr
	}
	_, err := pubgh.VerifyHandoff(context.Background(), meta, expected, sleep)
	require.Error(t, err)
	require.ErrorIs(t, err, sleepErr)
	assert.Contains(t, err.Error(), "verify handoff:")
	assert.NotContains(t, err.Error(), "get artifact")
}

func TestVerifyHandoffPortCanceledKeepsGetPrefix(t *testing.T) {
	t.Parallel()

	expected := mustHandoff(t, 100)
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, expected.Repository, expected.Artifact).
		Return(pubgh.ArtifactMetadata{}, fmt.Errorf("lookup: %w", context.Canceled)).
		Once()

	_, err := pubgh.VerifyHandoff(context.Background(), meta, expected, instantSleep)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "get artifact 1:")
	assert.NotContains(t, err.Error(), "verify handoff:")
}

// instantSleep is a SleepFunc that never waits.
func instantSleep(_ context.Context, _ time.Duration) error {
	return nil
}
