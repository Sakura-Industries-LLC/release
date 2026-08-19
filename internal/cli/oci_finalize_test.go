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
	// finalizeCommand is the envelope command path for publish oci finalize.
	finalizeCommand = "publish oci finalize"
	// finalizeLoopbackImage is a loopback repository used to allow --plain-http.
	finalizeLoopbackImage = "127.0.0.1:5000/owner/repo"
	// finalizeJSONLimitMiB matches the cli prepare-envelope JSON bound.
	finalizeJSONLimitMiB = 4
	// finalizeBytesPerKiB is the number of bytes in a kibibyte.
	finalizeBytesPerKiB = 1024
	// finalizeKibibytesPerMiB is the number of kibibytes in a mebibyte.
	finalizeKibibytesPerMiB = 1024
	// finalizeJSONLimitBytes is the documented 4 MiB prepare-envelope bound.
	finalizeJSONLimitBytes int64 = finalizeJSONLimitMiB * finalizeBytesPerKiB * finalizeKibibytesPerMiB
)

func TestPublishOCIFinalizeJSONSuccess(t *testing.T) {
	t.Parallel()

	image := mustImage(t)
	ports := mixedFinalizePorts(t, image)
	stdin := encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, true))

	stdout, stderr, err := executeFinalize(t, map[string]string{
		"GITHUB_TOKEN": tagsToken,
	}, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
		"--json",
	}, ports)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.NotContains(t, stdout, tagsToken)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, finalizeCommand, envelope.Command)
	assert.True(t, envelope.OK)
	assert.Equal(t, puboci.FinalizeResult{
		Schema:      puboci.FinalizeSchema,
		Image:       tagsImage,
		Version:     "1.2.3",
		IndexDigest: tagsDigest,
		Applied:     []string{"1.2.3", "1.2"},
		Accepted:    []string{"latest"},
		Retained:    []string{"1"},
	}, decodeFinalizeResult(t, stdout))
}

func TestPublishOCIFinalizeSilentSuccess(t *testing.T) {
	t.Parallel()

	image := mustImage(t)
	ports := mixedFinalizePorts(t, image)
	stdin := encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, true))

	stdout, stderr, err := executeFinalize(t, nil, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
	}, ports)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestPublishOCIFinalizeUsageErrors(t *testing.T) {
	t.Parallel()

	valid := encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, true))

	tests := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{
			name: "missing result",
			args: []string{"publish", "oci", "finalize", "--json"},
			want: "--result is required",
		},
		{
			name: "receipt file",
			args: []string{"publish", "oci", "finalize", "--result", "out.json", "--json"},
			want: "there is no receipt file",
		},
		{
			name:  "empty stdin",
			args:  []string{"publish", "oci", "finalize", "--result", "-", "--json"},
			stdin: "",
			want:  "stdin is empty",
		},
		{
			name:  "wrong schema",
			args:  []string{"publish", "oci", "finalize", "--result", "-", "--json"},
			stdin: `{"schema":"release.dev/oci-prepare/v1","command":"publish oci prepare","ok":true,"result":{}}`,
			want:  "schema",
		},
		{
			name:  "wrong command",
			args:  []string{"publish", "oci", "finalize", "--result", "-", "--json"},
			stdin: encodePrepareEnvelope(t, "plan tags", true, mixedPrepareResult(tagsImage, true)),
			want:  prepareCommand,
		},
		{
			name:  "ok false",
			args:  []string{"publish", "oci", "finalize", "--result", "-", "--json"},
			stdin: encodePrepareEnvelope(t, prepareCommand, false, cli.ErrorResult{Error: "prepare failed"}),
			want:  "not successful",
		},
		{
			name:  "malformed inner result",
			args:  []string{"publish", "oci", "finalize", "--result", "-", "--json"},
			stdin: `{"schema":"release.dev/result/v1","command":"publish oci prepare","ok":true,"result":{"schema":"release.dev/oci-prepare/v1"}}`,
			want:  "prepare result",
		},
		{
			name:  "trailing garbage",
			args:  []string{"publish", "oci", "finalize", "--result", "-", "--json"},
			stdin: valid + "\n{}",
			want:  "trailing content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executeFinalizeFactory(t, nil, tt.stdin, tt.args, trackingFinalizeFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.False(t, called)
			assert.Contains(t, err.Error(), tt.want)
			assertFinalizeFailureEnvelope(t, stdout, tt.want)
		})
	}
}

func TestPublishOCIFinalizeEnvelopeJustUnderJSONLimit(t *testing.T) {
	t.Parallel()

	image := mustImage(t)
	ports := mixedFinalizePorts(t, image)
	stdin := padJSONObject(
		encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, true)),
		int(finalizeJSONLimitBytes-1),
	)

	stdout, stderr, err := executeFinalize(t, nil, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
		"--json",
	}, ports)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.Equal(t, []string{"1.2.3", "1.2"}, decodeFinalizeResult(t, stdout).Applied)
}

func TestPublishOCIFinalizeEnvelopeOverJSONLimit(t *testing.T) {
	t.Parallel()

	called := false
	stdin := padJSONObject(
		encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, true)),
		int(finalizeJSONLimitBytes+1),
	)
	stdout, err := executeFinalizeFactory(t, nil, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
		"--json",
	}, trackingFinalizeFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.False(t, called)
	assert.Contains(t, err.Error(), "4 MiB")
	assertFinalizeFailureEnvelope(t, stdout, "4 MiB")
}

func TestPublishOCIFinalizeNotAuthoritative(t *testing.T) {
	t.Parallel()

	stdin := encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, false))
	stdout, _, err := executeFinalize(t, nil, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
		"--json",
	}, finalizePorts{
		reader:    unusedReader(t),
		committer: unusedCommitter(t),
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	require.ErrorIs(t, err, puboci.ErrNotAuthoritative)
	assert.Contains(t, err.Error(), puboci.ErrNotAuthoritative.Error())
	assertFinalizeFailureEnvelope(t, stdout, puboci.ErrNotAuthoritative.Error())
}

func TestPublishOCIFinalizeDrift(t *testing.T) {
	t.Parallel()

	stdin := encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, true))
	stdout, _, err := executeFinalize(t, map[string]string{
		"GITHUB_TOKEN": tagsToken,
	}, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
		"--json",
	}, finalizePorts{
		reader:    driftedExactReader(t),
		committer: unusedCommitter(t),
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	require.ErrorIs(t, err, puboci.ErrStateDrift)
	assert.Contains(t, err.Error(), "1.2.3")
	assert.NotContains(t, err.Error(), tagsToken)
	assert.NotContains(t, stdout, tagsToken)
	assertFinalizeFailureEnvelope(t, stdout, "1.2.3")
}

func TestPublishOCIFinalizeRegistryConfig(t *testing.T) {
	t.Parallel()

	image, err := puboci.ParseImage(finalizeLoopbackImage)
	require.NoError(t, err)
	ports := mixedFinalizePorts(t, image)
	stdin := encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(finalizeLoopbackImage, true))

	var gotReader cli.RegistryConfig
	var gotCommitter cli.RegistryConfig
	stdout, execErr := executeFinalizeFactory(t, map[string]string{
		"GITHUB_TOKEN": tagsToken,
		"GITHUB_ACTOR": "octocat",
	}, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
		"--plain-http",
		"--json",
	}, finalizeFactories{
		newReader: func(config cli.RegistryConfig) (puboci.StateReader, error) {
			gotReader = config
			return ports.reader, nil
		},
		newCommitter: func(config cli.RegistryConfig) (puboci.TagCommitter, error) {
			gotCommitter = config
			return ports.committer, nil
		},
	})
	require.NoError(t, execErr)
	assert.Equal(t, gotReader, gotCommitter)
	assert.Equal(t, "octocat", gotReader.Credentials.Username)
	assert.Equal(t, tagsToken, gotReader.Credentials.Password.Reveal())
	assert.True(t, gotReader.PlainHTTP)
	assert.NotContains(t, stdout, tagsToken)
	assert.Equal(t, []string{"1.2.3", "1.2"}, decodeFinalizeResult(t, stdout).Applied)
}

func TestPublishOCIFinalizePlainHTTPRefusedForGHCR(t *testing.T) {
	t.Parallel()

	called := false
	stdin := encodePrepareEnvelope(t, prepareCommand, true, mixedPrepareResult(tagsImage, true))
	stdout, err := executeFinalizeFactory(t, nil, stdin, []string{
		"publish", "oci", "finalize",
		"--result", "-",
		"--plain-http",
		"--json",
	}, trackingFinalizeFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.False(t, called)
	assert.Contains(t, err.Error(), "--plain-http")
	assertFinalizeFailureEnvelope(t, stdout, "--plain-http")
}

// finalizePorts is the injected finalize command ports.
type finalizePorts struct {
	// reader is the registry read port.
	reader puboci.StateReader
	// committer is the registry tag-write port.
	committer puboci.TagCommitter
}

// finalizeFactories constructs finalize ports from resolved configuration.
type finalizeFactories struct {
	// newReader constructs the registry read port.
	newReader func(cli.RegistryConfig) (puboci.StateReader, error)
	// newCommitter constructs the registry tag-write port.
	newCommitter func(cli.RegistryConfig) (puboci.TagCommitter, error)
}

// trackingFinalizeFactories records whether any finalize factory was invoked.
func trackingFinalizeFactories(t *testing.T, called *bool) finalizeFactories {
	t.Helper()

	return finalizeFactories{
		newReader: func(cli.RegistryConfig) (puboci.StateReader, error) {
			*called = true
			return unusedReader(t), nil
		},
		newCommitter: func(cli.RegistryConfig) (puboci.TagCommitter, error) {
			*called = true
			return unusedCommitter(t), nil
		},
	}
}

// unusedCommitter returns a generated mock that fails if the port is called.
func unusedCommitter(t *testing.T) *regmocks.MockTagCommitter {
	t.Helper()

	return regmocks.NewMockTagCommitter(t)
}

// mixedPrepareResult is an authoritative-or-dry-run prepare document for 1.2.3.
func mixedPrepareResult(image string, authoritative bool) puboci.OCIPrepareResult {
	return puboci.OCIPrepareResult{
		Schema:        puboci.PrepareSchema,
		Authoritative: authoritative,
		Image:         image,
		Version:       "1.2.3",
		IndexDigest:   tagsDigest,
		Platforms: []puboci.AttestationSubject{
			{Platform: "linux/amd64", Digest: tagsOther},
		},
		Observed: []puboci.TagObservation{
			{Tag: "1.2.3", Scope: string(rel.ScopeExact), Present: false},
			{Tag: "1.2", Scope: string(rel.ScopeMinor), Present: false},
			{
				Tag:     "1",
				Scope:   string(rel.ScopeMajor),
				Present: true,
				Digest:  tagsOther,
				Version: "1.9.0",
			},
			{Tag: "latest", Scope: string(rel.ScopeLatest), Present: true, Digest: tagsDigest},
		},
	}
}

// mixedFinalizePorts plans create/create/retain/accept and commits the create tags.
func mixedFinalizePorts(t *testing.T, image puboci.Image) finalizePorts {
	t.Helper()

	candidate, err := rel.ParseDigest(tagsDigest)
	require.NoError(t, err)
	other, err := rel.ParseDigest(tagsOther)
	require.NoError(t, err)
	newer, err := rel.ParseVersion("1.9.0")
	require.NoError(t, err)
	exact, err := rel.ParseTag("1.2.3")
	require.NoError(t, err)
	minor, err := rel.ParseTag("1.2")
	require.NoError(t, err)

	committed := false
	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ref puboci.Reference) (rel.Digest, error) {
			switch ref.Tag.String() {
			case "1":
				return other, nil
			case "latest":
				return candidate, nil
			case "1.2.3", "1.2":
				if committed {
					return candidate, nil
				}

				return "", puboci.ErrTagAbsent
			default:
				return "", puboci.ErrTagAbsent
			}
		})
	reader.EXPECT().
		Version(mock.Anything, mock.MatchedBy(func(ref puboci.Reference) bool {
			return ref.Tag.String() == "1"
		})).
		Return(newer, nil).
		Once()

	committer := regmocks.NewMockTagCommitter(t)
	committer.EXPECT().
		Commit(mock.Anything, image, candidate, []rel.Tag{exact, minor}).
		Run(func(context.Context, puboci.Image, rel.Digest, []rel.Tag) {
			committed = true
		}).
		Return(nil).
		Once()

	return finalizePorts{reader: reader, committer: committer}
}

// driftedExactReader points the exact tag at tagsOther after an absent observation.
func driftedExactReader(t *testing.T) *regmocks.MockStateReader {
	t.Helper()

	other, err := rel.ParseDigest(tagsOther)
	require.NoError(t, err)
	reader := regmocks.NewMockStateReader(t)
	reader.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ref puboci.Reference) (rel.Digest, error) {
			if ref.Tag.String() == "1.2.3" {
				return other, nil
			}

			return "", puboci.ErrTagAbsent
		}).
		Times(4)

	return reader
}

// executeFinalize runs publish oci finalize with injected ports.
func executeFinalize(
	t *testing.T,
	env map[string]string,
	stdin string,
	args []string,
	ports finalizePorts,
) (string, string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		In:  strings.NewReader(stdin),
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		StateReader:  ports.reader,
		TagCommitter: ports.committer,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// executeFinalizeFactory runs publish oci finalize with observing factories.
func executeFinalizeFactory(
	t *testing.T,
	env map[string]string,
	stdin string,
	args []string,
	factories finalizeFactories,
) (string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		In:  strings.NewReader(stdin),
		Out: stdout,
		Err: &strings.Builder{},
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		NewStateReader:  factories.newReader,
		NewTagCommitter: factories.newCommitter,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), err
}

// encodePrepareEnvelope marshals one prepare-result envelope for stdin.
func encodePrepareEnvelope(t *testing.T, command string, ok bool, result any) string {
	t.Helper()

	payload, err := json.Marshal(cli.Envelope{
		Schema:  cli.Schema,
		Command: command,
		OK:      ok,
		Result:  result,
	})
	require.NoError(t, err)

	return string(payload) + "\n"
}

// padJSONObject inserts spaces after the opening brace so document is size bytes.
func padJSONObject(document string, size int) string {
	if len(document) >= size {
		return document
	}

	return "{" + strings.Repeat(" ", size-len(document)) + document[1:]
}

// decodeFinalizeResult unmarshals the envelope result as [puboci.FinalizeResult].
func decodeFinalizeResult(t *testing.T, stdout string) puboci.FinalizeResult {
	t.Helper()

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result puboci.FinalizeResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// assertFinalizeFailureEnvelope checks stdout is one ok:false finalize envelope.
func assertFinalizeFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, finalizeCommand, envelope.Command)
	assert.False(t, envelope.OK)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
	assert.NotContains(t, stdout, tagsToken)
}
