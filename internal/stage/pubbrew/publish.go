package pubbrew

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/meigma/release/internal/rel"
)

// PublicationState is the reconciled tap outcome.
type PublicationState string

const (
	// StateCreated means this invocation created the pull request.
	StateCreated PublicationState = "created"
	// StateOpen means the exact pull request already existed.
	StateOpen PublicationState = "open"
	// StatePublished means the tap default branch already contains the cask.
	StatePublished PublicationState = "published"
)

// PullRequestState is the observed lifecycle state of one publication pull request.
type PullRequestState string

const (
	// PullRequestAbsent means no pull request uses the publication branch.
	PullRequestAbsent PullRequestState = "absent"
	// PullRequestOpen means the pull request awaits review or merge.
	PullRequestOpen PullRequestState = "open"
	// PullRequestMerged means the pull request was merged.
	PullRequestMerged PullRequestState = "merged"
	// PullRequestClosed means the pull request was closed without merging.
	PullRequestClosed PullRequestState = "closed"
)

// BaseSnapshot is the tap default branch and cask observed together.
type BaseSnapshot struct {
	// Branch is the tap's current default branch.
	Branch BranchName
	// Commit is the default branch head commit.
	Commit CommitSHA
	// File is the cask at Commit.
	File File
}

// BranchSnapshot is the publication branch head and cask.
type BranchSnapshot struct {
	// Present reports whether the branch exists.
	Present bool
	// Commit is the branch head commit when Present is true.
	Commit CommitSHA
	// Parent is the sole parent of Commit. It is empty unless Commit has
	// exactly one parent.
	Parent CommitSHA
	// Files are the paths changed by Commit.
	Files []ChangedFile
	// File is the cask at Commit.
	File File
}

// PullRequest is the unique pull request for a publication branch.
type PullRequest struct {
	// State is the observed pull-request lifecycle.
	State PullRequestState
	// URL is the human-facing pull-request URL when State is not absent.
	URL string
}

// PullRequestInput is the closed request used to open a publication review.
type PullRequestInput struct {
	// Base is the tap default branch.
	Base BranchName
	// Head is the publication branch.
	Head BranchName
	// Title is the pull-request title.
	Title string
	// Body is the pull-request description.
	Body string
}

// RepositoryReader observes tap branches, casks, and pull requests.
type RepositoryReader interface {
	// ReadBase returns the default branch, its head, and path at that head.
	ReadBase(ctx context.Context, repository Repository, path FilePath) (BaseSnapshot, error)
	// ReadBranch returns branch head metadata and path at that head. An absent
	// branch is a successful snapshot with Present false.
	ReadBranch(
		ctx context.Context,
		repository Repository,
		branch BranchName,
		path FilePath,
	) (BranchSnapshot, error)
	// ReadPullRequest returns the unique pull request from head into base. No
	// match is a successful result with State absent.
	ReadPullRequest(
		ctx context.Context,
		repository Repository,
		base BranchName,
		head BranchName,
	) (PullRequest, error)
}

// RepositoryWriter creates publisher-owned branches, commits, and pull requests.
type RepositoryWriter interface {
	// CreateBranch creates branch at from without updating an existing ref.
	CreateBranch(
		ctx context.Context,
		repository Repository,
		branch BranchName,
		from CommitSHA,
	) error
	// PutFile creates one commit on branch that creates or replaces path.
	// Previous is empty for a new path and the current base blob for an update.
	PutFile(
		ctx context.Context,
		repository Repository,
		branch BranchName,
		path FilePath,
		previous BlobSHA,
		content []byte,
		message string,
	) error
	// CreatePullRequest opens a non-draft pull request without auto-merge.
	CreatePullRequest(
		ctx context.Context,
		repository Repository,
		input PullRequestInput,
	) (string, error)
}

// PublishInput is the closed input to [Publish].
type PublishInput struct {
	// Tap is the Homebrew tap repository to update.
	Tap Repository
	// Source is the producer repository that owns the release.
	Source Repository
	// Version is the stable released version.
	Version rel.Version
	// Commit is the producer commit that built the release.
	Commit CommitSHA
	// Cask is the expected cask token and filename stem.
	Cask CaskToken
	// Content is the generated cask Ruby source.
	Content []byte
	// Sleep waits between retryable observations. Nil selects a
	// context-aware timer.
	Sleep SleepFunc
}

// PublishResult is the JSON payload produced by a successful [Publish].
type PublishResult struct {
	// Tap is the target owner/repository.
	Tap string `json:"tap"`
	// Cask is the published cask token.
	Cask string `json:"cask"`
	// Branch is the deterministic publication branch.
	Branch string `json:"branch"`
	// PullRequestURL is the review URL. It can be empty when matching content
	// reached the default branch outside a discoverable pull request.
	PullRequestURL string `json:"pull_request_url"`
	// State is created, open, or published.
	State PublicationState `json:"state"`
}

// Publish reconciles one generated cask through a tap pull request.
//
// It never writes the default branch, force-updates a branch, deletes a path,
// or enables auto-merge. Remote write errors are followed by a fresh read
// before retry so an accepted request with a lost response cannot duplicate a
// commit or pull request.
func Publish(
	ctx context.Context,
	input PublishInput,
	reader RepositoryReader,
	writer RepositoryWriter,
) (PublishResult, error) {
	if err := validatePublish(ctx, input, reader, writer); err != nil {
		return PublishResult{}, err
	}
	desiredVersion, err := caskVersion(input.Content)
	if err != nil {
		return PublishResult{}, fmt.Errorf("generated cask: %w", err)
	}
	if desiredVersion != input.Version {
		return PublishResult{}, fmt.Errorf(
			"generated cask version %s, expected %s",
			desiredVersion,
			input.Version,
		)
	}

	sleep := input.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	return publish(ctx, input, reader, writer, sleep)
}

// validatePublish rejects incomplete input and nil ports before any I/O.
func validatePublish(
	ctx context.Context,
	input PublishInput,
	reader RepositoryReader,
	writer RepositoryWriter,
) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if reader == nil {
		return errors.New("repository reader is nil")
	}
	if writer == nil {
		return errors.New("repository writer is nil")
	}
	if input.Tap.Owner == "" || input.Tap.Name == "" {
		return errors.New("tap repository is empty")
	}
	if input.Source.Owner == "" || input.Source.Name == "" {
		return errors.New("source repository is empty")
	}
	if input.Commit == "" {
		return errors.New("source commit is empty")
	}
	if input.Cask == "" {
		return errors.New("cask token is empty")
	}
	if len(input.Content) == 0 {
		return errors.New("cask content is empty")
	}

	return nil
}

// publish runs the ordered state machine after exported guards.
func publish(
	ctx context.Context,
	input PublishInput,
	reader RepositoryReader,
	writer RepositoryWriter,
	sleep SleepFunc,
) (PublishResult, error) {
	path := input.Cask.Path()
	branch := publicationBranch(input.Cask, input.Version)
	base, err := readBase(ctx, reader, input.Tap, path, sleep)
	if err != nil {
		return PublishResult{}, err
	}
	err = validateBase(base)
	if err != nil {
		return PublishResult{}, err
	}

	pull, err := readPullRequest(ctx, reader, input.Tap, base.Branch, branch, sleep)
	if err != nil {
		return PublishResult{}, err
	}
	if base.File.Present && bytes.Equal(base.File.Content, input.Content) {
		return result(input, branch, pull.URL, StatePublished), nil
	}
	err = rejectBaseConflict(base.File, input)
	if err != nil {
		return PublishResult{}, err
	}
	if pull.State == PullRequestMerged {
		return PublishResult{}, fmt.Errorf(
			"%w: merged pull request %s did not publish the expected cask",
			ErrConflict,
			pull.URL,
		)
	}
	if pull.State == PullRequestClosed {
		return PublishResult{}, fmt.Errorf(
			"%w: publication pull request %s is closed",
			ErrConflict,
			pull.URL,
		)
	}

	observed, err := ensureBranch(ctx, input, reader, writer, sleep, base, branch, path)
	if err != nil {
		return PublishResult{}, err
	}
	if err := requireExactBranch(observed, input.Content, path, base.Commit); err != nil {
		return PublishResult{}, err
	}
	if pull.State == PullRequestOpen {
		return result(input, branch, pull.URL, StateOpen), nil
	}

	return ensurePullRequest(ctx, input, reader, writer, sleep, base.Branch, branch)
}

// readBase observes the default branch with bounded transient retries.
func readBase(
	ctx context.Context,
	reader RepositoryReader,
	tap Repository,
	path FilePath,
	sleep SleepFunc,
) (BaseSnapshot, error) {
	base, err := retryRead(ctx, sleep, func() (BaseSnapshot, error) {
		return reader.ReadBase(ctx, tap, path)
	})
	if err != nil {
		return BaseSnapshot{}, fmt.Errorf("read tap base: %w", err)
	}

	return base, nil
}

// readBranch observes the publication branch with bounded transient retries.
func readBranch(
	ctx context.Context,
	reader RepositoryReader,
	tap Repository,
	branch BranchName,
	path FilePath,
	sleep SleepFunc,
) (BranchSnapshot, error) {
	observed, err := retryRead(ctx, sleep, func() (BranchSnapshot, error) {
		return reader.ReadBranch(ctx, tap, branch, path)
	})
	if err != nil {
		return BranchSnapshot{}, fmt.Errorf("read publication branch: %w", err)
	}

	return observed, nil
}

// readPullRequest observes the publication pull request with bounded retries.
func readPullRequest(
	ctx context.Context,
	reader RepositoryReader,
	tap Repository,
	base BranchName,
	branch BranchName,
	sleep SleepFunc,
) (PullRequest, error) {
	pull, err := retryRead(ctx, sleep, func() (PullRequest, error) {
		return reader.ReadPullRequest(ctx, tap, base, branch)
	})
	if err != nil {
		return PullRequest{}, fmt.Errorf("read publication pull request: %w", err)
	}

	return pull, nil
}

// validateBase rejects incomplete repository metadata.
func validateBase(base BaseSnapshot) error {
	if base.Branch == "" {
		return errors.New("tap default branch is empty")
	}
	if base.Commit == "" {
		return errors.New("tap default branch commit is empty")
	}
	if base.File.Present && base.File.SHA == "" {
		return errors.New("tap cask blob SHA is empty")
	}

	return nil
}

// rejectBaseConflict refuses equal or newer cask versions with different content.
func rejectBaseConflict(current File, input PublishInput) error {
	if !current.Present {
		return nil
	}
	version, err := caskVersion(current.Content)
	if err != nil {
		return fmt.Errorf("tap cask: %w", err)
	}
	comparison := version.Compare(input.Version)
	if comparison == 0 {
		return fmt.Errorf(
			"%w: cask version %s exists with different content",
			ErrConflict,
			version,
		)
	}
	if comparison > 0 {
		return fmt.Errorf(
			"%w: tap cask version %s is newer than release %s",
			ErrConflict,
			version,
			input.Version,
		)
	}

	return nil
}

// ensureBranch creates or converges the deterministic publication branch.
func ensureBranch(
	ctx context.Context,
	input PublishInput,
	reader RepositoryReader,
	writer RepositoryWriter,
	sleep SleepFunc,
	base BaseSnapshot,
	branch BranchName,
	path FilePath,
) (BranchSnapshot, error) {
	observed, err := readBranch(ctx, reader, input.Tap, branch, path, sleep)
	if err != nil {
		return BranchSnapshot{}, err
	}
	if !observed.Present {
		observed, err = createBranch(ctx, input.Tap, reader, writer, sleep, base, branch, path)
		if err != nil {
			return BranchSnapshot{}, err
		}
	}
	if exactBranch(observed, input.Content, path, base.Commit) {
		return observed, nil
	}
	if !emptyBranch(observed, base) {
		return BranchSnapshot{}, unexpectedBranch(branch)
	}

	return putCask(ctx, input, reader, writer, sleep, base, branch, path)
}

// createBranch creates a missing branch and re-observes ambiguous outcomes.
func createBranch(
	ctx context.Context,
	tap Repository,
	reader RepositoryReader,
	writer RepositoryWriter,
	sleep SleepFunc,
	base BaseSnapshot,
	branch BranchName,
	path FilePath,
) (BranchSnapshot, error) {
	for attempt := range retryAttempts {
		err := writer.CreateBranch(ctx, tap, branch, base.Commit)
		observed, readErr := readBranch(ctx, reader, tap, branch, path, sleep)
		if readErr != nil {
			return BranchSnapshot{}, readErr
		}
		if observed.Present {
			return observed, nil
		}
		if err == nil {
			return BranchSnapshot{}, errors.New("created publication branch is absent")
		}
		if !errors.Is(err, ErrRetryable) || attempt == retryAttempts-1 {
			return BranchSnapshot{}, fmt.Errorf("create publication branch: %w", err)
		}
		if err := sleep(ctx, retryBaseDelay<<attempt); err != nil {
			return BranchSnapshot{}, fmt.Errorf("retry branch creation: %w", err)
		}
	}

	return BranchSnapshot{}, errors.New("create publication branch exhausted retries")
}

// putCask commits the cask and re-observes ambiguous write outcomes.
func putCask(
	ctx context.Context,
	input PublishInput,
	reader RepositoryReader,
	writer RepositoryWriter,
	sleep SleepFunc,
	base BaseSnapshot,
	branch BranchName,
	path FilePath,
) (BranchSnapshot, error) {
	message := "chore(cask): update " + input.Cask.String() + " to " + input.Version.String()
	for attempt := range retryAttempts {
		err := writer.PutFile(ctx, input.Tap, branch, path, base.File.SHA, input.Content, message)
		observed, readErr := readBranch(ctx, reader, input.Tap, branch, path, sleep)
		if readErr != nil {
			return BranchSnapshot{}, readErr
		}
		if exactBranch(observed, input.Content, path, base.Commit) {
			return observed, nil
		}
		if err == nil {
			return BranchSnapshot{}, unexpectedBranch(branch)
		}
		if !emptyBranch(observed, base) {
			return BranchSnapshot{}, unexpectedBranch(branch)
		}
		if !errors.Is(err, ErrRetryable) || attempt == retryAttempts-1 {
			return BranchSnapshot{}, fmt.Errorf("commit cask: %w", err)
		}
		if err := sleep(ctx, retryBaseDelay<<attempt); err != nil {
			return BranchSnapshot{}, fmt.Errorf("retry cask commit: %w", err)
		}
	}

	return BranchSnapshot{}, errors.New("commit cask exhausted retries")
}

// ensurePullRequest creates the review or accepts one created concurrently.
func ensurePullRequest(
	ctx context.Context,
	input PublishInput,
	reader RepositoryReader,
	writer RepositoryWriter,
	sleep SleepFunc,
	base BranchName,
	branch BranchName,
) (PublishResult, error) {
	request := PullRequestInput{
		Base:  base,
		Head:  branch,
		Title: "chore(cask): update " + input.Cask.String() + " to " + input.Version.String(),
		Body:  pullRequestBody(input),
	}
	for attempt := range retryAttempts {
		url, err := writer.CreatePullRequest(ctx, input.Tap, request)
		if err == nil {
			if url == "" {
				return PublishResult{}, errors.New("created pull request URL is empty")
			}

			return result(input, branch, url, StateCreated), nil
		}

		observed, readErr := readPullRequest(ctx, reader, input.Tap, base, branch, sleep)
		if readErr != nil {
			return PublishResult{}, readErr
		}
		if observed.State == PullRequestOpen {
			return result(input, branch, observed.URL, StateOpen), nil
		}
		if observed.State != PullRequestAbsent {
			return PublishResult{}, fmt.Errorf(
				"%w: publication pull request %s is %s",
				ErrConflict,
				observed.URL,
				observed.State,
			)
		}
		if !errors.Is(err, ErrRetryable) || attempt == retryAttempts-1 {
			return PublishResult{}, fmt.Errorf("create publication pull request: %w", err)
		}
		if err := sleep(ctx, retryBaseDelay<<attempt); err != nil {
			return PublishResult{}, fmt.Errorf("retry pull-request creation: %w", err)
		}
	}

	return PublishResult{}, errors.New("create publication pull request exhausted retries")
}

// pullRequestBody records the immutable producer identity for review.
func pullRequestBody(input PublishInput) string {
	return "Update `" + input.Cask.String() + "` to `" + input.Version.String() + "`.\n\n" +
		"Source release: https://github.com/" + input.Source.String() + "/releases/tag/v" + input.Version.String() + "\n" +
		"Source commit: `" + input.Commit.String() + "`\n"
}

// exactBranch reports whether the branch head is one exact cask-only commit.
func exactBranch(
	branch BranchSnapshot,
	content []byte,
	path FilePath,
	base CommitSHA,
) bool {
	if !branch.Present || branch.Commit == "" || branch.Commit == base || branch.Parent != base {
		return false
	}
	if len(branch.Files) != 1 || branch.Files[0].Path != path {
		return false
	}
	if branch.Files[0].Status != ChangeAdded && branch.Files[0].Status != ChangeModified {
		return false
	}

	return branch.File.Present && bytes.Equal(branch.File.Content, content)
}

// requireExactBranch returns a conflict for any unexpected branch state.
func requireExactBranch(
	branch BranchSnapshot,
	content []byte,
	path FilePath,
	base CommitSHA,
) error {
	if exactBranch(branch, content, path, base) {
		return nil
	}

	return unexpectedBranch(BranchName(""))
}

// emptyBranch reports whether the branch still exactly matches the observed base.
func emptyBranch(branch BranchSnapshot, base BaseSnapshot) bool {
	if !branch.Present || branch.Commit != base.Commit {
		return false
	}
	if branch.File.Present != base.File.Present {
		return false
	}
	if !branch.File.Present {
		return true
	}

	return branch.File.SHA == base.File.SHA && bytes.Equal(branch.File.Content, base.File.Content)
}

// unexpectedBranch returns the stable publisher-branch conflict.
func unexpectedBranch(branch BranchName) error {
	if branch == "" {
		return fmt.Errorf("%w: publication branch has unexpected content", ErrConflict)
	}

	return fmt.Errorf("%w: publication branch %s has unexpected content", ErrConflict, branch)
}

// result constructs the stable successful JSON payload.
func result(
	input PublishInput,
	branch BranchName,
	url string,
	state PublicationState,
) PublishResult {
	return PublishResult{
		Tap:            input.Tap.String(),
		Cask:           input.Cask.String(),
		Branch:         branch.String(),
		PullRequestURL: url,
		State:          state,
	}
}
