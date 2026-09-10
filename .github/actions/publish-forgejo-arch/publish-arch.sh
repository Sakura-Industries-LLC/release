#!/usr/bin/env bash
#
# Uploads the Arch Linux packages of a verified release bundle to a Forgejo
# Arch package registry.
#
# The caller is expected to have verified the bundle already: the distribution
# directory must be the closed asset set that `release-cli verify bundle`
# accepted, so every `.pkg.tar.zst` in it is covered by the signed
# `checksums.txt`. This script owns the transport and the package-set rules
# only. It runs the same way from a workflow and from an operator's terminal,
# so a rehearsal exercises the production path instead of an imitation of it.
#
# Usage: publish-arch.sh <distribution-directory>
#
# Environment:
#   FORGEJO_ORIGIN            https origin of the forge, e.g. https://forge.dntls.net
#   FORGEJO_OWNER             registry owner that holds the packages
#   FORGEJO_GROUP             Arch registry group, e.g. stable
#   FORGEJO_PACKAGE           pkgname both packages must declare
#   FORGEJO_EXPECTED_VERSION  version this release publishes; required to publish
#   FORGEJO_USERNAME          publisher account
#   FORGEJO_PUBLISH           true uploads; false checks the set and stops
#   FORGEJO_TOKEN             publisher token, required only when publishing
#
# Forgejo signs both the package and the pacman database with the registry's
# own key, so a pacman client checks signatures the registry produced. Nothing
# here signs anything: the producer ships unsigned packages, and this script
# proves the registry ended up in the state a pacman client needs.
#
# Publishing is convergent, and it is not atomic on the Forgejo side: the
# instance creates the package first, then adds its signature, then rebuilds
# and signs the database. A failure between those steps leaves a package that
# a retry can only meet as `409`. Every upload is therefore followed by a full
# read-back of the published state - package bytes, package signature,
# database signature, and the database entry itself - for `201` and `409`
# alike. An incomplete or conflicting state fails the run for an operator to
# repair. Nothing is ever deleted or replaced, and no request follows a
# redirect, so the credential never leaves the origin it was written for.

set -euo pipefail
# Tracing would echo the credential this script writes into its netrc.
set +x
umask 077

# Host with optional port, no userinfo and no path: anything else could send
# the credential somewhere other than the intended forge.
readonly ORIGIN_PATTERN='^https://[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$'
# One URL path segment. No slash, percent, or dot-dot can survive this.
readonly SEGMENT_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
# The pkgname characters Forgejo accepts, minus a leading punctuation mark.
readonly PACKAGE_PATTERN='^[A-Za-z0-9][A-Za-z0-9@._+-]{1,63}$'
# Stable releases only: a pacman repository must not serve a prerelease.
readonly VERSION_PATTERN='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
# The pkgrel field addresses the package in the registry and in the database,
# so it is held to the plain numeric form pacman expects.
readonly RELEASE_PATTERN='^[1-9][0-9]*$'
# A full pkgver as Forgejo files it: upstream version, then pkgrel.
readonly PKGVER_PATTERN='^[A-Za-z0-9._+]+-[0-9]+$'
readonly REQUIRED_ARCHITECTURES='x86_64 aarch64'

# fail reports why the publication stopped and exits nonzero.
fail() {
	printf 'publish-arch: %s\n' "$1" >&2
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

# base64_of prints the standard base64 of a file on one line, which is how
# Forgejo records a package signature in its database.
base64_of() {
	openssl base64 -A -in "$1"
}

# info_value prints the single value a .PKGINFO body declares for a key. A key
# the package declares twice is ambiguous, and nothing may be guessed about
# which one the registry would file the package under.
info_value() {
	local body="$1" key="$2" values count
	values="$(printf '%s\n' "${body}" | awk -v key="${key}" '
		substr($0, 1, 1) == "#" { next }
		{
			separator = index($0, " = ")
			if (separator == 0) { next }
			if (substr($0, 1, separator - 1) == key) { print substr($0, separator + 3) }
		}
	')"
	count="$(printf '%s' "${values}" | grep -c . || true)"
	if [ "${count}" != '1' ]; then
		fail "the package declares ${key} ${count} times, expected once"
	fi
	printf '%s' "${values}"
}

# entry_count prints how many entries of an archive carry the given name.
entry_count() {
	local archive="$1" name="$2"
	bsdtar -tf "${archive}" | grep -c -x -F -- "${name}" || true
}

# desc_value prints the value a pacman database description declares for a
# field, or nothing when the field is absent.
desc_value() {
	local body="$1" field="$2"
	printf '%s\n' "${body}" | awk -v field="%${field}%" '
		$0 == field { getline value; print value; exit }
	'
}

if [ "$#" -ne 1 ]; then
	printf 'Usage: publish-arch.sh <distribution-directory>\n' >&2
	exit 2
fi
dist="$1"
[ -d "${dist}" ] || fail "distribution directory ${dist} does not exist"

origin="$(checked FORGEJO_ORIGIN "${ORIGIN_PATTERN}")"
owner="$(checked FORGEJO_OWNER "${SEGMENT_PATTERN}")"
group="$(checked FORGEJO_GROUP "${SEGMENT_PATTERN}")"
package="$(checked FORGEJO_PACKAGE "${PACKAGE_PATTERN}")"
username="$(checked FORGEJO_USERNAME "${SEGMENT_PATTERN}")"
publish="${FORGEJO_PUBLISH-}"
case "${publish}" in
true | false) ;;
*) fail "FORGEJO_PUBLISH must be true or false; got '${publish}'" ;;
esac

command -v bsdtar >/dev/null 2>&1 ||
	fail 'bsdtar is required to read package and database metadata'
command -v gpgv >/dev/null 2>&1 ||
	fail 'gpgv is required to verify the signatures the registry produces'
command -v gpg >/dev/null 2>&1 ||
	fail 'gpg is required to read the registry key'
command -v openssl >/dev/null 2>&1 ||
	fail 'openssl is required to compare a database signature entry'

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
netrc="${workdir}/netrc"
response="${workdir}/response"
registry_key="${workdir}/repository.key"
keyring="${workdir}/registry.gpg"
published="${workdir}/published.pkg.tar.zst"
published_signature="${workdir}/published.sig"
database="${workdir}/database.db"
database_signature="${workdir}/database.db.sig"
mkdir -p "${gnupghome}"
chmod 700 "${gnupghome}"

# Only regular files whose name ends in .pkg.tar.zst, and only in the top
# level: the sibling SBOM, checksum, and archive members of the bundle are not
# packages, and neither is a detached signature beside one.
packages=()
for candidate in "${dist}"/*.pkg.tar.zst; do
	[ -f "${candidate}" ] || continue
	packages+=("${candidate}")
done
if [ "${#packages[@]}" -ne 2 ]; then
	fail "expected 2 Arch packages in ${dist}, found ${#packages[@]}"
fi

# The registry keys a file by what the package declares, not by the name it
# was uploaded under, so the set is checked and later addressed by its
# metadata. The archive is read through libarchive and never unpacked onto
# this host.
pkgver=''
architectures=''
filenames=()
package_architectures=()
for candidate in "${packages[@]}"; do
	if ! bsdtar -tf "${candidate}" >/dev/null 2>&1; then
		fail "${candidate} is not a readable zstd-compressed tar archive"
	fi
	if [ "$(entry_count "${candidate}" .PKGINFO)" != '1' ]; then
		fail "${candidate} must contain exactly one .PKGINFO entry"
	fi
	if [ "$(entry_count "${candidate}" .MTREE)" != '1' ]; then
		fail "${candidate} must contain exactly one .MTREE entry; the registry rejects a package without it"
	fi

	info="$(bsdtar -xOf "${candidate}" .PKGINFO)"
	declared_package="$(info_value "${info}" pkgname)"
	declared_pkgver="$(info_value "${info}" pkgver)"
	declared_architecture="$(info_value "${info}" arch)"

	if [ "${declared_package}" != "${package}" ]; then
		fail "${candidate} declares pkgname ${declared_package}, expected ${package}"
	fi
	# An epoch would change both the registry route and the database key, and
	# no producer here emits one.
	if ! [[ "${declared_pkgver}" =~ ${PKGVER_PATTERN} ]]; then
		fail "${candidate} declares pkgver ${declared_pkgver}, which is not a plain VERSION-RELEASE"
	fi
	declared_version="${declared_pkgver%-*}"
	declared_release="${declared_pkgver##*-}"
	if ! [[ "${declared_version}" =~ ${VERSION_PATTERN} ]]; then
		fail "${candidate} declares version ${declared_version}, which is not a stable MAJOR.MINOR.PATCH release"
	fi
	if ! [[ "${declared_release}" =~ ${RELEASE_PATTERN} ]]; then
		fail "${candidate} declares pkgrel ${declared_release}, which cannot address the package in the registry"
	fi
	if ! [[ "${declared_architecture}" =~ ${SEGMENT_PATTERN} ]]; then
		fail "${candidate} declares arch ${declared_architecture}, which cannot address the package in the registry"
	fi

	if [ -z "${pkgver}" ]; then
		pkgver="${declared_pkgver}"
		version="${declared_version}"
	elif [ "${declared_pkgver}" != "${pkgver}" ]; then
		fail "the packages declare different versions: ${pkgver} and ${declared_pkgver}"
	fi
	case " ${architectures} " in
	*" ${declared_architecture} "*)
		fail "two packages declare architecture ${declared_architecture}"
		;;
	esac
	architectures="${architectures}${declared_architecture} "

	# Forgejo serves a package as `NAME-PKGVER-ARCH.pkg.tar.zst`, whatever the
	# uploaded file was called.
	filenames+=("${package}-${pkgver}-${declared_architecture}.pkg.tar.zst")
	package_architectures+=("${declared_architecture}")
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
		"${package}" "${pkgver}" "${architectures% }" "${origin}"
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
registry_url="${origin}/api/packages/${owner}/arch"
upload_url="${registry_url}/${group}"

# read_registry stores one registry file and fails unless it was served whole.
# The response body of an unexpected status is the registry's, not this run's:
# it can carry workflow commands or reflected credentials, so only the status
# is reported.
read_registry() {
	local url="$1" destination="$2" description="$3" code
	if ! code="$(curl "${curl_options[@]}" --output "${destination}" "${url}")"; then
		fail "reading ${description} did not complete"
	fi
	if [ "${code}" != '200' ]; then
		fail "reading ${description} returned HTTP ${code}"
	fi
}

# The registry signs every package and every database it serves with this key,
# so it is fetched once, from the same authenticated origin, and every
# signature below is verified against exactly it.
read_registry "${registry_url}/repository.key" "${registry_key}" 'the registry key'
gpg --homedir "${gnupghome}" --batch --no-tty --quiet --import -- "${registry_key}" ||
	fail 'the registry did not serve an OpenPGP public key'
gpg --homedir "${gnupghome}" --batch --no-tty --quiet --export >"${keyring}"

# verify_signature holds a registry file to a detached signature the registry
# key validates. gpgv reads the exported keyring only, so a signature is
# accepted because that key made it, never because a keyring on this host
# holds something else.
verify_signature() {
	local signature="$1" file="$2" description="$3"
	if ! gpgv --keyring "${keyring}" -- "${signature}" "${file}" >/dev/null 2>&1; then
		fail "${description} is not signed by the registry key"
	fi
}

# published_state holds the registry to the complete state a pacman client
# needs for one architecture: the package bytes this run built, a package
# signature the registry key validates, a signed database, and the database
# entry that addresses exactly this file. Nothing here deletes or replaces a
# published file.
published_state() {
	local filename="$1" architecture="$2" candidate="$3"
	local base="${registry_url}/${group}/${architecture}"
	local published_digest built_digest entry indexed description signature field

	read_registry "${base}/${filename}" "${published}" "the published ${filename}"
	published_digest="$(digest_of "${published}")"
	built_digest="$(digest_of "${candidate}")"
	if [ "${published_digest}" != "${built_digest}" ]; then
		fail "${filename} is published with SHA-256 ${published_digest}, and this run built ${built_digest}; publish these bytes as a new version"
	fi

	read_registry "${base}/${filename}.sig" "${published_signature}" "the signature of ${filename}"
	verify_signature "${published_signature}" "${published}" "${filename}"

	read_registry "${base}/${group}.db" "${database}" "the ${architecture} database"
	read_registry "${base}/${group}.db.sig" "${database_signature}" "the ${architecture} database signature"
	verify_signature "${database_signature}" "${database}" "the ${architecture} database"

	# Forgejo indexes one entry per package name, for the version it created
	# most recently, under `NAME-PKGVER/desc`. A package name that is a prefix
	# of another cannot be confused with it, because the remainder of a real
	# entry is a pkgver.
	indexed=''
	while IFS= read -r entry; do
		case "${entry}" in
		"${package}-"*/desc) ;;
		*) continue ;;
		esac
		entry="${entry#"${package}-"}"
		entry="${entry%/desc}"
		[[ "${entry}" =~ ${PKGVER_PATTERN} ]] || continue
		if [ -n "${indexed}" ]; then
			fail "the ${architecture} database indexes ${package} twice, as ${indexed} and ${entry}"
		fi
		indexed="${entry}"
	done < <(bsdtar -tf "${database}")

	if [ -z "${indexed}" ]; then
		fail "the ${architecture} database holds no entry for ${package}; the registry stored the package without indexing it, so repair the registry before publishing again"
	fi
	if [ "${indexed}" != "${pkgver}" ]; then
		fail "the ${architecture} database indexes ${package} ${indexed}, not ${pkgver}; Forgejo indexes only the version it created most recently, so either a later release now owns this package or an interrupted publication left the database stale"
	fi

	description="$(bsdtar -xOf "${database}" "${package}-${pkgver}/desc")"
	signature="$(base64_of "${published_signature}")"
	for field in \
		"FILENAME=${filename}" \
		"NAME=${package}" \
		"VERSION=${pkgver}" \
		"ARCH=${architecture}" \
		"SHA256SUM=${built_digest}" \
		"PGPSIG=${signature}"; do
		if [ "$(desc_value "${description}" "${field%%=*}")" != "${field#*=}" ]; then
			fail "the ${architecture} database entry for ${package} ${pkgver} declares a ${field%%=*} this run did not publish"
		fi
	done
}

index=0
for candidate in "${packages[@]}"; do
	filename="${filenames[${index}]}"
	architecture="${package_architectures[${index}]}"
	index=$((index + 1))

	if ! code="$(curl "${curl_options[@]}" --output "${response}" --upload-file "${candidate}" "${upload_url}")"; then
		fail "uploading ${filename} did not complete"
	fi

	case "${code}" in
	201)
		# The registry accepted the upload. Reading it back proves the route
		# the metadata derives serves exactly the reviewed bytes, and that the
		# signature and the database a client depends on were written too.
		published_state "${filename}" "${architecture}" "${candidate}"
		printf 'published %s\n' "${filename}"
		;;
	409)
		# The registry already holds a package under this exact key, and it
		# answered before repairing anything it may have left half-published.
		# The same read-back decides: these bytes, fully indexed, or a failure
		# an operator resolves.
		published_state "${filename}" "${architecture}" "${candidate}"
		printf 'unchanged %s\n' "${filename}"
		;;
	*)
		# The response body is the registry's, not this run's: it can carry
		# workflow commands or reflected credentials, so only the status is
		# reported.
		fail "uploading ${filename} returned HTTP ${code}"
		;;
	esac
done
