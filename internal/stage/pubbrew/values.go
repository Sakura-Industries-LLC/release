package pubbrew

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/meigma/release/internal/rel"
)

const (
	// caskPathPrefix is the only tap directory the publisher may change.
	caskPathPrefix = "Casks/"
	// caskPathSuffix is the required Ruby source suffix.
	caskPathSuffix = ".rb"
	// publicationBranchPrefix isolates publisher-owned branches.
	publicationBranchPrefix = "release/"
)

// Repository is a validated GitHub owner/name pair.
type Repository struct {
	// Owner is the repository owner or organization.
	Owner string
	// Name is the repository name.
	Name string
}

// ParseRepository constructs a [Repository] from owner/name.
func ParseRepository(value string) (Repository, error) {
	owner, name, ok := strings.Cut(value, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repository{}, fmt.Errorf("repository %q must be owner/name", value)
	}
	if !validRepositoryPart(owner) || !validRepositoryPart(name) {
		return Repository{}, fmt.Errorf("repository %q contains an invalid character", value)
	}

	return Repository{Owner: owner, Name: name}, nil
}

// String returns owner/name.
func (r Repository) String() string {
	return r.Owner + "/" + r.Name
}

// CaskToken is a safe Homebrew cask token and filename stem.
type CaskToken string

// ParseCaskToken constructs a [CaskToken] from a lowercase token.
func ParseCaskToken(value string) (CaskToken, error) {
	if value == "" {
		return "", fmt.Errorf("cask token %q is empty", value)
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		if character == '-' && index > 0 && index < len(value)-1 {
			continue
		}

		return "", fmt.Errorf("cask token %q must use lowercase letters, digits, and interior hyphens", value)
	}

	return CaskToken(value), nil
}

// String returns the cask token text.
func (t CaskToken) String() string {
	return string(t)
}

// Path returns the only tap path the publisher may change.
func (t CaskToken) Path() FilePath {
	return FilePath(caskPathPrefix + t.String() + caskPathSuffix)
}

// BranchName is a validated publisher branch name.
type BranchName string

// String returns the branch name text.
func (b BranchName) String() string {
	return string(b)
}

// CommitSHA identifies a Git commit.
type CommitSHA string

// String returns the commit SHA text.
func (s CommitSHA) String() string {
	return string(s)
}

// BlobSHA identifies a Git blob.
type BlobSHA string

// String returns the blob SHA text.
func (s BlobSHA) String() string {
	return string(s)
}

// FilePath is a repository-relative file path.
type FilePath string

// String returns the repository-relative path.
func (p FilePath) String() string {
	return string(p)
}

// ChangeStatus describes how one commit changed a path.
type ChangeStatus string

const (
	// ChangeAdded means the commit created a path.
	ChangeAdded ChangeStatus = "added"
	// ChangeModified means the commit replaced an existing path.
	ChangeModified ChangeStatus = "modified"
)

// File is one observed repository file.
type File struct {
	// Present reports whether the path exists at the observed ref.
	Present bool
	// Content is the decoded file body when Present is true.
	Content []byte
	// SHA is the blob object ID when Present is true.
	SHA BlobSHA
}

// ChangedFile is one path changed by a branch-head commit.
type ChangedFile struct {
	// Path is the repository-relative changed path.
	Path FilePath
	// Status is the GitHub change classification.
	Status ChangeStatus
}

// publicationBranch returns the deterministic branch for token and version.
func publicationBranch(token CaskToken, version rel.Version) BranchName {
	return BranchName(publicationBranchPrefix + token.String() + "/v" + version.String())
}

// validRepositoryPart reports whether a GitHub owner or repository segment is
// safe to pass to API adapters.
func validRepositoryPart(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			continue
		}

		return false
	}

	return true
}

// caskVersion reads the unique literal version declaration from generated
// Homebrew Ruby source.
func caskVersion(content []byte) (rel.Version, error) {
	var found string
	for line := range bytes.SplitSeq(content, []byte{'\n'}) {
		trimmed := strings.TrimSpace(string(line))
		value, ok := strings.CutPrefix(trimmed, "version ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return rel.Version{}, errorsVersionDeclaration(value)
		}
		value = value[1 : len(value)-1]
		if value == "" || strings.ContainsAny(value, "\"\\") {
			return rel.Version{}, errorsVersionDeclaration(value)
		}
		if found != "" {
			return rel.Version{}, errors.New("cask contains multiple version declarations")
		}
		found = value
	}
	if found == "" {
		return rel.Version{}, errors.New("cask has no literal version declaration")
	}

	version, err := rel.ParseVersion(found)
	if err != nil {
		return rel.Version{}, fmt.Errorf("cask version: %w", err)
	}

	return version, nil
}

// errorsVersionDeclaration returns the stable malformed-version diagnostic.
func errorsVersionDeclaration(value string) error {
	return fmt.Errorf("cask version declaration %q is not a literal string", value)
}
