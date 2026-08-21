package ghbucket

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/stage/pubscoop"
)

const (
	// pullRequestPageSize is GitHub's maximum pull-request list page size.
	pullRequestPageSize = 100
)

// ReadBase implements [pubscoop.RepositoryReader].
func (c *Client) ReadBase(
	ctx context.Context,
	repository pubscoop.Repository,
	path pubscoop.FilePath,
) (pubscoop.BaseSnapshot, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubscoop.BaseSnapshot{}, err
	}

	remote, _, err := c.github.Repositories.Get(ctx, repository.Owner, repository.Name)
	if err != nil {
		return pubscoop.BaseSnapshot{}, classify(err, "bucket repository")
	}
	branch := remote.GetDefaultBranch()
	if branch == "" {
		return pubscoop.BaseSnapshot{}, errors.New("bucket default branch is empty")
	}
	commit, err := c.refCommit(ctx, repository, pubscoop.BranchName(branch))
	if err != nil {
		return pubscoop.BaseSnapshot{}, err
	}
	file, err := c.readFile(ctx, repository, commit, path)
	if err != nil {
		return pubscoop.BaseSnapshot{}, err
	}

	return pubscoop.BaseSnapshot{
		Branch: pubscoop.BranchName(branch),
		Commit: commit,
		File:   file,
	}, nil
}

// ReadBranch implements [pubscoop.RepositoryReader].
func (c *Client) ReadBranch(
	ctx context.Context,
	repository pubscoop.Repository,
	branch pubscoop.BranchName,
	path pubscoop.FilePath,
) (pubscoop.BranchSnapshot, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubscoop.BranchSnapshot{}, err
	}

	commit, err := c.refCommit(ctx, repository, branch)
	if err != nil {
		if isNotFound(err) {
			return pubscoop.BranchSnapshot{}, nil
		}

		return pubscoop.BranchSnapshot{}, err
	}
	remote, _, err := c.github.Repositories.GetCommit(
		ctx,
		repository.Owner,
		repository.Name,
		commit.String(),
		nil,
	)
	if err != nil {
		return pubscoop.BranchSnapshot{}, classify(err, "publication branch commit")
	}
	file, err := c.readFile(ctx, repository, commit, path)
	if err != nil {
		return pubscoop.BranchSnapshot{}, err
	}

	return pubscoop.BranchSnapshot{
		Present: true,
		Commit:  commit,
		Parent:  soleParent(remote),
		Files:   changedFiles(remote.Files),
		File:    file,
	}, nil
}

// ReadPullRequest implements [pubscoop.RepositoryReader].
func (c *Client) ReadPullRequest(
	ctx context.Context,
	repository pubscoop.Repository,
	base pubscoop.BranchName,
	head pubscoop.BranchName,
) (pubscoop.PullRequest, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubscoop.PullRequest{}, err
	}

	options := &github.PullRequestListOptions{
		State: "all",
		Head:  repository.Owner + ":" + head.String(),
		Base:  base.String(),
		ListOptions: github.ListOptions{
			PerPage: pullRequestPageSize,
		},
	}
	var matched []*github.PullRequest
	for {
		pulls, response, err := c.github.PullRequests.List(
			ctx,
			repository.Owner,
			repository.Name,
			options,
		)
		if err != nil {
			return pubscoop.PullRequest{}, classify(err, "publication pull request")
		}
		for _, pull := range pulls {
			if pull.GetHead().GetRef() == head.String() && pull.GetBase().GetRef() == base.String() {
				matched = append(matched, pull)
			}
		}
		if response == nil || response.NextPage == 0 {
			break
		}
		options.Page = response.NextPage
	}

	switch len(matched) {
	case 0:
		return pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil
	case 1:
		return mapPullRequest(matched[0])
	default:
		return pubscoop.PullRequest{}, fmt.Errorf(
			"%w: multiple pull requests use branch %s",
			pubscoop.ErrConflict,
			head,
		)
	}
}

// refCommit resolves one branch without updating it.
func (c *Client) refCommit(
	ctx context.Context,
	repository pubscoop.Repository,
	branch pubscoop.BranchName,
) (pubscoop.CommitSHA, error) {
	ref, _, err := c.github.Git.GetRef(
		ctx,
		repository.Owner,
		repository.Name,
		"heads/"+branch.String(),
	)
	if err != nil {
		return "", classify(err, "repository branch")
	}
	sha := ref.GetObject().GetSHA()
	if sha == "" {
		return "", errors.New("repository branch commit is empty")
	}

	return pubscoop.CommitSHA(sha), nil
}

// readFile returns path at ref or a successful absent snapshot.
func (c *Client) readFile(
	ctx context.Context,
	repository pubscoop.Repository,
	ref pubscoop.CommitSHA,
	path pubscoop.FilePath,
) (pubscoop.File, error) {
	file, directory, _, err := c.github.Repositories.GetContents(
		ctx,
		repository.Owner,
		repository.Name,
		path.String(),
		&github.RepositoryContentGetOptions{Ref: ref.String()},
	)
	if err != nil {
		if isNotFound(err) {
			return pubscoop.File{}, nil
		}

		return pubscoop.File{}, classify(err, "repository manifest")
	}
	if file == nil || len(directory) != 0 || file.GetType() != "file" {
		return pubscoop.File{}, errors.New("repository manifest is not a regular file")
	}
	content, err := file.GetContent()
	if err != nil {
		return pubscoop.File{}, errors.New("repository manifest content is malformed")
	}
	if file.GetSHA() == "" {
		return pubscoop.File{}, errors.New("repository manifest blob SHA is empty")
	}

	return pubscoop.File{
		Present: true,
		Content: []byte(content),
		SHA:     pubscoop.BlobSHA(file.GetSHA()),
	}, nil
}

// soleParent returns the parent only when the commit has exactly one.
func soleParent(commit *github.RepositoryCommit) pubscoop.CommitSHA {
	if commit == nil || len(commit.Parents) != 1 {
		return ""
	}

	return pubscoop.CommitSHA(commit.Parents[0].GetSHA())
}

// changedFiles maps commit paths without interpreting publication policy.
func changedFiles(files []*github.CommitFile) []pubscoop.ChangedFile {
	changed := make([]pubscoop.ChangedFile, 0, len(files))
	for _, file := range files {
		changed = append(changed, pubscoop.ChangedFile{
			Path:   pubscoop.FilePath(file.GetFilename()),
			Status: pubscoop.ChangeStatus(file.GetStatus()),
		})
	}

	return changed
}

// mapPullRequest maps GitHub state onto the closed publication lifecycle.
func mapPullRequest(pull *github.PullRequest) (pubscoop.PullRequest, error) {
	if pull == nil || pull.GetHTMLURL() == "" {
		return pubscoop.PullRequest{}, errors.New("publication pull request URL is empty")
	}
	state := pubscoop.PullRequestClosed
	if pull.GetState() == "open" {
		state = pubscoop.PullRequestOpen
	} else if pull.MergedAt != nil {
		state = pubscoop.PullRequestMerged
	}

	return pubscoop.PullRequest{State: state, URL: pull.GetHTMLURL()}, nil
}
