package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	regmocks "github.com/meigma/release/internal/adapter/reg/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	tagsDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tagsOther  = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tagsToken  = "ghs_should_never_appear"
	tagsImage  = "ghcr.io/owner/repo"
)

func TestPlanTagsMissingDigestIsUsage(t *testing.T) {
	t.Parallel()

	called := false
	stdout, err := executeTagsFactory(t, map[string]string{
		"RELEASE_IMAGE":   tagsImage,
		"RELEASE_VERSION": "1.2.3",
	}, []string{"plan", "tags"}, func(cli.RegistryCredentials) (puboci.StateReader, error) {
		called = true
		return unusedReader(t), nil
	})
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Contains(t, err.Error(), "--digest is required")
	assert.False(t, called)
}

func TestPlanTagsMalformedValuesAreUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{
			name: "malformed digest",
			args: []string{"plan", "tags", "--image", tagsImage, "--version", "1.2.3", "--digest", "not-a-digest"},
			want: "digest",
		},
		{
			name: "malformed version",
			args: []string{"plan", "tags", "--image", tagsImage, "--version", "v1.2.3", "--digest", tagsDigest},
			want: "v prefix",
		},
		{
			name: "malformed image",
			args: []string{
				"plan", "tags",
				"--image", "GHCR.IO/OWNER/REPO",
				"--version", "1.2.3",
				"--digest", tagsDigest,
			},
			want: "uppercase",
		},
		{
			name: "missing repository without image",
			env:  map[string]string{"GITHUB_REF_NAME": "v1.2.3"},
			args: []string{"plan", "tags", "--digest", tagsDigest},
			want: "GITHUB_REPOSITORY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executeTagsFactory(
				t,
				tt.env,
				tt.args,
				func(cli.RegistryCredentials) (puboci.StateReader, error) {
					called = true
					return unusedReader(t), nil
				},
			)
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Empty(t, stdout)
			assert.Contains(t, err.Error(), tt.want)
			assert.False(t, called)
		})
	}
}

func TestPlanTagsJSONConfigFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "malformed digest",
			args: []string{
				"plan",
				"tags",
				"--json",
				"--image",
				tagsImage,
				"--version",
				"1.2.3",
				"--digest",
				"not-a-digest",
			},
			want: "digest",
		},
		{
			name: "malformed version",
			args: []string{
				"plan",
				"tags",
				"--json",
				"--image",
				tagsImage,
				"--version",
				"v1.2.3",
				"--digest",
				tagsDigest,
			},
			want: "v prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executeTagsFactory(
				t,
				nil,
				tt.args,
				func(cli.RegistryCredentials) (puboci.StateReader, error) {
					called = true
					return unusedReader(t), nil
				},
			)
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.False(t, called)
			assert.Equal(t, 1, countJSONDocuments(stdout))

			var envelope cli.Envelope
			require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
			assert.Equal(t, "plan tags", envelope.Command)
			assert.False(t, envelope.OK)

			raw, marshalErr := json.Marshal(envelope.Result)
			require.NoError(t, marshalErr)
			var result cli.ErrorResult
			require.NoError(t, json.Unmarshal(raw, &result))
			assert.Contains(t, result.Error, tt.want)
		})
	}
}

func TestPlanTagsDerivedDefaults(t *testing.T) {
	t.Parallel()

	var got []puboci.Reference
	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		Run(func(_ context.Context, ref puboci.Reference) {
			got = append(got, ref)
		}).
		Return(rel.Digest(""), puboci.ErrTagAbsent).
		Times(4)

	_, _, err := executeTags(t, map[string]string{
		"GITHUB_REPOSITORY": "Owner/Repo",
		"GITHUB_REF_NAME":   "v1.2.3",
	}, []string{"plan", "tags", "--digest", tagsDigest}, reader)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, tagsImage, got[0].Image.String())
	assert.Equal(t, []string{"1.2.3", "1.2", "1", "latest"}, referenceTags(got))
}

func TestPlanTagsFlagOverridesEnv(t *testing.T) {
	t.Parallel()

	var got []puboci.Reference
	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		Run(func(_ context.Context, ref puboci.Reference) {
			got = append(got, ref)
		}).
		Return(rel.Digest(""), puboci.ErrTagAbsent).
		Times(4)

	_, _, err := executeTags(t, map[string]string{
		"GITHUB_REPOSITORY": "Owner/Repo",
		"GITHUB_REF_NAME":   "v9.9.9",
		"RELEASE_IMAGE":     "ghcr.io/env/image",
		"RELEASE_VERSION":   "9.9.9",
		"RELEASE_DIGEST":    tagsOther,
	}, []string{
		"plan", "tags",
		"--image", "ghcr.io/flag/image",
		"--version", "1.2.3",
		"--digest", tagsDigest,
	}, reader)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, "ghcr.io/flag/image", got[0].Image.String())
	assert.Equal(t, []string{"1.2.3", "1.2", "1", "latest"}, referenceTags(got))
}

func TestPlanTagsSilentSuccessWithoutJSON(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeTags(t, nil, []string{
		"plan", "tags",
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", tagsDigest,
	}, absentReader(t))
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestPlanTagsJSONSuccess(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeTags(t, nil, []string{
		"plan", "tags",
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", tagsDigest,
		"--json",
	}, absentReader(t))
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.NotContains(t, stdout, tagsToken)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, "plan tags", envelope.Command)
	assert.True(t, envelope.OK)

	result := decodePlanTagsResult(t, envelope)
	assert.Equal(t, tagsImage, result.Image)
	assert.Equal(t, "1.2.3", result.Version)
	assert.Equal(t, tagsDigest, result.Digest)
	assert.Equal(t, []string{"1.2.3", "1.2", "1", "latest"}, result.Tags)
	assert.Equal(t, []cli.TagDecisionResult{
		{Tag: "1.2.3", Scope: "exact", Action: "create"},
		{Tag: "1.2", Scope: "minor", Action: "create"},
		{Tag: "1", Scope: "major", Action: "create"},
		{Tag: "latest", Scope: "latest", Action: "create"},
	}, result.Decisions)
}

func TestPlanTagsJSONDocumentedPayload(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeTags(t, nil, []string{
		"plan", "tags",
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", tagsDigest,
		"--json",
	}, mixedReader(t))
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, "plan tags", envelope.Command)
	assert.True(t, envelope.OK)

	result := decodePlanTagsResult(t, envelope)
	assert.Equal(t, cli.PlanTagsResult{
		Image:   tagsImage,
		Version: "1.2.3",
		Digest:  tagsDigest,
		Tags:    []string{"1.2.3", "1.2"},
		Decisions: []cli.TagDecisionResult{
			{Tag: "1.2.3", Scope: "exact", Action: "create"},
			{Tag: "1.2", Scope: "minor", Action: "create"},
			{Tag: "1", Scope: "major", Action: "retain"},
			{Tag: "latest", Scope: "latest", Action: "accept"},
		},
	}, result)
}

func TestPlanTagsJSONEmptyTags(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeTags(t, nil, []string{
		"plan", "tags",
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", tagsDigest,
		"--json",
	}, matchingReader(t, tagsDigest))
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.Contains(t, stdout, `"tags":[]`)
	assert.NotContains(t, stdout, `"tags":null`)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, "plan tags", envelope.Command)
	assert.True(t, envelope.OK)

	result := decodePlanTagsResult(t, envelope)
	require.NotNil(t, result.Tags)
	assert.Empty(t, result.Tags)
	assert.Equal(t, []cli.TagDecisionResult{
		{Tag: "1.2.3", Scope: "exact", Action: "accept"},
		{Tag: "1.2", Scope: "minor", Action: "accept"},
		{Tag: "1", Scope: "major", Action: "accept"},
		{Tag: "latest", Scope: "latest", Action: "accept"},
	}, result.Decisions)
}

func TestPlanTagsImmutableConflictIsExitOne(t *testing.T) {
	t.Parallel()

	stdout, _, err := executeTags(t, map[string]string{
		"GITHUB_TOKEN": tagsToken,
	}, []string{
		"plan", "tags",
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", tagsDigest,
		"--json",
	}, conflictReader(t, tagsOther))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "immutable")
	assert.NotContains(t, err.Error(), tagsToken)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.Contains(t, stdout, `"command":"plan tags"`)
	assert.Contains(t, stdout, `"ok":false`)
	assert.Contains(t, stdout, "immutable")
	assert.NotContains(t, stdout, tagsToken)
}

func TestPlanTagsCredentialResolution(t *testing.T) {
	t.Parallel()

	t.Run("github token wins and actor is username", func(t *testing.T) {
		t.Parallel()

		var got cli.RegistryCredentials
		stdout, err := executeTagsFactory(t, map[string]string{
			"GITHUB_TOKEN": tagsToken,
			"GH_TOKEN":     "ghs_fallback_must_not_win",
			"GITHUB_ACTOR": "octocat",
		}, []string{
			"plan", "tags",
			"--image", tagsImage,
			"--version", "1.2.3",
			"--digest", tagsDigest,
			"--json",
		}, func(credentials cli.RegistryCredentials) (puboci.StateReader, error) {
			got = credentials
			return absentReader(t), nil
		})
		require.NoError(t, err)
		assert.Equal(t, "octocat", got.Username)
		assert.Equal(t, tagsToken, got.Password.Reveal())
		assert.NotContains(t, stdout, tagsToken)
		assert.NotContains(t, stdout, "ghs_fallback_must_not_win")
	})

	t.Run("absent token is anonymous", func(t *testing.T) {
		t.Parallel()

		var got cli.RegistryCredentials
		called := false
		_, err := executeTagsFactory(t, nil, []string{
			"plan", "tags",
			"--image", tagsImage,
			"--version", "1.2.3",
			"--digest", tagsDigest,
		}, func(credentials cli.RegistryCredentials) (puboci.StateReader, error) {
			called = true
			got = credentials
			return absentReader(t), nil
		})
		require.NoError(t, err)
		require.True(t, called)
		assert.Equal(t, cli.RegistryCredentials{}, got)
		assert.True(t, got.Password.IsEmpty())
	})
}

// unusedReader returns a generated mock that fails if the port is called.
func unusedReader(t *testing.T) *regmocks.MockStateReader {
	t.Helper()

	return regmocks.NewMockStateReader(t)
}

// absentReader returns ErrTagAbsent for every Resolve call.
func absentReader(t *testing.T) *regmocks.MockStateReader {
	t.Helper()

	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		Return(rel.Digest(""), puboci.ErrTagAbsent).
		Times(4)

	return reader
}

// matchingReader resolves every tag to digest and never reads Version.
func matchingReader(t *testing.T, digest string) *regmocks.MockStateReader {
	t.Helper()

	parsed, err := rel.ParseDigest(digest)
	require.NoError(t, err)

	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		Return(parsed, nil).
		Times(4)

	return reader
}

// conflictReader points the exact tag at other and leaves channels absent.
func conflictReader(t *testing.T, other string) *regmocks.MockStateReader {
	t.Helper()

	parsed, err := rel.ParseDigest(other)
	require.NoError(t, err)

	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ref puboci.Reference) (rel.Digest, error) {
			if ref.Tag.String() == "1.2.3" {
				return parsed, nil
			}

			return "", puboci.ErrTagAbsent
		}).
		Times(4)

	return reader
}

// mixedReader produces the documented create/create/retain/accept plan.
func mixedReader(t *testing.T) *regmocks.MockStateReader {
	t.Helper()

	candidate, err := rel.ParseDigest(tagsDigest)
	require.NoError(t, err)
	other, err := rel.ParseDigest(tagsOther)
	require.NoError(t, err)
	newer, err := rel.ParseVersion("1.9.0")
	require.NoError(t, err)

	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ref puboci.Reference) (rel.Digest, error) {
			switch ref.Tag.String() {
			case "1":
				return other, nil
			case "latest":
				return candidate, nil
			default:
				return "", puboci.ErrTagAbsent
			}
		}).
		Times(4)
	reader.EXPECT().
		Version(mock.Anything, mock.MatchedBy(func(ref puboci.Reference) bool {
			return ref.Tag.String() == "1"
		})).
		Return(newer, nil).
		Once()

	return reader
}

// executeTags runs plan tags with an injected state reader.
func executeTags(
	t *testing.T,
	env map[string]string,
	args []string,
	reader puboci.StateReader,
) (string, string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		StateReader: reader,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// executeTagsFactory runs plan tags with a credential-observing factory and
// returns stdout.
func executeTagsFactory(
	t *testing.T,
	env map[string]string,
	args []string,
	factory func(cli.RegistryCredentials) (puboci.StateReader, error),
) (string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: &strings.Builder{},
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		NewStateReader: factory,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), err
}

// decodePlanTagsResult unmarshals the envelope result as [cli.PlanTagsResult].
func decodePlanTagsResult(t *testing.T, envelope cli.Envelope) cli.PlanTagsResult {
	t.Helper()

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.PlanTagsResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// referenceTags returns the tags observed on successive Resolve calls.
func referenceTags(refs []puboci.Reference) []string {
	tags := make([]string, 0, len(refs))
	for _, ref := range refs {
		tags = append(tags, ref.Tag.String())
	}

	return tags
}
