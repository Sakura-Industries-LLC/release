package pubbrew_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghtap/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubbrew"
)

const (
	// testBaseCommit is the observed tap default-branch commit.
	testBaseCommit pubbrew.CommitSHA = "1111111111111111111111111111111111111111"
	// testHeadCommit is the publisher-created branch commit.
	testHeadCommit pubbrew.CommitSHA = "2222222222222222222222222222222222222222"
	// testBlobSHA is the current tap cask blob.
	testBlobSHA pubbrew.BlobSHA = "3333333333333333333333333333333333333333"
	// testPullURL is the publication review URL.
	testPullURL = "https://github.com/meigma/homebrew-tap/pull/7"
)

// TestPublishCreatesOnceAndRerunsOpen proves the first publication mutates the
// tap once and a rerun returns the exact existing pull request.
func TestPublishCreatesOnceAndRerunsOpen(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	var branch pubbrew.BranchSnapshot
	pull := pubbrew.PullRequest{State: pubbrew.PullRequestAbsent}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Tap, test.path).Return(test.base, nil).Twice()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Tap, test.branch, test.path).
		RunAndReturn(func(context.Context, pubbrew.Repository, pubbrew.BranchName, pubbrew.FilePath) (pubbrew.BranchSnapshot, error) {
			return branch, nil
		}).
		Maybe()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Tap, test.base.Branch, test.branch).
		RunAndReturn(func(context.Context, pubbrew.Repository, pubbrew.BranchName, pubbrew.BranchName) (pubbrew.PullRequest, error) {
			return pull, nil
		}).
		Maybe()
	test.writer.EXPECT().CreateBranch(mock.Anything, test.input.Tap, test.branch, test.base.Commit).
		Run(func(context.Context, pubbrew.Repository, pubbrew.BranchName, pubbrew.CommitSHA) {
			branch = pubbrew.BranchSnapshot{
				Present: true,
				Commit:  test.base.Commit,
				Parent:  "0000000000000000000000000000000000000000",
				Files: []pubbrew.ChangedFile{{
					Path:   "README.md",
					Status: pubbrew.ChangeModified,
				}},
				File: test.base.File,
			}
		}).Return(nil).Once()
	test.writer.EXPECT().PutFile(
		mock.Anything,
		test.input.Tap,
		test.branch,
		test.path,
		test.base.File.SHA,
		test.input.Content,
		"chore(cask): update release-cli to 1.2.3",
	).Run(func(context.Context, pubbrew.Repository, pubbrew.BranchName, pubbrew.FilePath, pubbrew.BlobSHA, []byte, string) {
		branch = test.exactBranch()
	}).Return(nil).Once()
	test.writer.EXPECT().
		CreatePullRequest(mock.Anything, test.input.Tap, mock.MatchedBy(func(input pubbrew.PullRequestInput) bool {
			return input.Base == test.base.Branch && input.Head == test.branch && input.Title != "" && input.Body != ""
		})).
		Run(func(context.Context, pubbrew.Repository, pubbrew.PullRequestInput) {
			pull = pubbrew.PullRequest{State: pubbrew.PullRequestOpen, URL: testPullURL}
		}).
		Return(testPullURL, nil).
		Once()

	created, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubbrew.StateCreated, created.State)
	assert.Equal(t, testPullURL, created.PullRequestURL)

	opened, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubbrew.StateOpen, opened.State)
	assert.Equal(t, created.Branch, opened.Branch)
	assert.Equal(t, testPullURL, opened.PullRequestURL)
}

// TestPublishAcceptsMergedCask proves exact default-branch content converges to
// published without a repository mutation.
func TestPublishAcceptsMergedCask(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	base := test.base
	base.File = pubbrew.File{Present: true, Content: test.input.Content, SHA: testBlobSHA}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Tap, test.path).Return(base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Tap, base.Branch, test.branch).
		Return(pubbrew.PullRequest{State: pubbrew.PullRequestMerged, URL: testPullURL}, nil).Once()

	result, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubbrew.StatePublished, result.State)
	assert.Equal(t, testPullURL, result.PullRequestURL)
}

// TestPublishReturnsExactOpenPullRequest proves an exact existing branch and
// open review are accepted without another mutation.
func TestPublishReturnsExactOpenPullRequest(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Tap, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Tap, test.base.Branch, test.branch).
		Return(pubbrew.PullRequest{State: pubbrew.PullRequestOpen, URL: testPullURL}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Tap, test.branch, test.path).
		Return(test.exactBranch(), nil).Once()

	result, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.NoError(t, err)
	assert.Equal(t, pubbrew.StateOpen, result.State)
	assert.Equal(t, testPullURL, result.PullRequestURL)
}

// TestPublishRejectsSameVersionWithDifferentContent proves immutable cask
// content on the tap default branch.
func TestPublishRejectsSameVersionWithDifferentContent(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	base := test.base
	base.File = pubbrew.File{
		Present: true,
		Content: cask("1.2.3", "https://example.invalid/other.tar.gz"),
		SHA:     testBlobSHA,
	}
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Tap, test.path).Return(base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Tap, base.Branch, test.branch).
		Return(pubbrew.PullRequest{State: pubbrew.PullRequestAbsent}, nil).Once()

	_, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubbrew.ErrConflict)
	assert.Contains(t, err.Error(), "version 1.2.3 exists with different content")
}

// TestPublishRejectsUnexpectedBranch proves a deterministic branch name does
// not authorize overwriting unrelated content.
func TestPublishRejectsUnexpectedBranch(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	unexpected := test.exactBranch()
	unexpected.Files = append(unexpected.Files, pubbrew.ChangedFile{
		Path:   "README.md",
		Status: pubbrew.ChangeModified,
	})
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Tap, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Tap, test.base.Branch, test.branch).
		Return(pubbrew.PullRequest{State: pubbrew.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Tap, test.branch, test.path).
		Return(unexpected, nil).Once()

	_, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubbrew.ErrConflict)
	assert.Contains(t, err.Error(), "unexpected content")
}

// TestPublishRejectsUnexpectedParent proves matching cask bytes cannot make a
// branch based on an unrelated commit publisher-owned.
func TestPublishRejectsUnexpectedParent(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	unexpected := test.exactBranch()
	unexpected.Parent = "3333333333333333333333333333333333333333"
	test.reader.EXPECT().ReadBase(mock.Anything, test.input.Tap, test.path).Return(test.base, nil).Once()
	test.reader.EXPECT().ReadPullRequest(mock.Anything, test.input.Tap, test.base.Branch, test.branch).
		Return(pubbrew.PullRequest{State: pubbrew.PullRequestAbsent}, nil).Once()
	test.reader.EXPECT().ReadBranch(mock.Anything, test.input.Tap, test.branch, test.path).
		Return(unexpected, nil).Once()

	_, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	require.ErrorIs(t, err, pubbrew.ErrConflict)
	assert.Contains(t, err.Error(), "unexpected content")
}

// TestPublishRejectsMalformedGeneratedVersion proves the generated cask must
// identify the source release exactly.
func TestPublishRejectsMalformedGeneratedVersion(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	test.input.Content = []byte("cask \"release-cli\" do\nend\n")

	_, err := pubbrew.Publish(context.Background(), test.input, test.reader, test.writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no literal version declaration")
}

// TestPublishRejectsNilPorts proves validation occurs before remote I/O.
func TestPublishRejectsNilPorts(t *testing.T) {
	t.Parallel()

	test := newPublishTest(t)
	_, err := pubbrew.Publish(context.Background(), test.input, nil, test.writer)
	require.EqualError(t, err, "repository reader is nil")
	assert.NotErrorIs(t, err, pubbrew.ErrConflict)
}

// publishTest holds one valid publication fixture and generated mocks.
type publishTest struct {
	// input is the candidate cask publication.
	input pubbrew.PublishInput
	// base is the older tap state.
	base pubbrew.BaseSnapshot
	// path is the only permitted changed path.
	path pubbrew.FilePath
	// branch is the deterministic publication branch.
	branch pubbrew.BranchName
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
	tap, err := pubbrew.ParseRepository("meigma/homebrew-tap")
	require.NoError(t, err)
	source, err := pubbrew.ParseRepository("meigma/release")
	require.NoError(t, err)
	token, err := pubbrew.ParseCaskToken("release-cli")
	require.NoError(t, err)
	path := token.Path()

	return &publishTest{
		input: pubbrew.PublishInput{
			Tap:     tap,
			Source:  source,
			Version: version,
			Commit:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Cask:    token,
			Content: cask("1.2.3", "https://example.invalid/release.tar.gz"),
			Sleep: func(context.Context, time.Duration) error {
				return nil
			},
		},
		base: pubbrew.BaseSnapshot{
			Branch: "main",
			Commit: testBaseCommit,
			File: pubbrew.File{
				Present: true,
				Content: cask("1.2.2", "https://example.invalid/old.tar.gz"),
				SHA:     testBlobSHA,
			},
		},
		path:   path,
		branch: "release/release-cli/v1.2.3",
		reader: mocks.NewMockRepositoryReader(t),
		writer: mocks.NewMockRepositoryWriter(t),
	}
}

// exactBranch returns the one-commit cask-only branch snapshot.
func (test *publishTest) exactBranch() pubbrew.BranchSnapshot {
	return pubbrew.BranchSnapshot{
		Present: true,
		Commit:  testHeadCommit,
		Parent:  test.base.Commit,
		Files: []pubbrew.ChangedFile{{
			Path:   test.path,
			Status: pubbrew.ChangeModified,
		}},
		File: pubbrew.File{
			Present: true,
			Content: test.input.Content,
			SHA:     "4444444444444444444444444444444444444444",
		},
	}
}

// cask renders the minimum generated Ruby needed by publication tests.
func cask(version, sourceURL string) []byte {
	return []byte("cask \"release-cli\" do\n  version \"" + version + "\"\n  url \"" + sourceURL + "\"\nend\n")
}
