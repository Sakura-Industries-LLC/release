package ghtap

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/stage/pubbrew"
)

const (
	// pullRequestPageSize is GitHub's maximum pull-request list page size.
	pullRequestPageSize = 100
)

// ReadBase implements [pubbrew.RepositoryReader].
func (c *Client) ReadBase(
	ctx context.Context,
	repository pubbrew.Repository,
	path pubbrew.FilePath,
) (pubbrew.BaseSnapshot, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubbrew.BaseSnapshot{}, err
	}

	remote, _, err := c.github.Repositories.Get(ctx, repository.Owner, repository.Name)
	if err != nil {
		return pubbrew.BaseSnapshot{}, classify(err, "tap repository")
	}
	branch := remote.GetDefaultBranch()
	if branch == "" {
		return pubbrew.BaseSnapshot{}, errors.New("tap default branch is empty")
	}
	commit, err := c.refCommit(ctx, repository, pubbrew.BranchName(branch))
	if err != nil {
		return pubbrew.BaseSnapshot{}, err
	}
	file, err := c.readFile(ctx, repository, commit, path)
	if err != nil {
		return pubbrew.BaseSnapshot{}, err
	}

	return pubbrew.BaseSnapshot{
		Branch: pubbrew.BranchName(branch),
		Commit: commit,
		File:   file,
	}, nil
}

// ReadBranch implements [pubbrew.RepositoryReader].
func (c *Client) ReadBranch(
	ctx context.Context,
	repository pubbrew.Repository,
	branch pubbrew.BranchName,
	path pubbrew.FilePath,
) (pubbrew.BranchSnapshot, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubbrew.BranchSnapshot{}, err
	}

	commit, err := c.refCommit(ctx, repository, branch)
	if err != nil {
		if isNotFound(err) {
			return pubbrew.BranchSnapshot{}, nil
		}

		return pubbrew.BranchSnapshot{}, err
	}
	remote, _, err := c.github.Repositories.GetCommit(
		ctx,
		repository.Owner,
		repository.Name,
		commit.String(),
		nil,
	)
	if err != nil {
		return pubbrew.BranchSnapshot{}, classify(err, "publication branch commit")
	}
	file, err := c.readFile(ctx, repository, commit, path)
	if err != nil {
		return pubbrew.BranchSnapshot{}, err
	}

	return pubbrew.BranchSnapshot{
		Present: true,
		Commit:  commit,
		Parent:  soleParent(remote),
		Files:   changedFiles(remote.Files),
		File:    file,
	}, nil
}

// ReadPullRequest implements [pubbrew.RepositoryReader].
func (c *Client) ReadPullRequest(
	ctx context.Context,
	repository pubbrew.Repository,
	base pubbrew.BranchName,
	head pubbrew.BranchName,
) (pubbrew.PullRequest, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubbrew.PullRequest{}, err
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
			return pubbrew.PullRequest{}, classify(err, "publication pull request")
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
		return pubbrew.PullRequest{State: pubbrew.PullRequestAbsent}, nil
	case 1:
		return mapPullRequest(matched[0])
	default:
		return pubbrew.PullRequest{}, fmt.Errorf(
			"%w: multiple pull requests use branch %s",
			pubbrew.ErrConflict,
			head,
		)
	}
}

// refCommit resolves one branch without updating it.
func (c *Client) refCommit(
	ctx context.Context,
	repository pubbrew.Repository,
	branch pubbrew.BranchName,
) (pubbrew.CommitSHA, error) {
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

	return pubbrew.CommitSHA(sha), nil
}

// readFile returns path at ref or a successful absent snapshot.
func (c *Client) readFile(
	ctx context.Context,
	repository pubbrew.Repository,
	ref pubbrew.CommitSHA,
	path pubbrew.FilePath,
) (pubbrew.File, error) {
	file, directory, _, err := c.github.Repositories.GetContents(
		ctx,
		repository.Owner,
		repository.Name,
		path.String(),
		&github.RepositoryContentGetOptions{Ref: ref.String()},
	)
	if err != nil {
		if isNotFound(err) {
			return pubbrew.File{}, nil
		}

		return pubbrew.File{}, classify(err, "repository cask")
	}
	if file == nil || len(directory) != 0 || file.GetType() != "file" {
		return pubbrew.File{}, errors.New("repository cask is not a regular file")
	}
	content, err := file.GetContent()
	if err != nil {
		return pubbrew.File{}, errors.New("repository cask content is malformed")
	}
	if file.GetSHA() == "" {
		return pubbrew.File{}, errors.New("repository cask blob SHA is empty")
	}

	return pubbrew.File{
		Present: true,
		Content: []byte(content),
		SHA:     pubbrew.BlobSHA(file.GetSHA()),
	}, nil
}

// soleParent returns the parent only when the commit has exactly one.
func soleParent(commit *github.RepositoryCommit) pubbrew.CommitSHA {
	if commit == nil || len(commit.Parents) != 1 {
		return ""
	}

	return pubbrew.CommitSHA(commit.Parents[0].GetSHA())
}

// changedFiles maps commit paths without interpreting publication policy.
func changedFiles(files []*github.CommitFile) []pubbrew.ChangedFile {
	changed := make([]pubbrew.ChangedFile, 0, len(files))
	for _, file := range files {
		changed = append(changed, pubbrew.ChangedFile{
			Path:   pubbrew.FilePath(file.GetFilename()),
			Status: pubbrew.ChangeStatus(file.GetStatus()),
		})
	}

	return changed
}

// mapPullRequest maps GitHub state onto the closed publication lifecycle.
func mapPullRequest(pull *github.PullRequest) (pubbrew.PullRequest, error) {
	if pull == nil || pull.GetHTMLURL() == "" {
		return pubbrew.PullRequest{}, errors.New("publication pull request URL is empty")
	}
	state := pubbrew.PullRequestClosed
	if pull.GetState() == "open" {
		state = pubbrew.PullRequestOpen
	} else if pull.MergedAt != nil {
		state = pubbrew.PullRequestMerged
	}

	return pubbrew.PullRequest{State: state, URL: pull.GetHTMLURL()}, nil
}
