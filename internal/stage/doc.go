// Package stage verifies a GoReleaser dist bundle against its checksum claim.
//
// ParseChecksums turns checksums.txt into a validated [ChecksumSet].
// VerifyBundle streams each claimed payload through SHA-256 and requires a
// nonempty regular checksums.txt.sigstore.json. Callers supply [io/fs.FS];
// the CLI composition edge is [os.OpenRoot].
package stage
