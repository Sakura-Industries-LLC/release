package ghrel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// uploadedAssetState is the only complete GitHub Release asset state.
	uploadedAssetState = "uploaded"
	// maxTagIndirection bounds annotated tags that point to another annotated tag.
	maxTagIndirection = 8
	// downloadedFileMode is the normalized release asset mode.
	downloadedFileMode = 0o644
)

// fullCommitPattern accepts one full lowercase source commit SHA.
var fullCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// sourceAsset is one validated GitHub asset pending download.
type sourceAsset struct {
	// id is the GitHub release asset identifier.
	id int64
	// name is the validated flat release asset name.
	name stage.AssetName
	// digest is the GitHub-advertised SHA-256 digest.
	digest rel.Digest
	// size is the GitHub-advertised content length.
	size int64
}

// Fetch implements [pkgrepo.ReleaseSource].
//
// It requires one exact published, non-prerelease tag, paginates the complete
// asset list, rejects duplicate or incomplete asset metadata, resolves the tag
// ref to its final commit, and downloads every asset into destination. Each
// download is streamed through SHA-256 and must match both GitHub's digest and
// size before Fetch succeeds.
func (c *Client) Fetch(
	ctx context.Context,
	request pkgrepo.ReleaseRequest,
	destination *os.Root,
) (pkgrepo.Release, error) {
	if err := c.requireReady(ctx); err != nil {
		return pkgrepo.Release{}, err
	}
	if destination == nil {
		return pkgrepo.Release{}, errors.New("release destination is nil")
	}
	repository, tag, err := validateSourceRequest(request)
	if err != nil {
		return pkgrepo.Release{}, err
	}
	owner, name := splitRepository(repository)

	release, _, err := c.github.Repositories.GetReleaseByTag(ctx, owner, name, tag)
	if err != nil {
		return pkgrepo.Release{}, classify(err)
	}
	if release == nil {
		return pkgrepo.Release{}, errors.New("GitHub returned a nil release")
	}
	if release.GetTagName() != tag {
		return pkgrepo.Release{}, fmt.Errorf("GitHub release tag %q does not match %q", release.GetTagName(), tag)
	}
	if release.GetDraft() {
		return pkgrepo.Release{}, fmt.Errorf("GitHub release %s is still a draft", tag)
	}
	if release.GetPrerelease() {
		return pkgrepo.Release{}, fmt.Errorf("GitHub release %s is a prerelease", tag)
	}
	if release.GetID() <= 0 {
		return pkgrepo.Release{}, fmt.Errorf("GitHub release %s has no ID", tag)
	}
	publishedAt := release.GetPublishedAt().Time
	if publishedAt.IsZero() {
		return pkgrepo.Release{}, fmt.Errorf("GitHub release %s has no publication time", tag)
	}

	assets, err := c.listSourceAssets(ctx, owner, name, release.GetID())
	if err != nil {
		return pkgrepo.Release{}, err
	}
	commit, err := c.resolveTagCommit(ctx, owner, name, tag)
	if err != nil {
		return pkgrepo.Release{}, err
	}
	downloaded := make([]pkgrepo.ReleaseAsset, 0, len(assets))
	for _, asset := range assets {
		if err := c.downloadSourceAsset(ctx, owner, name, destination, asset); err != nil {
			return pkgrepo.Release{}, err
		}
		downloaded = append(downloaded, pkgrepo.ReleaseAsset{
			Name:   asset.name,
			Path:   asset.name.String(),
			Digest: asset.digest,
			Size:   asset.size,
		})
	}

	return pkgrepo.Release{
		Repository:  repository,
		Tag:         tag,
		Commit:      commit,
		PublishedAt: publishedAt,
		Assets:      downloaded,
	}, nil
}

// validateSourceRequest parses the strict repository and stable tag request.
func validateSourceRequest(request pkgrepo.ReleaseRequest) (pkgrepo.Repository, string, error) {
	repository, err := pkgrepo.ParseRepository(string(request.Repository))
	if err != nil {
		return "", "", err
	}
	if _, err := pkgrepo.ParseTag(request.Tag); err != nil {
		return "", "", err
	}

	return repository, request.Tag, nil
}

// splitRepository returns the owner and name from one validated repository.
func splitRepository(repository pkgrepo.Repository) (string, string) {
	owner, name, _ := strings.Cut(string(repository), "/")

	return owner, name
}

// listSourceAssets paginates and validates every asset before any download starts.
func (c *Client) listSourceAssets(
	ctx context.Context,
	owner string,
	repository string,
	releaseID int64,
) ([]sourceAsset, error) {
	assets := make([]sourceAsset, 0)
	seen := make(map[stage.AssetName]struct{})
	options := &github.ListOptions{PerPage: listPageSize}
	for {
		page, response, err := c.github.Repositories.ListReleaseAssets(ctx, owner, repository, releaseID, options)
		if err != nil {
			return nil, classify(err)
		}
		for _, asset := range page {
			mapped, err := mapSourceAsset(asset)
			if err != nil {
				return nil, err
			}
			if _, duplicate := seen[mapped.name]; duplicate {
				return nil, fmt.Errorf("GitHub release asset %q is duplicated", mapped.name)
			}
			seen[mapped.name] = struct{}{}
			assets = append(assets, mapped)
		}
		if response == nil || response.NextPage == 0 {
			break
		}
		options.Page = response.NextPage
	}
	if len(assets) == 0 {
		return nil, errors.New("GitHub release has no assets")
	}
	sort.Slice(assets, func(left, right int) bool { return assets[left].name.String() < assets[right].name.String() })

	return assets, nil
}

// mapSourceAsset validates one GitHub release asset transport record.
func mapSourceAsset(asset *github.ReleaseAsset) (sourceAsset, error) {
	if asset == nil {
		return sourceAsset{}, errors.New("GitHub returned a nil release asset")
	}
	name, err := stage.ParseAssetName(asset.GetName())
	if err != nil {
		return sourceAsset{}, fmt.Errorf("GitHub release asset name: %w", err)
	}
	if asset.GetID() <= 0 {
		return sourceAsset{}, fmt.Errorf("GitHub release asset %q has no ID", name)
	}
	if asset.GetState() != uploadedAssetState {
		return sourceAsset{}, fmt.Errorf(
			"GitHub release asset %q state %q, want %q",
			name,
			asset.GetState(),
			uploadedAssetState,
		)
	}
	if asset.GetSize() < 0 {
		return sourceAsset{}, fmt.Errorf("GitHub release asset %q has negative size %d", name, asset.GetSize())
	}
	digest, err := rel.ParseDigest(asset.GetDigest())
	if err != nil {
		return sourceAsset{}, fmt.Errorf("GitHub release asset %q digest: %w", name, err)
	}

	return sourceAsset{
		id:     asset.GetID(),
		name:   name,
		digest: digest,
		size:   int64(asset.GetSize()),
	}, nil
}

// resolveTagCommit follows one lightweight or annotated tag to a commit SHA.
func (c *Client) resolveTagCommit(ctx context.Context, owner, repository, tag string) (string, error) {
	reference, _, err := c.github.Git.GetRef(ctx, owner, repository, "tags/"+tag)
	if err != nil {
		return "", classify(err)
	}
	if reference == nil || reference.GetObject() == nil {
		return "", fmt.Errorf("GitHub tag %s has no object", tag)
	}
	object := reference.GetObject()
	for range maxTagIndirection {
		switch object.GetType() {
		case "commit":
			if !fullCommitPattern.MatchString(object.GetSHA()) {
				return "", fmt.Errorf("GitHub tag %s commit %q is not a full lowercase SHA", tag, object.GetSHA())
			}
			return object.GetSHA(), nil
		case "tag":
			annotated, _, err := c.github.Git.GetTag(ctx, owner, repository, object.GetSHA())
			if err != nil {
				return "", classify(err)
			}
			if annotated == nil || annotated.GetObject() == nil {
				return "", fmt.Errorf("GitHub annotated tag %s has no target", tag)
			}
			object = annotated.GetObject()
		default:
			return "", fmt.Errorf("GitHub tag %s points to unsupported object type %q", tag, object.GetType())
		}
	}

	return "", fmt.Errorf("GitHub tag %s exceeds %d levels of indirection", tag, maxTagIndirection)
}

// downloadSourceAsset streams one asset to a new confined regular file.
func (c *Client) downloadSourceAsset(
	ctx context.Context,
	owner string,
	repository string,
	destination *os.Root,
	asset sourceAsset,
) error {
	body, redirect, err := c.github.Repositories.DownloadReleaseAsset(
		ctx,
		owner,
		repository,
		asset.id,
		http.DefaultClient,
	)
	if err != nil {
		return classify(err)
	}
	if body == nil || redirect != "" {
		if body != nil {
			_ = body.Close()
		}
		return fmt.Errorf("GitHub asset %q download returned an unresolved redirect", asset.name)
	}
	defer body.Close()

	output, err := destination.OpenFile(asset.name.String(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, downloadedFileMode)
	if err != nil {
		return fmt.Errorf("create release asset %s: %w", asset.name, err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hasher), body)
	chmodErr := output.Chmod(downloadedFileMode)
	closeErr := output.Close()
	if copyErr != nil {
		_ = destination.Remove(asset.name.String())
		return fmt.Errorf("download release asset %s: %w", asset.name, copyErr)
	}
	if chmodErr != nil {
		_ = destination.Remove(asset.name.String())
		return fmt.Errorf("chmod release asset %s: %w", asset.name, chmodErr)
	}
	if closeErr != nil {
		_ = destination.Remove(asset.name.String())
		return fmt.Errorf("close release asset %s: %w", asset.name, closeErr)
	}
	if written != asset.size {
		_ = destination.Remove(asset.name.String())
		return fmt.Errorf("release asset %q downloaded %d bytes, want %d", asset.name, written, asset.size)
	}
	digest, err := rel.ParseDigest("sha256:" + hex.EncodeToString(hasher.Sum(nil)))
	if err != nil {
		_ = destination.Remove(asset.name.String())
		return fmt.Errorf("parse downloaded asset digest %s: %w", asset.name, err)
	}
	if digest != asset.digest {
		_ = destination.Remove(asset.name.String())
		return fmt.Errorf("release asset %q has digest %s, GitHub reports %s", asset.name, digest, asset.digest)
	}

	return nil
}
