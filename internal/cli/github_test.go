package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	ghrelmocks "github.com/meigma/release/internal/adapter/ghrel/mocks"
	ghupmocks "github.com/meigma/release/internal/adapter/ghup/mocks"
	gitxmocks "github.com/meigma/release/internal/adapter/gitx/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// githubCommand is the envelope command path for publish github.
	githubCommand = "publish github"
	// githubToken is a credential that must never appear in output.
	githubToken = "ghs_github_should_never_appear"
	// githubSHA is a valid 40-character lowercase commit SHA.
	githubSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// githubTagName is the GITHUB_REF_NAME fixture.
	githubTagName = "v1.2.3"
	// githubRepo is the GITHUB_REPOSITORY fixture.
	githubRepo = "meigma/release"
	// githubReleaseURL is the published release HTML URL fixture.
	githubReleaseURL = "https://github.com/meigma/release/releases/tag/v1.2.3"
	// githubReleaseID is the discovered draft identifier.
	githubReleaseID int64 = 42
	// githubGHPath is the RELEASE_GH_PATH fixture.
	githubGHPath = "/opt/gh"
	// githubGitPath is the RELEASE_GIT_PATH fixture.
	githubGitPath = "/opt/git"
	// githubUploaded is the GitHub-reported ready asset state.
	githubUploaded = "uploaded"
)

func TestPublishGitHubConfigErrorsAreUsage(t *testing.T) {
	t.Parallel()

	dist := t.TempDir()
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{
			name: "missing dist",
			env:  githubEnv(),
			args: []string{"publish", "github", "--json"},
			want: "--dist is required",
		},
		{
			name: "missing token",
			env:  omitEnv(githubEnv(), "RELEASE_APP_TOKEN"),
			args: []string{"publish", "github", "--dist", dist, "--json"},
			want: "RELEASE_APP_TOKEN is required",
		},
		{
			name: "missing repository",
			env:  omitEnv(githubEnv(), "GITHUB_REPOSITORY"),
			args: []string{"publish", "github", "--dist", dist, "--json"},
			want: "GITHUB_REPOSITORY is required",
		},
		{
			name: "missing ref name",
			env:  omitEnv(githubEnv(), "GITHUB_REF_NAME"),
			args: []string{"publish", "github", "--dist", dist, "--json"},
			want: "GITHUB_REF_NAME is required",
		},
		{
			name: "missing sha",
			env:  omitEnv(githubEnv(), "GITHUB_SHA"),
			args: []string{"publish", "github", "--dist", dist, "--json"},
			want: "GITHUB_SHA is required",
		},
		{
			name: "malformed sha",
			env:  withEnv(githubEnv(), "GITHUB_SHA", "not-a-sha"),
			args: []string{"publish", "github", "--dist", dist, "--json"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, stderr, err := executeGitHubFactory(t, tt.env, tt.args, trackingGitHubFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.False(t, called)
			assertGitHubFailureEnvelope(t, stdout, tt.want)
			assert.NotContains(t, stdout, githubToken)
			assert.NotContains(t, stderr, githubToken)
			assert.NotContains(t, err.Error(), githubToken)
		})
	}
}

func TestPublishGitHubClosedSetFailureNeverCallsRelease(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	writeFile(t, filepath.Join(fixture.dir, "extra.bin"), "unexpected")

	stdout, stderr, err := executeGitHub(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fixture.dir,
		"--json",
	}, unusedGitHubPorts(t))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "extra.bin")
	assertGitHubFailureEnvelope(t, stdout, "extra.bin")
	assert.NotContains(t, stdout, githubToken)
	assert.NotContains(t, stderr, githubToken)
}

func TestPublishGitHubJSONSuccess(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	var gotAssets []pubgh.AssetPath
	ports := successfulGitHubPorts(t, fixture, true, &gotAssets)

	stdout, stderr, err := executeGitHub(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fixture.dir,
		"--json",
	}, ports)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.NotContains(t, stdout, githubToken)
	assert.NotContains(t, stderr, githubToken)

	result := decodeGitHubResult(t, stdout)
	assert.Equal(t, githubReleaseID, result.ReleaseID)
	assert.Equal(t, githubTagName, result.Tag)
	assert.Equal(t, githubReleaseURL, result.URL)
	assert.False(t, result.Draft)
	assert.Equal(t, []string{
		"checksums.txt",
		"checksums.txt.sigstore.json",
		bundleFirstName,
		bundleSecondName,
	}, result.Assets)
	assert.Equal(t, expectedGitHubAssetPaths(t, fixture), gotAssets)
}

func TestPublishGitHubNoUndraftReachesEngine(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	ports := successfulGitHubPorts(t, fixture, false, nil)

	stdout, stderr, err := executeGitHub(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fixture.dir,
		"--no-undraft",
		"--json",
	}, ports)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, githubToken)

	result := decodeGitHubResult(t, stdout)
	assert.True(t, result.Draft)
	assert.Equal(t, githubReleaseURL, result.URL)
}

func TestPublishGitHubSilentSuccessWithoutJSON(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	ports := successfulGitHubPorts(t, fixture, true, nil)

	stdout, stderr, err := executeGitHub(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fixture.dir,
	}, ports)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestPublishGitHubEngineFailure(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	ports := unusedGitHubPorts(t)
	resolver := gitxmocks.NewMockRefResolver(t)
	resolver.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		Return(pubgh.CommitSHA(""), errors.New("tag does not resolve")).
		Once()
	ports.resolver = resolver

	stdout, stderr, err := executeGitHub(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fixture.dir,
		"--json",
	}, ports)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "tag does not resolve")
	assertGitHubFailureEnvelope(t, stdout, "tag does not resolve")
	assert.NotContains(t, stdout, githubToken)
	assert.NotContains(t, stderr, githubToken)
}

func TestPublishGitHubIndeterminateWritesHint(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	ports := unusedGitHubPorts(t)
	resolver := matchingResolver(t)
	reader := ghrelmocks.NewMockReleaseReader(t)
	releaseID := mustReleaseID(t)
	tag := mustGitHubTag(t)
	reader.EXPECT().
		FindDraft(mock.Anything, mock.Anything, tag).
		Return(pubgh.Release{
			ID:    releaseID,
			Tag:   tag,
			Draft: false,
			URL:   githubReleaseURL,
		}, nil).
		Once()
	reader.EXPECT().
		WaitAssets(mock.Anything, mock.Anything, releaseID).
		Return(pubgh.AssetsView{}, nil).
		Once()
	ports.resolver = resolver
	ports.reader = reader

	stdout, stderr, err := executeGitHub(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fixture.dir,
		"--json",
	}, ports)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	require.ErrorIs(t, err, pubgh.ErrIndeterminate)
	assert.Contains(t, stderr, "inspect the release in GitHub and reconcile manually")
	assert.Contains(t, stderr, "do not rerun the publication blindly")
	assert.NotContains(t, stdout, "inspect the release")
	assertGitHubFailureEnvelope(t, stdout, pubgh.ErrIndeterminate.Error())
	assert.NotContains(t, stdout, githubToken)
	assert.NotContains(t, stderr, githubToken)
}

func TestPublishGitHubOrdinaryFailureOmitsHint(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	ports := unusedGitHubPorts(t)
	resolver := gitxmocks.NewMockRefResolver(t)
	resolver.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		Return(pubgh.CommitSHA(""), errors.New("tag does not resolve")).
		Once()
	ports.resolver = resolver

	stdout, stderr, err := executeGitHub(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fixture.dir,
		"--json",
	}, ports)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.NotContains(t, stderr, "inspect the release")
	assert.NotContains(t, stderr, "do not rerun")
	assertGitHubFailureEnvelope(t, stdout, "tag does not resolve")
}

func TestPublishGitHubTokenReachesFactoriesAsSecret(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	var readerToken rel.Secret
	var replacerToken rel.Secret
	var publisherToken rel.Secret
	var gotGHPath string
	var gotGitPath string
	var gotDir string
	var gotResolverDir string

	env := githubEnv()
	env["RELEASE_GH_PATH"] = githubGHPath
	env["RELEASE_GIT_PATH"] = githubGitPath

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		NewReleaseReader: func(token rel.Secret, _ cli.GitHubEndpoint) (pubgh.ReleaseReader, error) {
			readerToken = token
			return successfulReleaseReader(t, fixture, true), nil
		},
		NewAssetReplacer: func(token rel.Secret, path, dir string) (pubgh.AssetReplacer, error) {
			replacerToken = token
			gotGHPath = path
			gotDir = dir
			return acceptingReplacer(t, nil), nil
		},
		NewPublisher: func(token rel.Secret, _ cli.GitHubEndpoint) (pubgh.Publisher, error) {
			publisherToken = token
			return acceptingPublisher(t), nil
		},
		NewRefResolver: func(path, dir string) (pubgh.RefResolver, error) {
			gotGitPath = path
			gotResolverDir = dir
			return matchingResolver(t), nil
		},
	})
	command.SetArgs([]string{"publish", "github", "--dist", fixture.dir, "--json"})
	err := command.Execute()
	require.NoError(t, err)

	assert.Equal(t, githubToken, readerToken.Reveal())
	assert.Equal(t, githubToken, replacerToken.Reveal())
	assert.Equal(t, githubToken, publisherToken.Reveal())
	assert.Equal(t, "[REDACTED]", readerToken.String())
	assert.Equal(t, githubGHPath, gotGHPath)
	assert.Equal(t, githubGitPath, gotGitPath)
	assert.Equal(t, absPath(t, fixture.dir), gotDir)
	assert.Empty(t, gotResolverDir)
	assert.NotContains(t, stdout.String(), githubToken)
	assert.NotContains(t, stderr.String(), githubToken)
	assert.NotContains(t, errString(err), githubToken)
}

func TestPublishGitHubInvalidDistPath(t *testing.T) {
	t.Parallel()

	fileDist := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(fileDist, []byte("file"), 0o644))

	called := false
	stdout, _, err := executeGitHubFactory(t, githubEnv(), []string{
		"publish", "github",
		"--dist", fileDist,
		"--json",
	}, trackingGitHubFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "open dist")
	assert.False(t, called)
	assertGitHubFailureEnvelope(t, stdout, "open dist")
}

// githubPorts is the injected publish-github command ports.
type githubPorts struct {
	// reader is the GitHub release read port.
	reader pubgh.ReleaseReader
	// replacer is the GitHub release upload port.
	replacer pubgh.AssetReplacer
	// publisher is the GitHub release undraft port.
	publisher pubgh.Publisher
	// resolver is the local tag-to-SHA port.
	resolver pubgh.RefResolver
}

// githubFactories constructs publish-github ports from resolved configuration.
type githubFactories struct {
	// newReader constructs the GitHub release read port.
	newReader func(rel.Secret, cli.GitHubEndpoint) (pubgh.ReleaseReader, error)
	// newReplacer constructs the GitHub release upload port.
	newReplacer func(rel.Secret, string, string) (pubgh.AssetReplacer, error)
	// newPublisher constructs the GitHub release undraft port.
	newPublisher func(rel.Secret, cli.GitHubEndpoint) (pubgh.Publisher, error)
	// newResolver constructs the local tag-to-SHA port.
	newResolver func(string, string) (pubgh.RefResolver, error)
}

// unusedGitHubPorts returns generated mocks that fail if a port is called.
func unusedGitHubPorts(t *testing.T) githubPorts {
	t.Helper()

	return githubPorts{
		reader:    ghrelmocks.NewMockReleaseReader(t),
		replacer:  ghupmocks.NewMockAssetReplacer(t),
		publisher: ghrelmocks.NewMockPublisher(t),
		resolver:  gitxmocks.NewMockRefResolver(t),
	}
}

// trackingGitHubFactories records whether any publish-github factory was invoked.
func trackingGitHubFactories(t *testing.T, called *bool) githubFactories {
	t.Helper()

	return githubFactories{
		newReader: func(rel.Secret, cli.GitHubEndpoint) (pubgh.ReleaseReader, error) {
			*called = true
			return ghrelmocks.NewMockReleaseReader(t), nil
		},
		newReplacer: func(rel.Secret, string, string) (pubgh.AssetReplacer, error) {
			*called = true
			return ghupmocks.NewMockAssetReplacer(t), nil
		},
		newPublisher: func(rel.Secret, cli.GitHubEndpoint) (pubgh.Publisher, error) {
			*called = true
			return ghrelmocks.NewMockPublisher(t), nil
		},
		newResolver: func(string, string) (pubgh.RefResolver, error) {
			*called = true
			return gitxmocks.NewMockRefResolver(t), nil
		},
	}
}

// successfulGitHubPorts expects the full draft-upload-converge sequence.
func successfulGitHubPorts(
	t *testing.T,
	fixture closedBundle,
	undraft bool,
	gotAssets *[]pubgh.AssetPath,
) githubPorts {
	t.Helper()

	ports := githubPorts{
		reader:   successfulReleaseReader(t, fixture, undraft),
		replacer: acceptingReplacer(t, gotAssets),
		resolver: matchingResolver(t),
	}
	if undraft {
		ports.publisher = acceptingPublisher(t)
	} else {
		ports.publisher = ghrelmocks.NewMockPublisher(t)
	}

	return ports
}

// successfulReleaseReader expects draft discovery, asset waits, and the final get.
func successfulReleaseReader(t *testing.T, fixture closedBundle, undraft bool) *ghrelmocks.MockReleaseReader {
	t.Helper()

	releaseID := mustReleaseID(t)
	tag := mustGitHubTag(t)
	draft := pubgh.Release{
		ID:    releaseID,
		Tag:   tag,
		Draft: true,
		URL:   githubReleaseURL,
	}
	final := draft
	final.Draft = !undraft
	uploaded := uploadedGitHubAssets(fixture)

	reader := ghrelmocks.NewMockReleaseReader(t)
	reader.EXPECT().
		FindDraft(mock.Anything, mock.Anything, tag).
		Return(draft, nil).
		Once()
	reader.EXPECT().
		WaitAssets(mock.Anything, mock.Anything, releaseID).
		Return(pubgh.AssetsView{}, nil).
		Once()
	reader.EXPECT().
		WaitAssets(mock.Anything, mock.Anything, releaseID).
		Return(uploaded, nil).
		Once()
	reader.EXPECT().
		Get(mock.Anything, mock.Anything, releaseID).
		Return(final, nil).
		Once()

	return reader
}

// acceptingReplacer records the uploaded paths and accepts the replace.
func acceptingReplacer(t *testing.T, got *[]pubgh.AssetPath) *ghupmocks.MockAssetReplacer {
	t.Helper()

	replacer := ghupmocks.NewMockAssetReplacer(t)
	expect := replacer.EXPECT().
		Replace(mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	if got != nil {
		expect.Run(func(_ context.Context, _ pubgh.Repository, _ rel.Tag, expected []pubgh.AssetPath) {
			cloned := make([]pubgh.AssetPath, len(expected))
			copy(cloned, expected)
			*got = cloned
		})
	}
	expect.Return(nil).Once()

	return replacer
}

// acceptingPublisher expects one undraft call.
func acceptingPublisher(t *testing.T) *ghrelmocks.MockPublisher {
	t.Helper()

	publisher := ghrelmocks.NewMockPublisher(t)
	publisher.EXPECT().
		Publish(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).
		Once()

	return publisher
}

// matchingResolver returns the fixture commit SHA for any tag.
func matchingResolver(t *testing.T) *gitxmocks.MockRefResolver {
	t.Helper()

	resolver := gitxmocks.NewMockRefResolver(t)
	resolver.EXPECT().
		Resolve(mock.Anything, mock.Anything).
		Return(mustCommitSHA(t), nil).
		Once()

	return resolver
}

// uploadedGitHubAssets returns the closed bundle as uploaded GitHub assets.
func uploadedGitHubAssets(fixture closedBundle) pubgh.AssetsView {
	return pubgh.AssetsView{
		Assets: []pubgh.Asset{
			{Name: bundleFirstName, Digest: "sha256:" + fixture.firstDigest, State: githubUploaded},
			{Name: bundleSecondName, Digest: "sha256:" + fixture.secondDigest, State: githubUploaded},
			{Name: "checksums.txt", Digest: "sha256:" + fixture.checksumsDigest, State: githubUploaded},
			{Name: "checksums.txt.sigstore.json", Digest: "sha256:" + fixture.bundleDigest, State: githubUploaded},
		},
	}
}

// expectedGitHubAssetPaths returns absolute bundle paths in bundle order.
func expectedGitHubAssetPaths(t *testing.T, fixture closedBundle) []pubgh.AssetPath {
	t.Helper()

	root := absPath(t, fixture.dir)

	return []pubgh.AssetPath{
		pubgh.AssetPath(filepath.Join(root, bundleFirstName)),
		pubgh.AssetPath(filepath.Join(root, bundleSecondName)),
		pubgh.AssetPath(filepath.Join(root, "checksums.txt")),
		pubgh.AssetPath(filepath.Join(root, "checksums.txt.sigstore.json")),
	}
}

// executeGitHub runs publish github with injected ports.
func executeGitHub(
	t *testing.T,
	env map[string]string,
	args []string,
	ports githubPorts,
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
		ReleaseReader: ports.reader,
		AssetReplacer: ports.replacer,
		Publisher:     ports.publisher,
		RefResolver:   ports.resolver,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// executeGitHubFactory runs publish github with observing factories.
func executeGitHubFactory(
	t *testing.T,
	env map[string]string,
	args []string,
	factories githubFactories,
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
		NewReleaseReader: factories.newReader,
		NewAssetReplacer: factories.newReplacer,
		NewPublisher:     factories.newPublisher,
		NewRefResolver:   factories.newResolver,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// decodeGitHubResult unmarshals the envelope result as [pubgh.PublishResult].
func decodeGitHubResult(t *testing.T, stdout string) pubgh.PublishResult {
	t.Helper()

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, githubCommand, envelope.Command)
	assert.True(t, envelope.OK)
	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result pubgh.PublishResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// assertGitHubFailureEnvelope checks stdout is one ok:false publish-github envelope.
func assertGitHubFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, githubCommand, envelope.Command)
	assert.False(t, envelope.OK)
	assert.NotContains(t, stdout, githubToken)

	if wantError == "" {
		return
	}

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
}

// githubEnv returns the required Actions environment for publish github.
func githubEnv() map[string]string {
	return map[string]string{
		"GITHUB_REPOSITORY": githubRepo,
		"GITHUB_REF_NAME":   githubTagName,
		"GITHUB_SHA":        githubSHA,
		"RELEASE_APP_TOKEN": githubToken,
	}
}

// omitEnv returns a copy of env without key.
func omitEnv(env map[string]string, key string) map[string]string {
	copied := make(map[string]string, len(env))
	for name, value := range env {
		if name == key {
			continue
		}
		copied[name] = value
	}

	return copied
}

// withEnv returns a copy of env with key set to value.
func withEnv(env map[string]string, key, value string) map[string]string {
	copied := make(map[string]string, len(env)+1)
	maps.Copy(copied, env)
	copied[key] = value

	return copied
}

// mustReleaseID constructs the fixture [pubgh.ReleaseID].
func mustReleaseID(t *testing.T) pubgh.ReleaseID {
	t.Helper()

	id, err := pubgh.ReleaseIDFromInt(githubReleaseID)
	require.NoError(t, err)

	return id
}

// mustGitHubTag constructs the fixture release tag.
func mustGitHubTag(t *testing.T) rel.Tag {
	t.Helper()

	tag, err := rel.ParseTag(githubTagName)
	require.NoError(t, err)

	return tag
}

// mustCommitSHA constructs the fixture commit SHA.
func mustCommitSHA(t *testing.T) pubgh.CommitSHA {
	t.Helper()

	sha, err := pubgh.ParseCommitSHA(githubSHA)
	require.NoError(t, err)

	return sha
}

// absPath returns the absolute form of path.
func absPath(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(path)
	require.NoError(t, err)

	return abs
}

// errString returns err.Error, or empty when err is nil.
func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
