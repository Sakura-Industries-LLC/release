package pkgrepo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validChecksumIdentity  = "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@0123456789abcdef0123456789abcdef01234567"
	validAttestationSigner = "meigma/release/.github/workflows/publish-github-release.yml"
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
			name: "accepted cross-repository signer",
			input: strings.Replace(
				validPublicationConfig(),
				"repository: meigma/release",
				"repository: acme/app",
				1,
			),
		},
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
			name: "invalid checksum identity",
			input: strings.Replace(
				validPublicationConfig(),
				validChecksumIdentity,
				"https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@refs/tags/v1.2.3",
				1,
			),
			wantErr: "checksum identity",
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
			require.Len(t, got.Sources, 1)
			assert.Equal(t, ChecksumIdentity(validChecksumIdentity), got.Sources[0].ChecksumIdentity)
			assert.Equal(t, AttestationSigner(validAttestationSigner), got.Sources[0].AttestationSigner)
			if strings.Contains(test.name, "cross-repository") {
				assert.Equal(t, Repository("acme/app"), got.Repository.Producers[0].Repository)
				assert.Equal(t, Repository("acme/app"), got.Sources[0].Repository)
				return
			}
			assert.Equal(t, Repository("meigma/release"), got.Repository.Producers[0].Repository)
			assert.Equal(t, []PackageName{"release-cli"}, got.Repository.Producers[0].Packages)
		})
	}
}

func TestParseChecksumIdentityRejectsNonImmutableValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "tag ref",
			input:   "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@refs/tags/v1.2.3",
			wantErr: "full lowercase commit SHA",
		},
		{
			name:    "branch ref",
			input:   "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@main",
			wantErr: "full lowercase commit SHA",
		},
		{
			name:    "missing ref",
			input:   "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml",
			wantErr: "immutable commit SHA",
		},
		{
			name:    "short SHA",
			input:   "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@0123456789abcdef01234567",
			wantErr: "full lowercase commit SHA",
		},
		{
			name:    "relative identity",
			input:   ".github/workflows/go-pre-publish.yml@0123456789abcdef0123456789abcdef01234567",
			wantErr: "absolute HTTPS URL",
		},
		{
			name:    "non-GitHub host",
			input:   "https://gitlab.com/meigma/release/.github/workflows/go-pre-publish.yml@0123456789abcdef0123456789abcdef01234567",
			wantErr: "host github.com",
		},
		{
			name:    "credentials",
			input:   "https://user:token@github.com/meigma/release/.github/workflows/go-pre-publish.yml@0123456789abcdef0123456789abcdef01234567",
			wantErr: "credentials",
		},
		{
			name:    "query",
			input:   validChecksumIdentity + "?ref=main",
			wantErr: "query",
		},
		{
			name:    "fragment",
			input:   validChecksumIdentity + "#section",
			wantErr: "fragment",
		},
		{
			name:    "uppercase owner",
			input:   "https://github.com/Meigma/release/.github/workflows/go-pre-publish.yml@0123456789abcdef0123456789abcdef01234567",
			wantErr: "lowercase owner/name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseChecksumIdentity(test.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestParseAttestationSignerRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "relative workflow",
			input:   ".github/workflows/publish-github-release.yml",
			wantErr: "owner/repository/.github/workflows/<file>",
		},
		{
			name:    "URL identity",
			input:   "https://github.com/meigma/release/.github/workflows/publish-github-release.yml",
			wantErr: "owner/repository/.github/workflows/<file>",
		},
		{
			name:    "pinned ref",
			input:   "meigma/release/.github/workflows/publish-github-release.yml@0123456789abcdef0123456789abcdef01234567",
			wantErr: "owner/repository/.github/workflows/<file>",
		},
		{
			name:    "uppercase owner",
			input:   "Meigma/release/.github/workflows/publish-github-release.yml",
			wantErr: "lowercase owner/name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseAttestationSigner(test.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestSourcePolicyKeepsExplicitIdentities(t *testing.T) {
	t.Parallel()

	policy := SourcePolicy{
		Repository:        "acme/app",
		ChecksumIdentity:  ChecksumIdentity(validChecksumIdentity),
		AttestationSigner: AttestationSigner(validAttestationSigner),
	}

	require.NoError(t, policy.Validate())
	assert.Equal(t, ChecksumIdentity(validChecksumIdentity), policy.ChecksumIdentity)
	assert.Equal(t, AttestationSigner(validAttestationSigner), policy.AttestationSigner)
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
			path:   "rpm/stable/x86_64/packages/release-cli-1.2.3-1.x86_64.rpm",
			format: FormatRPM,
			ok:     true,
		},
		{name: "APK package", path: "apk/stable/main/aarch64/release-cli-1.2.3.apk", format: FormatAPK, ok: true},
		{name: "wrong channel", path: "rpm/testing/x86_64/packages/release-cli.rpm"},
		{name: "legacy uppercase RPM path", path: "rpm/stable/x86_64/Packages/release-cli.rpm"},
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
    checksum_identity: ` + validChecksumIdentity + `
    attestation_signer: ` + validAttestationSigner + `
    rpm_key:
      source: keys/release-rpm.asc
      published: release-rpm-001.asc
    apk_key:
      source: keys/release-apk.rsa.pub
      published: release-apk-001.rsa.pub
`
}
