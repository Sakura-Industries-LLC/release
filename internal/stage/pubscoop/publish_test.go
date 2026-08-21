package pubscoop_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghbucket/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubscoop"
)

const (
	// testBaseCommit is the observed bucket default-branch commit.
	testBaseCommit pubscoop.CommitSHA = "1111111111111111111111111111111111111111"
	// testHeadCommit is the publisher-created branch commit.
	testHeadCommit pubscoop.CommitSHA = "2222222222222222222222222222222222222222"
	// testBlobSHA is the current bucket manifest blob.
	testBlobSHA pubscoop.BlobSHA = "3333333333333333333333333333333333333333"
	// testPullURL is the publication review URL.
	testPullURL = "https://github.com/meigma/scoop-bucket/pull/7"
)

// TestPublishCreatesOnceAndRerunsOpen proves the first publication mutates the
// bucket once and a rerun returns the exact existing pull request.
func TestPublishCreatesOnceAndRerunsOpen(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	var branch pubscoop.BranchSnapshot
	pull := pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Twice()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		RunAndReturn(func(context.Context, pubscoop.Repository, pubscoop.BranchName, pubscoop.FilePath) (pubscoop.BranchSnapshot, error) {
			return branch, nil
		}).
		Maybe()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		RunAndReturn(func(context.Context, pubscoop.Repository, pubscoop.BranchName, pubscoop.BranchName) (pubscoop.PullRequest, error) {
			return pull, nil
		}).
		Maybe()
	test.writer.EXPECT().CreateBranch(mock.Anything, test.input.Bucket, test.branch, test.base.Commit).
		Run(func(context.Context, pubscoop.Repository, pubscoop.BranchName, pubscoop.CommitSHA) {
			branch = pubscoop.BranchSnapshot{
				Present: true,
				Commit:  test.base.Commit,
				Parent:  "0000000000000000000000000000000000000000",
				Files: []pubscoop.ChangedFile{{
					Path:   "README.md",
					Status: pubscoop.ChangeModified,
				}},
				File: test.base.File,
			}
		}).Return(nil).Once()
	test.writer.EXPECT().PutFile(
		mock.Anything,
		test.input.Bucket,
		test.branch,
		test.path,
		test.base.File.SHA,
		test.input.Content,
		"chore(scoop): update release-cli to 1.2.3",
	).Run(func(context.Context, pubscoop.Repository, pubscoop.BranchName, pubscoop.FilePath, pubscoop.BlobSHA, []byte, string) {
		branch = test.exactBranch()
	}).Return(nil).Once()
	test.writer.EXPECT().
		CreatePullRequest(mock.Anything, test.input.Bucket, mock.MatchedBy(func(input pubscoop.PullRequestInput) bool {
			return input.Base == test.base.Branch && input.Head == test.branch && input.Title != "" && input.Body != ""
		})).
		Run(func(context.Context, pubscoop.Repository, pubscoop.PullRequestInput) {
			pull = pubscoop.PullRequest{State: pubscoop.PullRequestOpen, URL: testPullURL}
		}).
		Return(testPullURL, nil).
		Once()

	created, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StateCreated, created.State)
	assert.Equal(t, "meigma/scoop-bucket", created.Bucket)
	assert.Equal(t, "release-cli", created.Manifest)
	assert.Equal(t, "release/release-cli/v1.2.3", created.Branch)
	assert.Equal(t, testPullURL, created.PullRequestURL)

	opened, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StateOpen, opened.State)
	assert.Equal(t, created.Branch, opened.Branch)
	assert.Equal(t, testPullURL, opened.PullRequestURL)
}

// TestPublishAcceptsMergedManifest proves exact default-branch content
// converges to published without a repository mutation.
func TestPublishAcceptsMergedManifest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	base := test.base
	base.File = pubscoop.File{Present: true, Content: test.input.Content, SHA: testBlobSHA}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestMerged, URL: testPullURL}, nil).Once()

	result, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StatePublished, result.State)
	assert.Equal(t, testPullURL, result.PullRequestURL)
}

// TestPublishReturnsExactOpenPullRequest proves an exact existing branch and
// open review are accepted without another mutation.
func TestPublishReturnsExactOpenPullRequest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestOpen, URL: testPullURL}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(test.exactBranch(), nil).Once()

	result, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StateOpen, result.State)
	assert.Equal(t, testPullURL, result.PullRequestURL)
}

// TestPublishConvergesAfterLostManifestCommit proves a retryable write is
// accepted when the follow-up read already shows the exact owned commit.
func TestPublishConvergesAfterLostManifestCommit(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	empty := pubscoop.BranchSnapshot{
		Present: true,
		Commit:  test.base.Commit,
		File:    test.base.File,
	}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(empty, nil).Once()
	test.writer.EXPECT().PutFile(
		mock.Anything,
		test.input.Bucket,
		test.branch,
		test.path,
		test.base.File.SHA,
		test.input.Content,
		"chore(scoop): update release-cli to 1.2.3",
	).Return(pubscoop.ErrRetryable).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(test.exactBranch(), nil).Once()
	test.writer.EXPECT().
		CreatePullRequest(mock.Anything, test.input.Bucket, mock.Anything).
		Return(testPullURL, nil).
		Once()

	result, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StateCreated, result.State)
	assert.Equal(t, testPullURL, result.PullRequestURL)
}

// TestPublishConvergesAfterLostPullRequest proves a retryable pull-request
// write converges when the follow-up read already shows the open review.
func TestPublishConvergesAfterLostPullRequest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(test.exactBranch(), nil).Once()
	test.writer.EXPECT().
		CreatePullRequest(mock.Anything, test.input.Bucket, mock.Anything).
		Return("", pubscoop.ErrRetryable).
		Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestOpen, URL: testPullURL}, nil).Once()

	result, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StateOpen, result.State)
	assert.Equal(t, testPullURL, result.PullRequestURL)
}

// TestPublishConvergesAfterLostBranchCreate proves a retryable branch create
// is accepted when the follow-up read already shows the owned ref.
func TestPublishConvergesAfterLostBranchCreate(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	empty := pubscoop.BranchSnapshot{}
	created := pubscoop.BranchSnapshot{
		Present: true,
		Commit:  test.base.Commit,
		File:    test.base.File,
	}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(empty, nil).Once()
	test.writer.EXPECT().CreateBranch(mock.Anything, test.input.Bucket, test.branch, test.base.Commit).
		Return(pubscoop.ErrRetryable).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(created, nil).Once()
	test.writer.EXPECT().PutFile(
		mock.Anything,
		test.input.Bucket,
		test.branch,
		test.path,
		test.base.File.SHA,
		test.input.Content,
		"chore(scoop): update release-cli to 1.2.3",
	).Return(nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(test.exactBranch(), nil).Once()
	test.writer.EXPECT().
		CreatePullRequest(mock.Anything, test.input.Bucket, mock.Anything).
		Return(testPullURL, nil).
		Once()

	result, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StateCreated, result.State)
	assert.Equal(t, testPullURL, result.PullRequestURL)
}

// TestPublishRetriesTransientBaseRead proves a retryable observation is
// retried before the publisher mutates anything.
func TestPublishRetriesTransientBaseRead(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	var waits []time.Duration
	test.input.Sleep = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	base := test.base
	base.File = pubscoop.File{Present: true, Content: test.input.Content, SHA: testBlobSHA}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).
		Return(pubscoop.BaseSnapshot{}, pubscoop.ErrRetryable).Once()
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestMerged, URL: testPullURL}, nil).Once()

	result, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubscoop.StatePublished, result.State)
	assert.Equal(t, []time.Duration{time.Second}, waits)
}

// TestPublishRejectsSameVersionWithDifferentContent proves immutable manifest
// content on the bucket default branch.
func TestPublishRejectsSameVersionWithDifferentContent(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	base := test.base
	base.File = pubscoop.File{
		Present: true,
		Content: manifest("1.2.3", "https://example.invalid/other.tar.gz"),
		SHA:     testBlobSHA,
	}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.Contains(t, err.Error(), "version 1.2.3 exists with different content")
}

// TestPublishRejectsNewerBucketManifest proves a newer default-branch version
// is never overwritten.
func TestPublishRejectsNewerBucketManifest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	base := test.base
	base.File = pubscoop.File{
		Present: true,
		Content: manifest("1.2.4", "https://example.invalid/newer.tar.gz"),
		SHA:     testBlobSHA,
	}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.Contains(t, err.Error(), "newer than release 1.2.3")
}

// TestPublishRejectsMalformedCurrentBucketManifest proves a present default-
// branch file must still parse before any mutation.
func TestPublishRejectsMalformedCurrentBucketManifest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	base := test.base
	base.File = pubscoop.File{
		Present: true,
		Content: []byte("{not-json"),
		SHA:     testBlobSHA,
	}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket manifest")
	assert.Contains(t, err.Error(), "malformed")
}

// TestPublishRejectsUnexpectedBranch proves a deterministic branch name does
// not authorize overwriting unrelated content.
func TestPublishRejectsUnexpectedBranch(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	unexpected := test.exactBranch()
	unexpected.Files = append(unexpected.Files, pubscoop.ChangedFile{
		Path:   "README.md",
		Status: pubscoop.ChangeModified,
	})
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(unexpected, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.Contains(t, err.Error(), "unexpected content")
}

// TestPublishRejectsChangedPathOutsideManifest proves the owned commit may
// change only the root manifest.
func TestPublishRejectsChangedPathOutsideManifest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	unexpected := test.exactBranch()
	unexpected.Files = []pubscoop.ChangedFile{{
		Path:   "bucket/release-cli.json",
		Status: pubscoop.ChangeModified,
	}}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(unexpected, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.Contains(t, err.Error(), "unexpected content")
}

// TestPublishRejectsUnexpectedParent proves matching manifest bytes cannot
// make a branch based on an unrelated commit publisher-owned.
func TestPublishRejectsUnexpectedParent(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	unexpected := test.exactBranch()
	unexpected.Parent = "3333333333333333333333333333333333333333"
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Bucket, test.branch, test.path).
		Return(unexpected, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.Contains(t, err.Error(), "unexpected content")
}

// TestPublishRejectsClosedPullRequest proves a closed unmerged review is a
// conflict even when the branch is otherwise exact.
func TestPublishRejectsClosedPullRequest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestClosed, URL: testPullURL}, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.Contains(t, err.Error(), "is closed")
}

// TestPublishRejectsMergedPullRequestWithoutExpectedManifest proves a merged
// review is not published unless the default branch already has the exact
// bytes.
func TestPublishRejectsMergedPullRequestWithoutExpectedManifest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Bucket, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Bucket, test.base.Branch, test.branch).
		Return(pubscoop.PullRequest{State: pubscoop.PullRequestMerged, URL: testPullURL}, nil).Once()

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.Contains(t, err.Error(), "did not publish the expected manifest")
}

// TestPublishRejectsMalformedGeneratedJSON proves the generated file must be
// an object before any repository I/O.
func TestPublishRejectsMalformedGeneratedJSON(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.input.Content = []byte("{not-json")

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed")
}

// TestPublishRejectsMissingGeneratedVersion proves the generated manifest
// must identify the source release.
func TestPublishRejectsMissingGeneratedVersion(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.input.Content = []byte(`{"url":"https://example.invalid/release.tar.gz"}`)

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no version")
}

// TestPublishRejectsNonStringGeneratedVersion proves a JSON version must be
// a string, not a number or object.
func TestPublishRejectsNonStringGeneratedVersion(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.input.Content = []byte(`{"version":1.2}`)

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a string")
}

// TestPublishRejectsMismatchedGeneratedVersion proves the generated version
// must equal the source release.
func TestPublishRejectsMismatchedGeneratedVersion(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.input.Content = manifest("9.9.9", "https://example.invalid/release.tar.gz")

	_, err := pubscoop.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generated manifest version 9.9.9, expected 1.2.3")
}

// TestPublishRejectsNilPorts proves validation occurs before remote I/O.
func TestPublishRejectsNilPorts(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	_, err := pubscoop.Publish(context.Background(), test.input, nil, test.writer)
	require.EqualError(t, err, "repository reader is nil")
	assert.NotErrorIs(t, err, pubscoop.ErrConflict)
}

// publishTest holds one valid publication fixture and generated mocks.
type publishTest struct {
	// input is the candidate manifest publication.
	input pubscoop.PublishInput
	// base is the older bucket state.
	base pubscoop.BaseSnapshot
	// path is the only permitted changed path.
	path pubscoop.FilePath
	// branch is the deterministic publication branch.
	branch pubscoop.BranchName
	// reader is the generated repository read mock.
	reader *mocks.MockRepositoryReader
	// writer is the generated repository write mock.
	writer *mocks.MockRepositoryWriter
}

// newPublishTest constructs one valid isolated publication fixture.
func newPublishTest(t *testing.T) *publishTest {
	t.Helper()

	version, err := rel.ParseVersion("1.2.3")
	require.NoError(t, err)
	bucket, err := pubscoop.ParseRepository("meigma/scoop-bucket")
	require.NoError(t, err)
	source, err := pubscoop.ParseRepository("meigma/release")
	require.NoError(t, err)
	name, err := pubscoop.ParseManifestName("release-cli")
	require.NoError(t, err)
	path := name.Path()

	return &publishTest{
		input: pubscoop.PublishInput{
			Bucket:   bucket,
			Source:   source,
			Version:  version,
			Commit:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Manifest: name,
			Content:  manifest("1.2.3", "https://example.invalid/release.tar.gz"),
			Sleep: func(context.Context, time.Duration) error {
				return nil
			},
		},
		base: pubscoop.BaseSnapshot{
			Branch: "main",
			Commit: testBaseCommit,
			File: pubscoop.File{
				Present: true,
				Content: manifest("1.2.2", "https://example.invalid/old.tar.gz"),
				SHA:     testBlobSHA,
			},
		},
		path:   path,
		branch: "release/release-cli/v1.2.3",
		reader: mocks.NewMockRepositoryReader(t),
		writer: mocks.NewMockRepositoryWriter(t),
	}
}

// exactBranch returns the one-commit manifest-only branch snapshot.
func (test *publishTest) exactBranch() pubscoop.BranchSnapshot {
	return pubscoop.BranchSnapshot{
		Present: true,
		Commit:  testHeadCommit,
		Parent:  test.base.Commit,
		Files: []pubscoop.ChangedFile{{
			Path:   test.path,
			Status: pubscoop.ChangeModified,
		}},
		File: pubscoop.File{
			Present: true,
			Content: test.input.Content,
			SHA:     "4444444444444444444444444444444444444444",
		},
	}
}

// manifest renders the minimum generated JSON needed by publication tests.
func manifest(version, sourceURL string) []byte {
	return []byte(`{"version":"` + version + `","url":"` + sourceURL + `"}`)
}
