package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakura-Industries-LLC/release/internal/profile/goprof"
	"github.com/Sakura-Industries-LLC/release/internal/stage"
)

const (
	// profileGo is the only accepted --profile value in this slice.
	profileGo = "go"
	// flagProfile is the stage profile flag name.
	flagProfile = "profile"
	// flagDist is the stage dist-directory flag name.
	flagDist = "dist"
	// octalBase formats file modes as octal strings.
	octalBase = 8
	// envNativePackageSigning selects which native package formats are signed.
	envNativePackageSigning = "RELEASE_NATIVE_PACKAGE_SIGNING"
	// formatRPM selects RPM package signing.
	formatRPM = "rpm"
	// formatAPK selects APK package signing.
	formatAPK = "apk"
	// envRPMSigningKeyFile is the nFPM RPM private-key path.
	envRPMSigningKeyFile = "RELEASE_RPM_SIGNING_KEY_FILE"
	// envAPKSigningKeyFile is the nFPM APK private-key path.
	envAPKSigningKeyFile = "RELEASE_APK_SIGNING_KEY_FILE"
	// envRPMPassphrase is the standard GoReleaser nFPM RPM passphrase.
	envRPMPassphrase = "NFPM_RELEASE_RPM_PASSPHRASE" // #nosec G101 -- environment variable name, not a credential
	// envAPKPassphrase is the standard GoReleaser nFPM APK passphrase.
	envAPKPassphrase = "NFPM_RELEASE_APK_PASSPHRASE" // #nosec G101 -- environment variable name, not a credential
)

// nativeSigning contains the validated nFPM signing environment. A format the
// selection leaves out keeps empty values, which is how nFPM is told to leave
// that format unsigned.
type nativeSigning struct {
	// rpmKeyFile is the RPM private-key path.
	rpmKeyFile string
	// apkKeyFile is the APK private-key path.
	apkKeyFile string
	// rpmPassphrase decrypts the RPM private key.
	rpmPassphrase string
	// apkPassphrase decrypts the APK private key.
	apkPassphrase string
}

// nativeFormat is one selectable native package format and the environment
// that signs it.
type nativeFormat struct {
	// name is the value that selects this format.
	name string
	// keyFileEnv names the private-key path variable.
	keyFileEnv string
	// passphraseEnv names the passphrase variable nFPM reads.
	passphraseEnv string
	// keyFile receives the validated private-key path.
	keyFile *string
	// passphrase receives the validated passphrase.
	passphrase *string
}

// newStageCommand constructs the stage verb.
func newStageCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stage",
		Short: "Build and verify a profile-specific dist directory",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStage(cmd.Context(), options)
		},
	}
	cmd.Flags().String(flagProfile, "", "release profile (only go is supported)")
	cmd.Flags().String(flagDist, "", "path to the GoReleaser dist directory")

	return cmd
}

// runStage builds the Go profile dist directory with GoReleaser, then verifies it.
//
// An unusable native-signing selection, a missing or unknown --profile, a
// missing --dist, and a --dist that is not a basename are [ErrUsage] and are
// raised before GoReleaser runs. A GoReleaser failure is a command
// failure. GoReleaser progress and diagnostics are written to stderr so
// --json stdout stays a single envelope. Success writes the OCI
// build-input projection and, without --json, writes nothing to stdout.
func runStage(ctx context.Context, options Options) error {
	settings := *options.settings
	if err := settings.err; err != nil {
		return writeCommandResult(options, "stage", nil, UsageError(err))
	}
	if settings.Profile == "" {
		return writeCommandResult(options, "stage", nil, UsageError(fmt.Errorf("--%s is required", flagProfile)))
	}
	if settings.Profile != profileGo {
		return writeCommandResult(
			options,
			"stage",
			nil,
			UsageError(fmt.Errorf("unknown profile %q (supported: %s)", settings.Profile, profileGo)),
		)
	}

	if settings.Dist == "" {
		return writeCommandResult(options, "stage", nil, UsageError(fmt.Errorf("--%s is required", flagDist)))
	}
	rootName, err := goprof.ParseRootName(settings.Dist)
	if err != nil {
		return writeCommandResult(options, "stage", nil, UsageError(fmt.Errorf(
			"--%s %q is not a basename; GoReleaser writes its distribution directory relative to the working directory",
			flagDist,
			settings.Dist,
		)))
	}

	signing, err := resolveNativeSigning(options.LookupEnv)
	if err != nil {
		return writeCommandResult(options, "stage", nil, UsageError(err))
	}

	if options.RunGoReleaser == nil {
		return writeCommandResult(options, "stage", nil, errors.New("goreleaser runner is not configured"))
	}

	// GoReleaser is chatty; both streams go to the CLI stderr so --json
	// stdout stays exactly one envelope.
	err = options.RunGoReleaser(ctx, goprof.GoReleaserOptions{
		Path:    lookupValue(options.LookupEnv, envGoreleaserPath),
		Dist:    rootName,
		Environ: nativeSigningEnvironment(os.Environ(), signing),
		Stdout:  options.Err,
		Stderr:  options.Err,
	})
	if err != nil {
		return writeCommandResult(options, "stage", nil, err)
	}

	root, err := os.OpenRoot(settings.Dist)
	if err != nil {
		return writeCommandResult(options, "stage", nil, fmt.Errorf("open dist %s: %w", settings.Dist, err))
	}
	defer root.Close()

	report, err := stage.Stage(root.FS(), rootName)
	if err != nil {
		return writeCommandResult(options, "stage", nil, err)
	}
	if err := writeImageInputs(root, report); err != nil {
		return writeCommandResult(options, "stage", nil, err)
	}
	if !settings.JSON {
		return nil
	}

	result := StageResult{
		Assets:   report.Assets,
		Binaries: make([]BinaryResult, 0, len(report.Binaries)),
	}
	for _, binary := range report.Binaries {
		result.Binaries = append(result.Binaries, BinaryResult{
			Arch: binary.Arch,
			Name: binary.Name,
			Path: binary.Path,
			Mode: strconv.FormatUint(uint64(binary.Mode), octalBase),
		})
	}

	return writeCommandResult(options, "stage", result, nil)
}

// resolveNativeSigning validates the selected package-signing configuration.
//
// The selection is a comma-separated list of native package formats, so a
// producer that holds an RPM key and no APK key signs its RPM packages and
// ships unsigned APK packages instead of being forced to choose between
// signing everything and signing nothing. An empty or unset selection signs
// nothing.
func resolveNativeSigning(lookup LookupEnv) (nativeSigning, error) {
	signing := nativeSigning{}
	formats := []nativeFormat{
		{
			name:          formatRPM,
			keyFileEnv:    envRPMSigningKeyFile,
			passphraseEnv: envRPMPassphrase,
			keyFile:       &signing.rpmKeyFile,
			passphrase:    &signing.rpmPassphrase,
		},
		{
			name:          formatAPK,
			keyFileEnv:    envAPKSigningKeyFile,
			passphraseEnv: envAPKPassphrase,
			keyFile:       &signing.apkKeyFile,
			passphrase:    &signing.apkPassphrase,
		},
	}

	selected, err := selectNativeFormats(lookupValue(lookup, envNativePackageSigning), formats)
	if err != nil {
		return nativeSigning{}, err
	}

	for _, format := range selected {
		keyFile := lookupValue(lookup, format.keyFileEnv)
		passphrase := lookupValue(lookup, format.passphraseEnv)
		for _, missing := range []struct {
			// name is the environment variable that must carry a value.
			name string
			// value is what the environment carries.
			value string
		}{
			{name: format.keyFileEnv, value: keyFile},
			{name: format.passphraseEnv, value: passphrase},
		} {
			if missing.value == "" {
				return nativeSigning{}, fmt.Errorf(
					"%s is required when %s selects %s",
					missing.name,
					envNativePackageSigning,
					format.name,
				)
			}
		}
		if err := validatePrivateKeyFile(format.keyFileEnv, keyFile); err != nil {
			return nativeSigning{}, err
		}
		*format.keyFile = keyFile
		*format.passphrase = passphrase
	}

	return signing, nil
}

// selectNativeFormats resolves a comma-separated selection to the formats it
// names. An unknown or repeated name is a usage error: a typo must not be
// read as "sign nothing".
func selectNativeFormats(selection string, formats []nativeFormat) ([]*nativeFormat, error) {
	if strings.TrimSpace(selection) == "" {
		return nil, nil
	}

	selected := make([]*nativeFormat, 0, len(formats))
	for name := range strings.SplitSeq(selection, ",") {
		name = strings.TrimSpace(name)
		var match *nativeFormat
		for index := range formats {
			if formats[index].name == name {
				match = &formats[index]
				break
			}
		}
		if match == nil {
			return nil, fmt.Errorf(
				"%s names unknown package format %q (supported: %s, %s)",
				envNativePackageSigning,
				name,
				formatRPM,
				formatAPK,
			)
		}
		for _, already := range selected {
			if already.name == name {
				return nil, fmt.Errorf("%s names package format %q twice", envNativePackageSigning, name)
			}
		}
		selected = append(selected, match)
	}

	return selected, nil
}

// validatePrivateKeyFile requires a regular owner-only private-key file.
func validatePrivateKeyFile(name, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s permissions %04o allow group or other access", name, info.Mode().Perm())
	}

	return nil
}

// nativeSigningEnvironment removes ambient native-signing values and overlays
// the validated values. Disabled signing supplies empty values because
// GoReleaser resolves the key-file templates before nFPM decides whether to sign.
func nativeSigningEnvironment(base []string, signing nativeSigning) []string {
	values := []struct {
		// name is the environment variable name.
		name string
		// value is the environment variable value.
		value string
	}{
		{name: envRPMSigningKeyFile, value: signing.rpmKeyFile},
		{name: envAPKSigningKeyFile, value: signing.apkKeyFile},
		{name: envRPMPassphrase, value: signing.rpmPassphrase},
		{name: envAPKPassphrase, value: signing.apkPassphrase},
	}
	names := make(map[string]struct{}, len(values))
	for _, value := range values {
		names[value.name] = struct{}{}
	}

	environ := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		name, _, found := strings.Cut(item, "=")
		if found {
			if _, replaced := names[name]; replaced {
				continue
			}
		}
		environ = append(environ, item)
	}
	for _, value := range values {
		environ = append(environ, value.name+"="+value.value)
	}

	return environ
}

// writeImageInputs persists the OCI build-input projection under dist.
func writeImageInputs(root *os.Root, report stage.Report) error {
	inputs, err := stage.NewImageInputs(profileGo, report)
	if err != nil {
		return err
	}

	file, err := root.Create(stage.ImageInputsName)
	if err != nil {
		return fmt.Errorf("create %s: %w", stage.ImageInputsName, err)
	}
	err = stage.EncodeImageInputs(file, inputs)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", stage.ImageInputsName, closeErr)
	}

	return nil
}
