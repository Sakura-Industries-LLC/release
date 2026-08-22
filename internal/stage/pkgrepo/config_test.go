package pkgrepo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePublicationConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "accepted reviewed policy", input: validPublicationConfig()},
		{
			name:    "unknown field",
			input:   strings.Replace(validPublicationConfig(), "origin:", "unknown: value\norigin:", 1),
			wantErr: "field unknown not found",
		},
		{
			name:    "multiple documents",
			input:   validPublicationConfig() + "---\nchannel: stable\n",
			wantErr: "multiple YAML documents",
		},
		{
			name:    "non-HTTPS origin",
			input:   strings.Replace(validPublicationConfig(), "https://pkgs.meigma.dev", "http://pkgs.meigma.dev", 1),
			wantErr: "absolute HTTPS URL",
		},
		{
			name: "origin path prefix",
			input: strings.Replace(
				validPublicationConfig(),
				"https://pkgs.meigma.dev",
				"https://pkgs.meigma.dev/repository",
				1,
			),
			wantErr: "path prefix",
		},
		{
			name: "invalid checksum workflow",
			input: strings.Replace(
				validPublicationConfig(),
				".github/workflows/go-pre-publish.yml",
				"scripts/publish.yml",
				1,
			),
			wantErr: "checksum workflow",
		},
		{
			name:    "invalid repository",
			input:   strings.Replace(validPublicationConfig(), "meigma/release", "Meigma/release", 1),
			wantErr: "repository",
		},
		{
			name:    "duplicate producer",
			input:   strings.Replace(validPublicationConfig(), "producers:\n", "producers:\n"+validProducerYAML(), 1),
			wantErr: "repository \"meigma/release\" is duplicated",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePublicationConfig(strings.NewReader(test.input))
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, ChannelStable, got.Repository.Channel)
			assert.Equal(t, "https://pkgs.meigma.dev", got.Origin)
			require.Len(t, got.Repository.Producers, 1)
			assert.Equal(t, Repository("meigma/release"), got.Repository.Producers[0].Repository)
			assert.Equal(t, []PackageName{"release-cli"}, got.Repository.Producers[0].Packages)
			require.Len(t, got.Sources, 1)
			assert.Equal(t, ".github/workflows/go-pre-publish.yml", got.Sources[0].ChecksumWorkflow)
		})
	}
}

func TestSourcePolicyDerivesExactIdentities(t *testing.T) {
	t.Parallel()

	policy := SourcePolicy{
		Repository:          "meigma/release",
		ChecksumWorkflow:    ".github/workflows/go-pre-publish.yml",
		AttestationWorkflow: ".github/workflows/publish-github-release.yml",
	}

	identity, err := policy.ChecksumIdentity("v1.2.3")
	require.NoError(t, err)
	assert.Equal(
		t,
		"https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@refs/tags/v1.2.3",
		identity,
	)
	assert.Equal(
		t,
		"meigma/release/.github/workflows/publish-github-release.yml",
		policy.AttestationSigner(),
	)
}

func TestPackageObjectFormatAcceptsOnlyCanonicalTrees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		path   string
		format Format
		ok     bool
	}{
		{
			name:   "APT package",
			path:   "apt/pool/main/r/release-cli/release-cli_1.2.3_amd64.deb",
			format: FormatDEB,
			ok:     true,
		},
		{
			name:   "RPM package",
			path:   "rpm/stable/x86_64/Packages/release-cli-1.2.3-1.x86_64.rpm",
			format: FormatRPM,
			ok:     true,
		},
		{name: "APK package", path: "apk/stable/main/aarch64/release-cli-1.2.3.apk", format: FormatAPK, ok: true},
		{name: "wrong channel", path: "rpm/testing/x86_64/Packages/release-cli.rpm"},
		{name: "metadata suffix", path: "rpm/stable/x86_64/repodata/primary.xml.gz"},
		{name: "traversal", path: "../apt/pool/main/release-cli.deb"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			format, ok := packageObjectFormat(test.path, ChannelStable)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.format, format)
		})
	}
}

// validPublicationConfig returns one complete reviewed YAML policy.
func validPublicationConfig() string {
	return `channel: stable
origin: https://pkgs.meigma.dev
keys:
  apt:
    source: keys/repository-apt.asc
    published: apt-repository-001.asc
  rpm:
    source: keys/repository-rpm.asc
    published: rpm-repository-001.asc
  apk:
    source: keys/repository-apk.rsa.pub
    published: apk-index-001.rsa.pub
producers:
` + validProducerYAML()
}

// validProducerYAML returns one producer entry with the expected indentation.
func validProducerYAML() string {
	return `  - repository: meigma/release
    packages:
      - release-cli
    checksum_workflow: .github/workflows/go-pre-publish.yml
    attestation_workflow: .github/workflows/publish-github-release.yml
    rpm_key:
      source: keys/release-rpm.asc
      published: release-rpm-001.asc
    apk_key:
      source: keys/release-apk.rsa.pub
      published: release-apk-001.rsa.pub
`
}
