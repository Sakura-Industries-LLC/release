#!/usr/bin/env bash
#
# Uploads the RPM packages of a verified release bundle to a Forgejo RPM
# package registry.
#
# The caller is expected to have verified the bundle already: the distribution
# directory must be the closed asset set that `release-cli verify bundle`
# accepted, so every `.rpm` in it is covered by the signed `checksums.txt`.
# This script owns the transport and the package-set rules only. It runs the
# same way from a workflow and from an operator's terminal, so a rehearsal
# exercises the production path instead of an imitation of it.
#
# Usage: publish-rpm.sh <distribution-directory>
#
# Environment:
#   FORGEJO_ORIGIN                    https origin of the forge, e.g. https://forge.dntls.net
#   FORGEJO_OWNER                     registry owner that holds the packages
#   FORGEJO_GROUP                     RPM registry group, e.g. stable
#   FORGEJO_PACKAGE                   RPM package name both packages must declare
#   FORGEJO_EXPECTED_VERSION          version this release publishes; required to publish
#   FORGEJO_PRODUCER_KEY_FILE         reviewed producer OpenPGP public key, armored
#   FORGEJO_PRODUCER_KEY_FINGERPRINT  40 hex digits the key file must present
#   FORGEJO_USERNAME                  publisher account
#   FORGEJO_PUBLISH                   true uploads; false checks the set and stops
#   FORGEJO_TOKEN                     publisher token, required only when publishing
#
# Forgejo signs repository metadata with its own key, so a DNF client running
# `gpgcheck=1` trusts a package because of the signature baked into the package
# header. Every package is therefore held to the reviewed producer key here,
# before any credential is written and before the registry is contacted at all.
#
# Publishing is convergent. Every upload is read back from the registry and
# compared byte for byte, so a file the registry already holds unchanged is
# reported as unchanged and one that differs fails the run: the release must
# then move to a new version. Nothing is ever deleted or replaced, and no
# request follows a redirect, so the credential never leaves the origin it was
# written for.

set -euo pipefail
# Tracing would echo the credential this script writes into its netrc.
set +x
umask 077

# Host with optional port, no userinfo and no path: anything else could send
# the credential somewhere other than the intended forge.
readonly ORIGIN_PATTERN='^https://[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$'
# One URL path segment. No slash, percent, or dot-dot can survive this.
readonly SEGMENT_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
readonly PACKAGE_PATTERN='^[A-Za-z0-9][A-Za-z0-9._+-]{1,63}$'
# Stable releases only: a DNF repository must not serve a prerelease.
readonly VERSION_PATTERN='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
# The RPM release field addresses the package in the registry, so it is held to
# characters that survive a URL path segment unchanged.
readonly RELEASE_PATTERN='^[A-Za-z0-9][A-Za-z0-9._+]{0,63}$'
readonly EPOCH_PATTERN='^(0|[1-9][0-9]{0,8})$'
readonly FINGERPRINT_PATTERN='^[0-9A-F]{40}$'
readonly REQUIRED_ARCHITECTURES='x86_64 aarch64'

# fail reports why the publication stopped and exits nonzero.
fail() {
	printf 'publish-rpm: %s\n' "$1" >&2
	exit 1
}

# checked returns the named environment variable after holding it to a
# pattern, so an unset, empty, or hostile value never reaches a URL.
checked() {
	local name="$1" pattern="$2" value
	value="${!name-}"
	if [ -z "${value}" ]; then
		fail "${name} is required"
	fi
	if ! [[ "${value}" =~ ${pattern} ]]; then
		fail "${name} has an invalid value"
	fi
	printf '%s' "${value}"
}

# digest_of prints the SHA-256 of a file as lowercase hex.
digest_of() {
	"${digest_tool[@]}" "$1" | cut -d ' ' -f 1
}

# primary_fingerprint prints the fingerprint of the single primary key the
# armored public-key file contains. A file holding no key, or more than one,
# is not a reviewed key.
primary_fingerprint() {
	local file="$1" listing fingerprints
	if ! listing="$(gpg --homedir "${gnupghome}" --batch --no-tty --with-colons --show-keys -- "${file}" 2>/dev/null)"; then
		fail "FORGEJO_PRODUCER_KEY_FILE is not an OpenPGP public key"
	fi
	# Only the fingerprint record that follows a `pub` record names a primary
	# key; subkey fingerprints follow `sub` records.
	fingerprints="$(printf '%s\n' "${listing}" | awk -F: '
		$1 == "pub" { primary = 1; next }
		$1 == "fpr" && primary { print $10; primary = 0; next }
		$1 == "sub" { primary = 0 }
	')"
	if [ "$(printf '%s\n' "${fingerprints}" | grep -c '[0-9A-F]')" != '1' ]; then
		fail 'FORGEJO_PRODUCER_KEY_FILE must contain exactly one primary public key'
	fi
	printf '%s' "${fingerprints}"
}

# header reads one header value from an RPM package.
header() {
	local tag="$1" package="$2" value
	value="$(rpm --dbpath "${rpmdb}" --query --package --queryformat "%{${tag}}" -- "${package}")"
	if [ -z "${value}" ] || [ "${value}" = '(none)' ]; then
		fail "${package} declares no ${tag}"
	fi
	printf '%s' "${value}"
}

# verify_signature requires a package signature the reviewed producer key
# validates. An unsigned package passes `rpm --checksig` on its digests alone,
# so the signature line is required explicitly.
verify_signature() {
	local file="$1" report signed=0 line
	if ! report="$(rpm --dbpath "${rpmdb}" --checksig --verbose -- "${file}" 2>&1)"; then
		fail "${file} has no signature the reviewed producer key validates"
	fi
	while IFS= read -r line; do
		# The first line is the file name; every result line reports one check.
		case "${line}" in
		*': OK') ;;
		"${file}:") continue ;;
		*) fail "${file} failed an integrity check: ${line#*: }" ;;
		esac
		# The scratch keyring already limits cryptographic verification to the
		# pinned primary key and its signing subkeys. RPM 4 reports a key ID;
		# RPM 6 reports a fingerprint. Neither changes the trust boundary.
		if [[ "${line,,}" == *' signature,'* ]]; then
			signed=1
		fi
	done <<<"${report}"
	if [ "${signed}" -ne 1 ]; then
		fail "${file} carries no OpenPGP signature; DNF clients with gpgcheck=1 would reject it"
	fi
}

if [ "$#" -ne 1 ]; then
	printf 'Usage: publish-rpm.sh <distribution-directory>\n' >&2
	exit 2
fi
dist="$1"
[ -d "${dist}" ] || fail "distribution directory ${dist} does not exist"

origin="$(checked FORGEJO_ORIGIN "${ORIGIN_PATTERN}")"
owner="$(checked FORGEJO_OWNER "${SEGMENT_PATTERN}")"
group="$(checked FORGEJO_GROUP "${SEGMENT_PATTERN}")"
package="$(checked FORGEJO_PACKAGE "${PACKAGE_PATTERN}")"
username="$(checked FORGEJO_USERNAME "${SEGMENT_PATTERN}")"
fingerprint="$(checked FORGEJO_PRODUCER_KEY_FINGERPRINT "${FINGERPRINT_PATTERN}")"
key_file="${FORGEJO_PRODUCER_KEY_FILE-}"
[ -n "${key_file}" ] || fail 'FORGEJO_PRODUCER_KEY_FILE is required'
[ -f "${key_file}" ] || fail "producer key file ${key_file} does not exist"
publish="${FORGEJO_PUBLISH-}"
case "${publish}" in
true | false) ;;
*) fail "FORGEJO_PUBLISH must be true or false; got '${publish}'" ;;
esac

command -v rpm >/dev/null 2>&1 ||
	fail 'rpm is required to read the package headers and verify the package signatures'
command -v gpg >/dev/null 2>&1 ||
	fail 'gpg is required to identify the reviewed producer key'

if command -v sha256sum >/dev/null 2>&1; then
	digest_tool=(sha256sum --)
elif command -v shasum >/dev/null 2>&1; then
	digest_tool=(shasum -a 256 --)
else
	fail 'sha256sum or shasum is required to compare a published file'
fi

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
gnupghome="${workdir}/gnupg"
rpmdb="${workdir}/rpmdb"
netrc="${workdir}/netrc"
response="${workdir}/response"
existing="${workdir}/existing.rpm"
mkdir -p "${gnupghome}" "${rpmdb}"
chmod 700 "${gnupghome}"

# Only regular files whose name ends in .rpm, and only in the top level: the
# sibling SBOM and checksum members of the bundle are not packages.
packages=()
for candidate in "${dist}"/*.rpm; do
	[ -f "${candidate}" ] || continue
	packages+=("${candidate}")
done
if [ "${#packages[@]}" -ne 2 ]; then
	fail "expected 2 RPM packages in ${dist}, found ${#packages[@]}"
fi

# The producer key decides which packages this publication may carry, so the
# key file is bound to the reviewed fingerprint before it is trusted.
declared_fingerprint="$(primary_fingerprint "${key_file}")"
if [ "${declared_fingerprint}" != "${fingerprint}" ]; then
	fail "producer key file ${key_file} presents fingerprint ${declared_fingerprint}, expected ${fingerprint}"
fi

# A scratch database holds exactly the reviewed key, so a signature can only
# be accepted because that key validates it, never because the operator's or
# the runner's keyring happens to hold something else.
rpm --dbpath "${rpmdb}" --initdb
rpm --dbpath "${rpmdb}" --import -- "${key_file}" ||
	fail "producer key file ${key_file} could not be imported as an RPM signing key"

# The registry keys a file by the header fields, not by the file name, so the
# set is checked and later addressed by what the packages declare.
version=''
release=''
epoch=''
architectures=''
names=()
routes=()
for candidate in "${packages[@]}"; do
	verify_signature "${candidate}"

	declared_package="$(header NAME "${candidate}")"
	declared_version="$(header VERSION "${candidate}")"
	declared_release="$(header RELEASE "${candidate}")"
	declared_architecture="$(header ARCH "${candidate}")"
	# EPOCHNUM is 0 for a package that declares no epoch, which is exactly how
	# the registry treats a missing epoch.
	declared_epoch="$(rpm --dbpath "${rpmdb}" --query --package --queryformat '%{EPOCHNUM}' -- "${candidate}")"

	if [ "${declared_package}" != "${package}" ]; then
		fail "${candidate} declares package ${declared_package}, expected ${package}"
	fi
	if ! [[ "${declared_version}" =~ ${VERSION_PATTERN} ]]; then
		fail "${candidate} declares version ${declared_version}, which is not a stable MAJOR.MINOR.PATCH release"
	fi
	if ! [[ "${declared_release}" =~ ${RELEASE_PATTERN} ]]; then
		fail "${candidate} declares release ${declared_release}, which cannot address the package in the registry"
	fi
	if ! [[ "${declared_epoch}" =~ ${EPOCH_PATTERN} ]]; then
		fail "${candidate} declares epoch ${declared_epoch}, which cannot address the package in the registry"
	fi
	if ! [[ "${declared_architecture}" =~ ${SEGMENT_PATTERN} ]]; then
		fail "${candidate} declares architecture ${declared_architecture}, which cannot address the package in the registry"
	fi

	if [ -z "${version}" ]; then
		version="${declared_version}"
		release="${declared_release}"
		epoch="${declared_epoch}"
	else
		if [ "${declared_version}" != "${version}" ]; then
			fail "the packages declare different versions: ${version} and ${declared_version}"
		fi
		if [ "${declared_release}" != "${release}" ]; then
			fail "the packages declare different releases: ${release} and ${declared_release}"
		fi
		if [ "${declared_epoch}" != "${epoch}" ]; then
			fail "the packages declare different epochs: ${epoch} and ${declared_epoch}"
		fi
	fi
	case " ${architectures} " in
	*" ${declared_architecture} "*)
		fail "two packages declare architecture ${declared_architecture}"
		;;
	esac
	architectures="${architectures}${declared_architecture} "

	# Forgejo files an RPM under `VERSION-RELEASE`, prefixed with `EPOCH-` when
	# the package declares an epoch other than zero, and serves it as
	# `NAME-<that>.ARCH.rpm`.
	route="${declared_version}-${declared_release}"
	if [ "${declared_epoch}" != '0' ]; then
		route="${declared_epoch}-${route}"
	fi
	routes+=("${route}/${declared_architecture}")
	names+=("${declared_package}-${route}.${declared_architecture}.rpm")
done

for required in ${REQUIRED_ARCHITECTURES}; do
	case " ${architectures} " in
	*" ${required} "*) ;;
	*) fail "the package set is missing architecture ${required}" ;;
	esac
done

# Packages carry their own version, so publication also holds them to the
# version this release is: a stale or mislabelled build is otherwise
# indistinguishable from the intended one once it is in the registry.
expected_version="${FORGEJO_EXPECTED_VERSION-}"
if [ "${publish}" = 'true' ] && [ -z "${expected_version}" ]; then
	fail 'FORGEJO_EXPECTED_VERSION is required to publish'
fi
if [ -n "${expected_version}" ] && [ "${expected_version}" != "${version}" ]; then
	fail "the packages declare version ${version}, but this release publishes ${expected_version}"
fi

if [ "${publish}" != 'true' ]; then
	printf 'verified %s %s-%s for %s signed by %s without contacting %s\n' \
		"${package}" "${version}" "${release}" "${architectures% }" "${fingerprint}" "${origin}"
	exit 0
fi

token="${FORGEJO_TOKEN-}"
[ -n "${token}" ] || fail 'FORGEJO_TOKEN is required to publish'

# A netrc binds the credential to one host, so a curl that is handed some
# other URL sends nothing. The token reaches curl through this file only:
# never through argv, an environment variable curl reads, or the URL.
host="${origin#https://}"
host="${host%%:*}"
printf 'machine %s\nlogin %s\npassword %s\n' "${host}" "${username}" "${token}" >"${netrc}"
unset token FORGEJO_TOKEN

# -q first: the operator's ~/.curlrc must not reach a publication. Redirects
# are never followed, so no response can move the credential or the digest
# comparison to another location.
curl_options=(
	-q
	--netrc-file "${netrc}"
	--proto '=https'
	--silent
	--show-error
	--retry 3
	--retry-connrefused
	--max-time 900
	--write-out '%{http_code}'
)
upload_url="${origin}/api/packages/${owner}/rpm/${group}/upload"

# published_matches reads the file the registry serves under the route the
# headers derive and compares it with the file this run built. Nothing here
# deletes or replaces a published file.
published_matches() {
	local name="$1" route="$2" candidate="$3" code published_digest built_digest
	local package_url="${origin}/api/packages/${owner}/rpm/${group}/package/${package}/${route}"

	if ! code="$(curl "${curl_options[@]}" --output "${existing}" "${package_url}")"; then
		fail "reading the published ${name} did not complete"
	fi
	if [ "${code}" != '200' ]; then
		fail "the registry holds ${name} but reading it returned HTTP ${code}"
	fi

	published_digest="$(digest_of "${existing}")"
	built_digest="$(digest_of "${candidate}")"
	if [ "${published_digest}" != "${built_digest}" ]; then
		fail "${name} is published with SHA-256 ${published_digest}, and this run built ${built_digest}; publish these bytes as a new version"
	fi
}

index=0
for candidate in "${packages[@]}"; do
	name="${names[${index}]}"
	route="${routes[${index}]}"
	index=$((index + 1))

	if ! code="$(curl "${curl_options[@]}" --output "${response}" --upload-file "${candidate}" "${upload_url}")"; then
		fail "uploading ${name} did not complete"
	fi

	case "${code}" in
	201)
		# The registry accepted the upload. Reading it back proves the route
		# these headers derive serves exactly the reviewed bytes, and that the
		# instance stored the package instead of re-signing it.
		published_matches "${name}" "${route}" "${candidate}"
		printf 'published %s\n' "${name}"
		;;
	409)
		# The registry already holds a file under this exact key. Publishing is
		# only convergent if those bytes are these bytes.
		published_matches "${name}" "${route}" "${candidate}"
		printf 'unchanged %s\n' "${name}"
		;;
	*)
		# The response body is the registry's, not this run's: it can carry
		# workflow commands or reflected credentials, so only the status is
		# reported.
		fail "uploading ${name} returned HTTP ${code}"
		;;
	esac
done
