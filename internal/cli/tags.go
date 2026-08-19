package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// commandPlanTags is the envelope command path for plan tags.
	commandPlanTags = "plan tags"
	// flagImage is the plan-tags image flag name.
	flagImage = "image"
	// flagVersion is the plan-tags version flag name.
	flagVersion = "version"
	// envRefName is the Actions git-ref name used as a version default.
	envRefName = "GITHUB_REF_NAME"
	// envActor is the Actions actor used as a registry username.
	envActor = "GITHUB_ACTOR"
	// defaultImageRegistry is the registry host used when deriving --image.
	defaultImageRegistry = "ghcr.io"
	// defaultRegistryUser is the GHCR username used when a token is present
	// but GITHUB_ACTOR is unset.
	defaultRegistryUser = "x-access-token"
)

// PlanTagsResult is the --json payload for plan tags.
type PlanTagsResult struct {
	// Image is the OCI image whose tags were inspected.
	Image string `json:"image"`
	// Version is the candidate stable release version.
	Version string `json:"version"`
	// Digest is the candidate OCI index digest.
	Digest string `json:"digest"`
	// Tags are the tags with a create decision, in decision order.
	Tags []string `json:"tags"`
	// Decisions are the exact tag and each channel, in policy order.
	Decisions []TagDecisionResult `json:"decisions"`
}

// TagDecisionResult is one planned tag action in a [PlanTagsResult].
type TagDecisionResult struct {
	// Tag is the exact or channel tag that was evaluated.
	Tag string `json:"tag"`
	// Scope is the tag scope: exact, minor, major, or latest.
	Scope string `json:"scope"`
	// Action is the planned outcome: create, accept, or retain.
	Action string `json:"action"`
}

// RegistryCredentials is the resolved registry username and password.
//
// An empty Password selects an anonymous read. Username is meaningful only
// when Password is set.
type RegistryCredentials struct {
	// Username is GITHUB_ACTOR, or x-access-token when a token is present.
	Username string
	// Password is the token from GITHUB_TOKEN or GH_TOKEN.
	Password rel.Secret
}

// newPlanCommand constructs the plan parent verb.
func newPlanCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Plan release publication steps",
		Args:  requireSubcommand,
		RunE: func(_ *cobra.Command, _ []string) error {
			return UsageError(errors.New("a plan subcommand is required"))
		},
	}
	cmd.AddCommand(newTagsCommand(options))

	return cmd
}

// newTagsCommand constructs the plan tags verb.
func newTagsCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tags",
		Short: "Plan immutable exact tags and moving channel tags",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlanTags(cmd, options)
		},
	}
	cmd.Flags().String(flagImage, "", "OCI image name without a tag or digest")
	cmd.Flags().String(flagVersion, "", "stable MAJOR.MINOR.PATCH version")
	cmd.Flags().String(flagDigest, "", "candidate OCI index digest")

	return cmd
}

// runPlanTags validates configuration and plans immutable release tags.
//
// Missing or malformed configuration is [ErrUsage] and is raised before the
// registry port is constructed or called. A planning or registry failure is
// returned as a command failure. Success without --json writes nothing.
func runPlanTags(cmd *cobra.Command, options Options) error {
	expected, err := resolveTags(options)
	if err != nil {
		return writeCommandResult(options, commandPlanTags, nil, UsageError(err))
	}

	reader, err := stateReader(options, expected.Credentials)
	if err != nil {
		return writeCommandResult(options, commandPlanTags, nil, err)
	}

	plan, err := puboci.PlanTags(
		cmd.Context(),
		reader,
		expected.Image,
		expected.Version,
		expected.Digest,
	)
	if err != nil {
		return writeCommandResult(options, commandPlanTags, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandPlanTags, planTagsResult(expected, plan), nil)
}

// tagsConfig is the resolved plan-tags configuration.
type tagsConfig struct {
	// Image is the untagged repository to inspect.
	Image puboci.Image
	// Version is the candidate stable release version.
	Version rel.Version
	// Digest is the candidate image digest.
	Digest rel.Digest
	// Credentials authenticates registry reads. An empty password is anonymous.
	Credentials RegistryCredentials
}

// resolveTags parses flags and Actions environment into a plan-tags config.
//
// It performs no network I/O.
func resolveTags(options Options) (tagsConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}

	digest, err := resolvePlanDigest(settings)
	if err != nil {
		return tagsConfig{}, err
	}
	image, err := resolvePlanImage(settings, options.LookupEnv)
	if err != nil {
		return tagsConfig{}, err
	}
	version, err := resolvePlanVersion(settings, options.LookupEnv)
	if err != nil {
		return tagsConfig{}, err
	}

	return tagsConfig{
		Image:       image,
		Version:     version,
		Digest:      digest,
		Credentials: resolveRegistryCredentials(options.LookupEnv),
	}, nil
}

// resolvePlanDigest requires and parses --digest / RELEASE_DIGEST.
func resolvePlanDigest(settings Settings) (rel.Digest, error) {
	if settings.Digest == "" {
		return "", fmt.Errorf("--%s is required", flagDigest)
	}

	return rel.ParseDigest(settings.Digest)
}

// resolvePlanImage returns --image / RELEASE_IMAGE, or a derived GHCR name.
func resolvePlanImage(settings Settings, lookup LookupEnv) (puboci.Image, error) {
	raw := settings.Image
	if raw == "" {
		derived, err := deriveImage(lookup)
		if err != nil {
			return "", err
		}
		raw = derived
	}

	return puboci.ParseImage(raw)
}

// resolvePlanVersion returns --version / RELEASE_VERSION, or GITHUB_REF_NAME.
func resolvePlanVersion(settings Settings, lookup LookupEnv) (rel.Version, error) {
	raw := settings.Version
	if raw == "" {
		derived, err := deriveVersion(lookup)
		if err != nil {
			return rel.Version{}, err
		}
		raw = derived
	}

	return rel.ParseVersion(raw)
}

// deriveImage builds ghcr.io/<owner>/<repo> from GITHUB_REPOSITORY.
func deriveImage(lookup LookupEnv) (string, error) {
	if lookup == nil {
		return "", fmt.Errorf("--%s is required when %s is unset", flagImage, envRepository)
	}
	repository, ok := lookup(envRepository)
	if !ok || repository == "" {
		return "", fmt.Errorf("--%s is required when %s is unset", flagImage, envRepository)
	}

	return strings.ToLower(defaultImageRegistry + "/" + repository), nil
}

// deriveVersion reads GITHUB_REF_NAME and strips one optional leading v.
func deriveVersion(lookup LookupEnv) (string, error) {
	if lookup == nil {
		return "", fmt.Errorf("--%s is required when %s is unset", flagVersion, envRefName)
	}
	refName, ok := lookup(envRefName)
	if !ok || refName == "" {
		return "", fmt.Errorf("--%s is required when %s is unset", flagVersion, envRefName)
	}

	return strings.TrimPrefix(refName, "v"), nil
}

// resolveRegistryCredentials reads the optional Actions registry token.
//
// A missing token yields empty credentials and is not an error.
func resolveRegistryCredentials(lookup LookupEnv) RegistryCredentials {
	token := resolveToken(lookup)
	if token == "" {
		return RegistryCredentials{}
	}

	username := defaultRegistryUser
	if lookup != nil {
		if value, ok := lookup(envActor); ok && value != "" {
			username = value
		}
	}

	return RegistryCredentials{
		Username: username,
		Password: rel.NewSecret(token),
	}
}

// stateReader returns the injected port or constructs one from credentials.
func stateReader(options Options, credentials RegistryCredentials) (puboci.StateReader, error) {
	if options.StateReader != nil {
		return options.StateReader, nil
	}
	if options.NewStateReader == nil {
		return nil, errors.New("state reader factory is not configured")
	}

	reader, err := options.NewStateReader(credentials)
	if err != nil {
		return nil, UsageError(fmt.Errorf("registry client: %w", err))
	}
	if reader == nil {
		return nil, errors.New("state reader factory returned nil")
	}

	return reader, nil
}

// planTagsResult builds the success envelope payload.
func planTagsResult(expected tagsConfig, plan rel.TagPlan) PlanTagsResult {
	applied := plan.Apply()
	result := PlanTagsResult{
		Image:     expected.Image.String(),
		Version:   expected.Version.String(),
		Digest:    expected.Digest.String(),
		Tags:      make([]string, 0, len(applied)),
		Decisions: make([]TagDecisionResult, 0, len(plan.Decisions)),
	}
	for _, tag := range applied {
		result.Tags = append(result.Tags, tag.String())
	}
	for _, decision := range plan.Decisions {
		result.Decisions = append(result.Decisions, TagDecisionResult{
			Tag:    decision.Tag.String(),
			Scope:  string(decision.Scope),
			Action: string(decision.Action),
		})
	}

	return result
}
