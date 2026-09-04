package pubobj

import (
	"fmt"
	"regexp"

	"github.com/Sakura-Industries-LLC/release/internal/rel"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pubgh"
)

const (
	// PublishStatePublished reports that at least one object was written.
	PublishStatePublished PublishState = "published"
	// PublishStateUnchanged reports that every requested object already matched.
	PublishStateUnchanged PublishState = "unchanged"

	// projectPattern accepts one lowercase object-store project name.
	projectPattern = `^[a-z0-9][a-z0-9._-]{0,63}$`
)

// PublishState is the converged remote object-store state.
type PublishState string

// Project is the first object-key segment.
//
// The only constructor is [ParseProject]. The zero value is invalid.
type Project string

// Prefix is the `<project>/<vTag>` key namespace for one release.
type Prefix string

// PublishInput is the closed input to [Publish].
type PublishInput struct {
	// Project is the first object-key segment.
	Project Project
	// Tag is the git tag whose version forms the second key segment.
	Tag rel.GitTag
	// Expected is the closed bundle rebuilt from the distribution directory.
	Expected pubgh.Bundle
	// Assets are the local paths to upload, in [pubgh.Bundle.Names] order.
	Assets []pubgh.AssetPath
}

// PublishResult is the JSON document produced by a successful [Publish].
type PublishResult struct {
	// Project is the first object-key segment.
	Project Project `json:"project"`
	// Tag is the git tag bound to the published prefix.
	Tag rel.GitTag `json:"tag"`
	// Prefix is the `<project>/<vTag>` key namespace.
	Prefix Prefix `json:"prefix"`
	// Uploaded are bundle names written during this invocation, in bundle order.
	Uploaded []string `json:"uploaded"`
	// Unchanged are bundle names whose stored digest already matched, in bundle order.
	Unchanged []string `json:"unchanged"`
	// State is published when at least one object was written and unchanged otherwise.
	State PublishState `json:"state"`
}

// ParseProject validates and constructs a [Project].
func ParseProject(value string) (Project, error) {
	if !regexp.MustCompile(projectPattern).MatchString(value) {
		return "", fmt.Errorf("project %q is invalid", value)
	}

	return Project(value), nil
}

// String returns the project name.
func (p Project) String() string {
	return string(p)
}

// String returns the key prefix.
func (p Prefix) String() string {
	return string(p)
}

// newPrefix derives `<project>/v<version>` from a validated project and tag.
func newPrefix(project Project, tag rel.GitTag) (Prefix, error) {
	version, err := tag.Version()
	if err != nil {
		return "", fmt.Errorf("git tag %q version: %w", tag, err)
	}

	return Prefix(project.String() + "/v" + version.String()), nil
}
