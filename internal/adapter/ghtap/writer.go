package ghtap

import (
	"context"
	"errors"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/stage/pubbrew"
)

// CreateBranch implements [pubbrew.RepositoryWriter].
func (c *Client) CreateBranch(
	ctx context.Context,
	repository pubbrew.Repository,
	branch pubbrew.BranchName,
	from pubbrew.CommitSHA,
) error {
	if err := c.requireReady(ctx); err != nil {
		return err
	}

	_, _, err := c.github.Git.CreateRef(
		ctx,
		repository.Owner,
		repository.Name,
		github.CreateRef{
			Ref: "refs/heads/" + branch.String(),
			SHA: from.String(),
		},
	)
	if err != nil {
		return classify(err, "publication branch")
	}

	return nil
}

// PutFile implements [pubbrew.RepositoryWriter].
func (c *Client) PutFile(
	ctx context.Context,
	repository pubbrew.Repository,
	branch pubbrew.BranchName,
	path pubbrew.FilePath,
	previous pubbrew.BlobSHA,
	content []byte,
	message string,
) error {
	if err := c.requireReady(ctx); err != nil {
		return err
	}

	options := &github.RepositoryContentFileOptions{
		Message: new(message),
		Content: content,
		Branch:  new(branch.String()),
	}
	var err error
	if previous == "" {
		_, _, err = c.github.Repositories.CreateFile(
			ctx,
			repository.Owner,
			repository.Name,
			path.String(),
			options,
		)
	} else {
		options.SHA = new(previous.String())
		_, _, err = c.github.Repositories.UpdateFile(
			ctx,
			repository.Owner,
			repository.Name,
			path.String(),
			options,
		)
	}
	if err != nil {
		return classify(err, "publication cask commit")
	}

	return nil
}

// CreatePullRequest implements [pubbrew.RepositoryWriter].
func (c *Client) CreatePullRequest(
	ctx context.Context,
	repository pubbrew.Repository,
	input pubbrew.PullRequestInput,
) (string, error) {
	if err := c.requireReady(ctx); err != nil {
		return "", err
	}

	pull, _, err := c.github.PullRequests.Create(
		ctx,
		repository.Owner,
		repository.Name,
		&github.NewPullRequest{
			Title:               new(input.Title),
			Head:                new(input.Head.String()),
			Base:                new(input.Base.String()),
			Body:                new(input.Body),
			MaintainerCanModify: new(false),
			Draft:               new(false),
		},
	)
	if err != nil {
		return "", classify(err, "publication pull request")
	}
	if pull == nil || pull.GetHTMLURL() == "" {
		return "", errors.New("created pull request URL is empty")
	}

	return pull.GetHTMLURL(), nil
}
