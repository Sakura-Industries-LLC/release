package pubscoop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/meigma/release/internal/rel"
)

const (
	// manifestPathSuffix is the required Scoop manifest suffix.
	manifestPathSuffix = ".json"
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

// ManifestName is a safe Scoop manifest name and filename stem.
type ManifestName string

// ParseManifestName constructs a [ManifestName] from a lowercase name.
func ParseManifestName(value string) (ManifestName, error) {
	if value == "" {
		return "", fmt.Errorf("manifest name %q is empty", value)
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		if character == '-' && index > 0 && index < len(value)-1 {
			continue
		}

		return "", fmt.Errorf("manifest name %q must use lowercase letters, digits, and interior hyphens", value)
	}

	return ManifestName(value), nil
}

// String returns the manifest name text.
func (n ManifestName) String() string {
	return string(n)
}

// Path returns the only bucket path the publisher may change.
func (n ManifestName) Path() FilePath {
	return FilePath(n.String() + manifestPathSuffix)
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

// publicationBranch returns the deterministic branch for name and version.
func publicationBranch(name ManifestName, version rel.Version) BranchName {
	return BranchName(publicationBranchPrefix + name.String() + "/v" + version.String())
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

// manifestVersion reads the unique string version field from generated Scoop
// JSON.
func manifestVersion(content []byte) (rel.Version, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(content, &payload); err != nil {
		return rel.Version{}, fmt.Errorf("manifest JSON is malformed: %w", err)
	}
	raw, ok := payload["version"]
	if !ok {
		return rel.Version{}, errorsVersionMissing()
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return rel.Version{}, errorsVersionDeclaration(string(raw))
	}
	if value == "" {
		return rel.Version{}, errorsVersionDeclaration(value)
	}

	version, err := rel.ParseVersion(value)
	if err != nil {
		return rel.Version{}, fmt.Errorf("manifest version: %w", err)
	}

	return version, nil
}

// errorsVersionMissing returns the stable absent-version diagnostic.
func errorsVersionMissing() error {
	return errors.New("manifest has no version")
}

// errorsVersionDeclaration returns the stable malformed-version diagnostic.
func errorsVersionDeclaration(value string) error {
	return fmt.Errorf("manifest version %q is not a string", value)
}
