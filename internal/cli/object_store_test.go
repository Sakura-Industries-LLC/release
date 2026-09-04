package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	r2mocks "github.com/Sakura-Industries-LLC/release/internal/adapter/r2/mocks"
	"github.com/Sakura-Industries-LLC/release/internal/cli"
	"github.com/Sakura-Industries-LLC/release/internal/rel"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pkgrepo"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pubobj"
)

const (
	objectStoreCommand = "publish object-store"
	objectStoreProject = "release"
	objectStoreTag     = "v1.2.3"
	objectStorePrefix  = "release/v1.2.3"
	objectStoreAccess  = "object-store-access-should-never-appear"
	objectStoreSecret  = "object-store-secret-should-never-appear"
	objectStoreURL     = "https://objects.dntls.net"
	objectStoreBucket  = "releases"
	objectStoreRegion  = "garage"
)

func TestPublishObjectStoreConfigErrorsAreUsage(t *testing.T) {
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
			env:  objectStoreEnv(),
			args: objectStoreArgs(""),
			want: "--dist is required",
		},
		{
			name: "missing project",
			env:  omitEnv(objectStoreEnv(), "RELEASE_OBJECT_STORE_PROJECT"),
			args: objectStoreArgs(dist),
			want: "--project or RELEASE_OBJECT_STORE_PROJECT is required",
		},
		{
			name: "missing ref name",
			env:  omitEnv(objectStoreEnv(), "GITHUB_REF_NAME"),
			args: objectStoreArgs(dist),
			want: "GITHUB_REF_NAME is required",
		},
		{
			name: "missing endpoint",
			env:  omitEnv(objectStoreEnv(), "RELEASE_OBJECT_STORE_ENDPOINT"),
			args: objectStoreArgs(dist),
			want: "--endpoint or RELEASE_OBJECT_STORE_ENDPOINT is required",
		},
		{
			name: "missing bucket",
			env:  omitEnv(objectStoreEnv(), "RELEASE_OBJECT_STORE_BUCKET"),
			args: objectStoreArgs(dist),
			want: "--bucket or RELEASE_OBJECT_STORE_BUCKET is required",
		},
		{
			name: "missing region",
			env:  omitEnv(objectStoreEnv(), "RELEASE_OBJECT_STORE_REGION"),
			args: objectStoreArgs(dist),
			want: "--region or RELEASE_OBJECT_STORE_REGION is required",
		},
		{
			name: "missing access key",
			env:  omitEnv(objectStoreEnv(), "OBJECT_STORE_ACCESS_KEY_ID"),
			args: objectStoreArgs(dist),
			want: "OBJECT_STORE_ACCESS_KEY_ID is required",
		},
		{
			name: "missing secret key",
			env:  omitEnv(objectStoreEnv(), "OBJECT_STORE_SECRET_ACCESS_KEY"),
			args: objectStoreArgs(dist),
			want: "OBJECT_STORE_SECRET_ACCESS_KEY is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := executeObjectStore(t, tt.env, tt.args, unusedObjectStore(t))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assertObjectStoreFailureEnvelope(t, stdout, tt.want)
			assert.NotContains(t, stdout, objectStoreAccess)
			assert.NotContains(t, stdout, objectStoreSecret)
			assert.NotContains(t, stderr, objectStoreAccess)
			assert.NotContains(t, stderr, objectStoreSecret)
			assert.NotContains(t, err.Error(), objectStoreAccess)
			assert.NotContains(t, err.Error(), objectStoreSecret)
		})
	}
}

func TestPublishObjectStoreJSONSuccess(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	store := r2mocks.NewMockObjectStore(t)
	store.EXPECT().
		Stat(mock.Anything, mock.Anything).
		Return(pkgrepo.StoredContent{}, false, nil).
		Times(4)
	store.EXPECT().
		Upload(mock.Anything, mock.Anything).
		Return(nil).
		Times(4)

	stdout, stderr, err := executeObjectStore(t, objectStoreEnv(), objectStoreArgs(fixture.dir), store)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.NotContains(t, stdout, objectStoreAccess)
	assert.NotContains(t, stdout, objectStoreSecret)
	assert.NotContains(t, stderr, objectStoreAccess)
	assert.NotContains(t, stderr, objectStoreSecret)

	result := decodeObjectStoreResult(t, stdout)
	assert.Equal(t, pubobj.Project(objectStoreProject), result.Project)
	assert.Equal(t, rel.GitTag(objectStoreTag), result.Tag)
	assert.Equal(t, pubobj.Prefix(objectStorePrefix), result.Prefix)
	assert.Equal(t, []string{
		bundleFirstName,
		bundleSecondName,
		"checksums.txt",
		"checksums.txt.sigstore.json",
	}, result.Uploaded)
	assert.Empty(t, result.Unchanged)
	assert.Equal(t, pubobj.PublishStatePublished, result.State)
}

func TestPublishObjectStoreSilentSuccessWithoutJSON(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	store := r2mocks.NewMockObjectStore(t)
	store.EXPECT().
		Stat(mock.Anything, mock.Anything).
		Return(pkgrepo.StoredContent{}, false, nil).
		Times(4)
	store.EXPECT().
		Upload(mock.Anything, mock.Anything).
		Return(nil).
		Times(4)

	args := []string{
		"publish", "object-store",
		"--dist", fixture.dir,
		"--project", objectStoreProject,
		"--endpoint", objectStoreURL,
		"--bucket", objectStoreBucket,
		"--region", objectStoreRegion,
	}
	stdout, stderr, err := executeObjectStore(t, objectStoreEnv(), args, store)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

// executeObjectStore runs publish object-store with an injected store.
func executeObjectStore(
	t *testing.T,
	env map[string]string,
	args []string,
	store pubobj.ObjectStore,
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
		ObjectStore: store,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// unusedObjectStore returns a generated mock that fails if the store is called.
func unusedObjectStore(t *testing.T) *r2mocks.MockObjectStore {
	t.Helper()

	return r2mocks.NewMockObjectStore(t)
}

// decodeObjectStoreResult unmarshals the envelope result as [pubobj.PublishResult].
func decodeObjectStoreResult(t *testing.T, stdout string) pubobj.PublishResult {
	t.Helper()

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, objectStoreCommand, envelope.Command)
	assert.True(t, envelope.OK)
	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result pubobj.PublishResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// assertObjectStoreFailureEnvelope checks stdout is one ok:false publish-object-store envelope.
func assertObjectStoreFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, objectStoreCommand, envelope.Command)
	assert.False(t, envelope.OK)
	assert.NotContains(t, stdout, objectStoreAccess)
	assert.NotContains(t, stdout, objectStoreSecret)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
}

// objectStoreEnv returns the required environment for publish object-store.
func objectStoreEnv() map[string]string {
	return map[string]string{
		"GITHUB_REF_NAME":                objectStoreTag,
		"RELEASE_OBJECT_STORE_PROJECT":   objectStoreProject,
		"RELEASE_OBJECT_STORE_ENDPOINT":  objectStoreURL,
		"RELEASE_OBJECT_STORE_BUCKET":    objectStoreBucket,
		"RELEASE_OBJECT_STORE_REGION":    objectStoreRegion,
		"OBJECT_STORE_ACCESS_KEY_ID":     objectStoreAccess,
		"OBJECT_STORE_SECRET_ACCESS_KEY": objectStoreSecret,
	}
}

// objectStoreArgs returns the object-store command line with optional dist.
func objectStoreArgs(dist string) []string {
	args := []string{"publish", "object-store", "--json"}
	if dist != "" {
		args = append(args, "--dist", dist)
	}

	return args
}
