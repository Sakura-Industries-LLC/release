#!/usr/bin/env bash
#
# Uploads the Debian packages of a verified release bundle to a Forgejo Debian
# package registry.
#
# The caller is expected to have verified the bundle already: the distribution
# directory must be the closed asset set that `release-cli verify bundle`
# accepted, so every `.deb` in it is covered by the signed `checksums.txt`.
# This script owns the transport and the package-set rules only. It runs the
# same way from a workflow and from an operator's terminal, so a rehearsal
# exercises the production path instead of an imitation of it.
#
# Usage: publish-debian.sh <distribution-directory>
#
# Environment:
#   FORGEJO_ORIGIN            https origin of the forge, e.g. https://forge.dntls.net
#   FORGEJO_OWNER             registry owner that holds the packages
#   FORGEJO_DISTRIBUTION      apt distribution, e.g. stable
#   FORGEJO_COMPONENT         apt component, e.g. main
#   FORGEJO_PACKAGE           Debian package name both packages must declare
#   FORGEJO_EXPECTED_VERSION  version this release publishes; required to publish
#   FORGEJO_USERNAME          publisher account
#   FORGEJO_PUBLISH           true uploads; false checks the set and stops
#   FORGEJO_TOKEN             publisher token, required only when publishing
#
# Publishing is convergent. A file the registry already holds byte for byte is
# reported as unchanged; one that differs fails the run and the release must
# move to a new version. Nothing is ever deleted or replaced, and no request
# follows a redirect, so the credential never leaves the origin it was written
# for.

set -euo pipefail
# Tracing would echo the credential this script writes into its netrc.
set +x
umask 077

# Host with optional port, no userinfo and no path: anything else could send
# the credential somewhere other than the intended forge.
readonly ORIGIN_PATTERN='^https://[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$'
# One URL path segment. No slash, percent, or dot-dot can survive this.
readonly SEGMENT_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
readonly PACKAGE_PATTERN='^[a-z0-9][a-z0-9+.-]{1,63}$'
# Stable releases only: an apt suite must not serve a prerelease.
readonly VERSION_PATTERN='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
readonly REQUIRED_ARCHITECTURES='amd64 arm64'

# fail reports why the publication stopped and exits nonzero.
fail() {
	printf 'publish-debian: %s\n' "$1" >&2
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

# field reads one control field from a Debian package.
field() {
	local name="$1" package="$2" value
	value="$(dpkg-deb --field "${package}" "${name}")"
	if [ -z "${value}" ]; then
		fail "${package} declares no ${name}"
	fi
	printf '%s' "${value}"
}

# digest_of prints the SHA-256 of a file as lowercase hex.
digest_of() {
	"${digest_tool[@]}" "$1" | cut -d ' ' -f 1
}

if [ "$#" -ne 1 ]; then
	printf 'Usage: publish-debian.sh <distribution-directory>\n' >&2
	exit 2
fi
dist="$1"
[ -d "${dist}" ] || fail "distribution directory ${dist} does not exist"

origin="$(checked FORGEJO_ORIGIN "${ORIGIN_PATTERN}")"
owner="$(checked FORGEJO_OWNER "${SEGMENT_PATTERN}")"
distribution="$(checked FORGEJO_DISTRIBUTION "${SEGMENT_PATTERN}")"
component="$(checked FORGEJO_COMPONENT "${SEGMENT_PATTERN}")"
package="$(checked FORGEJO_PACKAGE "${PACKAGE_PATTERN}")"
username="$(checked FORGEJO_USERNAME "${SEGMENT_PATTERN}")"
publish="${FORGEJO_PUBLISH-}"
case "${publish}" in
true | false) ;;
*) fail "FORGEJO_PUBLISH must be true or false; got '${publish}'" ;;
esac

command -v dpkg-deb >/dev/null 2>&1 ||
	fail 'dpkg-deb is required to read the package control fields'

# Only regular files whose name ends in .deb, and only in the top level: the
# sibling SBOM and checksum members of the bundle are not packages.
packages=()
for candidate in "${dist}"/*.deb; do
	[ -f "${candidate}" ] || continue
	packages+=("${candidate}")
done
if [ "${#packages[@]}" -ne 2 ]; then
	fail "expected 2 Debian packages in ${dist}, found ${#packages[@]}"
fi

# The registry keys a file by the control fields, not by the file name, so the
# set is checked and later addressed by what the packages declare.
version=''
architectures=''
names=()
for candidate in "${packages[@]}"; do
	declared_package="$(field Package "${candidate}")"
	declared_version="$(field Version "${candidate}")"
	declared_architecture="$(field Architecture "${candidate}")"

	if [ "${declared_package}" != "${package}" ]; then
		fail "${candidate} declares package ${declared_package}, expected ${package}"
	fi
	if ! [[ "${declared_version}" =~ ${VERSION_PATTERN} ]]; then
		fail "${candidate} declares version ${declared_version}, which is not a stable MAJOR.MINOR.PATCH release"
	fi
	if [ -z "${version}" ]; then
		version="${declared_version}"
	elif [ "${declared_version}" != "${version}" ]; then
		fail "the packages declare different versions: ${version} and ${declared_version}"
	fi
	case " ${architectures} " in
	*" ${declared_architecture} "*)
		fail "two packages declare architecture ${declared_architecture}"
		;;
	esac
	architectures="${architectures}${declared_architecture} "
	names+=("${declared_package}_${declared_version}_${declared_architecture}.deb")
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
	printf 'verified %s %s for %s without contacting %s\n' \
		"${package}" "${version}" "${architectures% }" "${origin}"
	exit 0
fi

token="${FORGEJO_TOKEN-}"
[ -n "${token}" ] || fail 'FORGEJO_TOKEN is required to publish'

if command -v sha256sum >/dev/null 2>&1; then
	digest_tool=(sha256sum --)
elif command -v shasum >/dev/null 2>&1; then
	digest_tool=(shasum -a 256 --)
else
	fail 'sha256sum or shasum is required to compare a conflicting file'
fi

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
netrc="${workdir}/netrc"
response="${workdir}/response"
existing="${workdir}/existing.deb"

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
upload_url="${origin}/api/packages/${owner}/debian/pool/${distribution}/${component}/upload"

index=0
for candidate in "${packages[@]}"; do
	name="${names[${index}]}"
	index=$((index + 1))

	if ! code="$(curl "${curl_options[@]}" --output "${response}" --upload-file "${candidate}" "${upload_url}")"; then
		fail "uploading ${name} did not complete"
	fi

	case "${code}" in
	201)
		printf 'published %s\n' "${name}"
		continue
		;;
	409) ;;
	*)
		# The response body is the registry's, not this run's: it can carry
		# workflow commands or reflected credentials, so only the status is
		# reported.
		fail "uploading ${name} returned HTTP ${code}"
		;;
	esac

	# The registry already holds a file under this exact key. Publishing is
	# only convergent if those bytes are these bytes. Nothing here deletes or
	# replaces a published file.
	pool_url="${origin}/api/packages/${owner}/debian/pool/${distribution}/${component}/${name}"
	if ! code="$(curl "${curl_options[@]}" --output "${existing}" "${pool_url}")"; then
		fail "reading the published ${name} did not complete"
	fi
	if [ "${code}" != '200' ]; then
		fail "the registry reports ${name} as published but reading it returned HTTP ${code}"
	fi

	published_digest="$(digest_of "${existing}")"
	built_digest="$(digest_of "${candidate}")"
	if [ "${published_digest}" != "${built_digest}" ]; then
		fail "${name} is already published with SHA-256 ${published_digest}, and this run built ${built_digest}; publish these bytes as a new version"
	fi
	printf 'unchanged %s\n' "${name}"
done
