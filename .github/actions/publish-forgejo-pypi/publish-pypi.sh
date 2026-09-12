#!/usr/bin/env bash
#
# Uploads the Python distributions of a verified release bundle to a Forgejo
# PyPI package registry.
#
# The caller is expected to have verified the bundle already: the distribution
# directory must be the closed asset set that `release-cli verify bundle`
# accepted, so both distributions in it are covered by the signed
# `checksums.txt`. This script owns the transport and the distribution-set
# rules only. It runs the same way from a workflow and from an operator's
# terminal, so a rehearsal exercises the production path instead of an
# imitation of it.
#
# Usage: publish-pypi.sh <distribution-directory>
#
# Environment:
#   FORGEJO_ORIGIN            https origin of the forge, e.g. https://forge.dntls.net
#   FORGEJO_OWNER             registry owner that holds the packages
#   FORGEJO_PACKAGE           PEP 503 normalized distribution name, e.g. dntls-testnet-sdk
#   FORGEJO_EXPECTED_VERSION  version this release publishes; required to publish
#   FORGEJO_USERNAME          publisher account
#   FORGEJO_PUBLISH           true uploads; false checks the set and stops
#   FORGEJO_TOKEN             publisher token, required only when publishing
#   RELEASE_UV_PATH           uv executable that runs the pinned twine; optional
#
# Publishing is convergent. A distribution the registry already lists with the
# SHA-256 this run built is reported as unchanged and is never re-sent, which
# is what keeps a rerun from meeting the registry's refusal to replace a file.
# One that differs fails the run and the release must move to a new version.
# Nothing is ever deleted or replaced, and no request follows a redirect, so
# the credential never leaves the origin it was written for.

set -euo pipefail
# Tracing would echo the credential this script writes into its netrc.
set +x
umask 077

# Host with optional port, no userinfo and no path: anything else could send
# the credential somewhere other than the intended forge.
readonly ORIGIN_PATTERN='^https://[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$'
# One URL path segment. No slash, percent, or dot-dot can survive this.
readonly SEGMENT_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
# A PEP 503 normalized project name. The registry normalizes what it is sent,
# so requiring the normalized form up front keeps the simple-index URL, the
# file names, and the published name the same string.
readonly PACKAGE_PATTERN='^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'
# Stable releases only: a shared index must not serve a prerelease.
readonly VERSION_PATTERN='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
readonly DIGEST_PATTERN='^[0-9a-f]{64}$'
# The two members of the bundle that are not distributions.
readonly CHECKSUMS='checksums.txt'
readonly SIGNATURE='checksums.txt.sigstore.json'
readonly TWINE_VERSION='7.0.0'

# fail reports why the publication stopped and exits nonzero.
fail() {
	printf 'publish-pypi: %s\n' "$1" >&2
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

# uv_executable prints the uv that runs the pinned twine. An ambient twine is
# never used: the version that talks to the registry is the version this
# script names.
uv_executable() {
	local candidate="${RELEASE_UV_PATH-}"
	if [ -z "${candidate}" ]; then
		candidate="$(command -v uv 2>/dev/null || true)"
	fi
	if [ -z "${candidate}" ] && command -v mise >/dev/null 2>&1; then
		# mise may hold uv without shims on PATH, exactly as the workflow
		# installs it.
		candidate="$(mise which uv 2>/dev/null || true)"
	fi
	if [ -z "${candidate}" ]; then
		fail 'uv is required to run the pinned twine; install uv or set RELEASE_UV_PATH'
	fi
	if [ ! -x "${candidate}" ]; then
		fail "the resolved uv ${candidate} is not executable"
	fi
	printf '%s' "${candidate}"
}

# read_index writes `<file-name> <sha256>` for every entry Forgejo's simple
# index lists for this package, and leaves the file empty when the registry
# holds no version of it yet.
read_index() {
	local code
	if ! code="$(curl "${curl_options[@]}" --output "${listing}" "${simple_url}")"; then
		fail "reading the published files of ${package} did not complete"
	fi
	case "${code}" in
	200) ;;
	404)
		# The registry holds no version of this package: nothing is published.
		: >"${index}"
		return 0
		;;
	*)
		# The response body is the registry's, not this run's: it can carry
		# workflow commands or reflected credentials, so only the status is
		# reported.
		fail "reading the published files of ${package} returned HTTP ${code}"
		;;
	esac

	# Forgejo renders one anchor per published file, whose href ends in
	# `/<file-name>#sha256=<hex>`. Splitting on `<` puts each tag on its own
	# line, so no HTML nesting has to be understood to read that pair.
	tr '<' '\n' <"${listing}" | awk '
		/^a [^>]*href="/ {
			href = $0
			sub(/^.*href="/, "", href)
			sub(/".*$/, "", href)
			split(href, parts, "#sha256=")
			if (parts[2] == "") {
				next
			}
			name = parts[1]
			sub(/^.*\//, "", name)
			printf "%s %s\n", name, parts[2]
		}
	' >"${index}"
}

# published_digest prints the SHA-256 the index lists for a file name, and
# nothing when the index does not list it.
published_digest() {
	local wanted="$1" name digest
	while read -r name digest; do
		if [ "${name}" = "${wanted}" ]; then
			printf '%s' "${digest}"
			return 0
		fi
	done <"${index}"
}

if [ "$#" -ne 1 ]; then
	printf 'Usage: publish-pypi.sh <distribution-directory>\n' >&2
	exit 2
fi
dist="$1"
[ -d "${dist}" ] || fail "distribution directory ${dist} does not exist"

origin="$(checked FORGEJO_ORIGIN "${ORIGIN_PATTERN}")"
owner="$(checked FORGEJO_OWNER "${SEGMENT_PATTERN}")"
package="$(checked FORGEJO_PACKAGE "${PACKAGE_PATTERN}")"
username="$(checked FORGEJO_USERNAME "${SEGMENT_PATTERN}")"
publish="${FORGEJO_PUBLISH-}"
case "${publish}" in
true | false) ;;
*) fail "FORGEJO_PUBLISH must be true or false; got '${publish}'" ;;
esac

# Distributions carry their own version in their file names, so publication
# also holds them to the version this release is: a stale or mislabelled build
# is otherwise indistinguishable from the intended one once it is in the
# registry.
expected_version="${FORGEJO_EXPECTED_VERSION-}"
if [ "${publish}" = 'true' ] && [ -z "${expected_version}" ]; then
	fail 'FORGEJO_EXPECTED_VERSION is required to publish'
fi
if [ -n "${expected_version}" ] && ! [[ "${expected_version}" =~ ${VERSION_PATTERN} ]]; then
	fail "FORGEJO_EXPECTED_VERSION ${expected_version} is not a stable MAJOR.MINOR.PATCH release"
fi

# PEP 625: a distribution file stem is the project name with every run of `-`,
# `_`, or `.` folded to one `_`. The name checked above is already normalized,
# so only `-` can appear in it.
stem="${package//-/_}"

if command -v sha256sum >/dev/null 2>&1; then
	digest_tool=(sha256sum --)
elif command -v shasum >/dev/null 2>&1; then
	digest_tool=(shasum -a 256 --)
else
	fail 'sha256sum or shasum is required to digest the distributions'
fi

# Every entry in the top level, hidden ones included: the set is closed, so
# anything the release did not build is a reason to stop.
shopt -s nullglob dotglob
entries=("${dist}"/*)
shopt -u nullglob dotglob

sdist=''
for candidate in "${entries[@]}"; do
	name="${candidate##*/}"
	case "${name}" in
	"${stem}-"*.tar.gz)
		if [ -n "${sdist}" ]; then
			fail "${dist} holds more than one ${stem} source distribution"
		fi
		sdist="${name}"
		;;
	esac
done
[ -n "${sdist}" ] || fail "${dist} holds no source distribution named ${stem}-<version>.tar.gz"

version="${sdist#"${stem}-"}"
version="${version%.tar.gz}"
if ! [[ "${version}" =~ ${VERSION_PATTERN} ]]; then
	fail "${sdist} names version ${version}, which is not a stable MAJOR.MINOR.PATCH release"
fi
if [ -n "${expected_version}" ] && [ "${expected_version}" != "${version}" ]; then
	fail "the distributions name version ${version}, but this release publishes ${expected_version}"
fi

wheel="${stem}-${version}-py3-none-any.whl"
expected_names=("${sdist}" "${wheel}" "${CHECKSUMS}" "${SIGNATURE}")

for want in "${expected_names[@]}"; do
	[ -f "${dist}/${want}" ] || fail "${dist} is missing ${want}"
done
for candidate in "${entries[@]}"; do
	name="${candidate##*/}"
	if [ ! -f "${candidate}" ]; then
		fail "${name} in ${dist} is not a regular file"
	fi
	known='false'
	for want in "${expected_names[@]}"; do
		if [ "${name}" = "${want}" ]; then
			known='true'
			break
		fi
	done
	if [ "${known}" != 'true' ]; then
		fail "${dist} holds ${name}, which is not part of this release"
	fi
done

# The two loops above accept every expected name and reject every unexpected
# one, so a count that still differs means the directory changed underneath
# this run.
if [ "${#entries[@]}" -ne "${#expected_names[@]}" ]; then
	fail "expected ${#expected_names[@]} files in ${dist}, found ${#entries[@]}"
fi

names=("${sdist}" "${wheel}")
digests=("$(digest_of "${dist}/${sdist}")" "$(digest_of "${dist}/${wheel}")")

# A private index refuses an unauthenticated read, so a rehearsal without a
# token stops at the set checks instead of reporting a registry state it could
# not read.
token="${FORGEJO_TOKEN-}"
if [ -z "${token}" ]; then
	printf 'verified %s %s (%s, %s) without contacting %s\n' \
		"${package}" "${version}" "${sdist}" "${wheel}" "${origin}"
	exit 0
fi

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
netrc="${workdir}/netrc"
listing="${workdir}/simple.html"
index="${workdir}/index"

# A netrc binds the credential to one host, so a curl that is handed some
# other URL sends nothing. The token reaches curl through this file only:
# never through argv, an environment variable curl reads, or the URL.
host="${origin#https://}"
host="${host%%:*}"
printf 'machine %s\nlogin %s\npassword %s\n' "${host}" "${username}" "${token}" >"${netrc}"

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
	--max-time 300
	--write-out '%{http_code}'
)
simple_url="${origin}/api/packages/${owner}/pypi/simple/${package}"

# The registry keys a distribution by name and version and refuses to replace
# one, so what it already holds decides what this run may send.
read_index
pending=()
index_position=0
while [ "${index_position}" -lt "${#names[@]}" ]; do
	name="${names[index_position]}"
	built="${digests[index_position]}"
	index_position=$((index_position + 1))

	listed="$(published_digest "${name}")"
	if [ -z "${listed}" ]; then
		pending+=("${name}")
		continue
	fi
	if ! [[ "${listed}" =~ ${DIGEST_PATTERN} ]]; then
		fail "the registry lists ${name} with an unreadable SHA-256"
	fi
	if [ "${listed}" != "${built}" ]; then
		fail "${name} is already published with SHA-256 ${listed}, and this run built ${built}; publish these bytes as a new version"
	fi
	printf 'unchanged %s\n' "${name}"
done

if [ "${#pending[@]}" -eq 0 ]; then
	printf '%s %s is already published, identical\n' "${package}" "${version}"
	exit 0
fi

if [ "${publish}" != 'true' ]; then
	for name in "${pending[@]}"; do
		printf 'would upload %s to %s\n' "${name}" "${origin}"
	done
	exit 0
fi

uv="$(uv_executable)"
upload_url="${origin}/api/packages/${owner}/pypi"
files=()
for name in "${pending[@]}"; do
	files+=("${dist}/${name}")
done

# twine reads the credential from the environment. It never appears in argv,
# where every process on the runner could read it. An empty config file keeps
# the operator's ~/.pypirc out of a publication, as curl's -q does elsewhere.
if ! TWINE_USERNAME="${username}" TWINE_PASSWORD="${token}" \
	"${uv}" tool run --isolated "twine@${TWINE_VERSION}" upload \
	--non-interactive \
	--disable-progress-bar \
	--config-file /dev/null \
	--repository-url "${upload_url}" \
	"${files[@]}"; then
	fail "uploading ${pending[*]} did not complete"
fi

# The upload is accepted only once the index lists what this run built, so an
# upload the registry altered, refused in part, or filed under another version
# fails here instead of being reported as a publication.
read_index
for name in "${pending[@]}"; do
	listed="$(published_digest "${name}")"
	if [ -z "${listed}" ]; then
		fail "${name} was uploaded but the registry does not list it"
	fi
	index_position=0
	while [ "${index_position}" -lt "${#names[@]}" ]; do
		if [ "${names[index_position]}" = "${name}" ]; then
			break
		fi
		index_position=$((index_position + 1))
	done
	if [ "${listed}" != "${digests[index_position]}" ]; then
		fail "${name} is published with SHA-256 ${listed}, and this run built ${digests[index_position]}"
	fi
	printf 'published %s\n' "${name}"
done
