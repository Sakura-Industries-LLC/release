package pkgrepo

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const (
	// publicationConfigLimit is the maximum reviewed YAML document size.
	publicationConfigLimit int64 = 64 * 1024
	// publicationConfigReadLimit reads one extra byte to distinguish an exact-size document.
	publicationConfigReadLimit = publicationConfigLimit + 1
)

// publicationConfigFile is the strict checked-in YAML representation.
type publicationConfigFile struct {
	// Channel is the only published repository channel.
	Channel string `yaml:"channel"`
	// Origin is the public HTTPS repository root.
	Origin string `yaml:"origin"`
	// Keys contains aggregate repository public keys.
	Keys aggregateKeysFile `yaml:"keys"`
	// Producers contains the reviewed source and package allowlist.
	Producers []producerFile `yaml:"producers"`
}

// aggregateKeysFile contains the three repository-level public keys.
type aggregateKeysFile struct {
	// APT verifies aggregate APT metadata.
	APT publicKeyFile `yaml:"apt"`
	// RPM verifies aggregate RPM metadata.
	RPM publicKeyFile `yaml:"rpm"`
	// APK verifies aggregate APK indexes.
	APK publicKeyFile `yaml:"apk"`
}

// publicKeyFile maps one local key path onto one published filename.
type publicKeyFile struct {
	// Source is relative to the configuration file's directory.
	Source string `yaml:"source"`
	// Published is the stable public key basename.
	Published string `yaml:"published"`
}

// producerFile is one reviewed producer YAML entry.
type producerFile struct {
	// Repository is the canonical producer owner/name.
	Repository string `yaml:"repository"`
	// Packages is the complete producer-owned package allowlist.
	Packages []string `yaml:"packages"`
	// ChecksumIdentity is the exact Cosign certificate identity that signs checksums.txt.
	ChecksumIdentity string `yaml:"checksum_identity"`
	// AttestationSigner is the GitHub workflow that attests release payloads.
	AttestationSigner string `yaml:"attestation_signer"`
	// RPMKey verifies producer RPM package signatures.
	RPMKey publicKeyFile `yaml:"rpm_key"`
	// APKKey verifies producer APK package signatures.
	APKKey publicKeyFile `yaml:"apk_key"`
}

// ParsePublicationConfig parses one strict, size-bounded reviewed YAML document.
func ParsePublicationConfig(reader io.Reader) (PublicationConfig, error) {
	if reader == nil {
		return PublicationConfig{}, errors.New("publication config reader is nil")
	}
	content, err := io.ReadAll(io.LimitReader(reader, publicationConfigReadLimit))
	if err != nil {
		return PublicationConfig{}, fmt.Errorf("read publication config: %w", err)
	}
	if int64(len(content)) > publicationConfigLimit {
		return PublicationConfig{}, fmt.Errorf("publication config exceeds %d bytes", publicationConfigLimit)
	}

	var file publicationConfigFile
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if decodeErr := decoder.Decode(&file); decodeErr != nil {
		return PublicationConfig{}, fmt.Errorf("decode publication config: %w", decodeErr)
	}
	var trailing any
	trailerErr := decoder.Decode(&trailing)
	if !errors.Is(trailerErr, io.EOF) {
		if trailerErr == nil {
			return PublicationConfig{}, errors.New("publication config contains multiple YAML documents")
		}
		return PublicationConfig{}, fmt.Errorf("decode publication config trailer: %w", trailerErr)
	}

	config, err := mapPublicationConfig(file)
	if err != nil {
		return PublicationConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return PublicationConfig{}, fmt.Errorf("validate publication config: %w", err)
	}

	return config, nil
}

// mapPublicationConfig maps the strict YAML representation onto domain values.
func mapPublicationConfig(file publicationConfigFile) (PublicationConfig, error) {
	config := PublicationConfig{
		Repository: Config{
			Channel: Channel(file.Channel),
			APTKey:  mapPublicKey(file.Keys.APT),
			RPMKey:  mapPublicKey(file.Keys.RPM),
			APKKey:  mapPublicKey(file.Keys.APK),
		},
		Origin: file.Origin,
	}
	config.Repository.Producers = make([]Producer, 0, len(file.Producers))
	config.Sources = make([]SourcePolicy, 0, len(file.Producers))
	for index, source := range file.Producers {
		repository, err := ParseRepository(source.Repository)
		if err != nil {
			return PublicationConfig{}, fmt.Errorf("producer %d repository: %w", index, err)
		}
		packages, err := mapPackageNames(source.Packages)
		if err != nil {
			return PublicationConfig{}, fmt.Errorf("producer %d: %w", index, err)
		}
		config.Repository.Producers = append(config.Repository.Producers, Producer{
			Repository: repository,
			Packages:   packages,
			RPMKey:     mapPublicKey(source.RPMKey),
			APKKey:     mapPublicKey(source.APKKey),
		})
		config.Sources = append(config.Sources, SourcePolicy{
			Repository:        repository,
			ChecksumIdentity:  ChecksumIdentity(source.ChecksumIdentity),
			AttestationSigner: AttestationSigner(source.AttestationSigner),
		})
	}

	return config, nil
}

// mapPackageNames parses one producer package allowlist in source order.
func mapPackageNames(values []string) ([]PackageName, error) {
	packages := make([]PackageName, 0, len(values))
	for _, value := range values {
		name, err := ParsePackageName(value)
		if err != nil {
			return nil, err
		}
		packages = append(packages, name)
	}

	return packages, nil
}

// mapPublicKey maps one YAML key value without performing validation.
func mapPublicKey(value publicKeyFile) PublicKey {
	return PublicKey(value)
}
