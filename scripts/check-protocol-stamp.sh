#!/usr/bin/env bash
#
# Guard the two hand-moved release-unit stamps that Release Please does
# not keep in lockstep automatically.
#
# Release Please stamps DEFAULT_VERSION only while the annotated
# x-release-please-start-version / x-release-please-end markers remain
# around that line. Deleting the markers leaves DEFAULT_VERSION stuck
# and every installed acquisition path downloads the wrong tag.
#
# The protocol integer is a source literal in both the action
# (EXPECTED_PROTOCOL) and Go (Protocol); extra-files stamps the version
# only, so this check is the CI guard that the two protocol literals
# stay equal.
#

set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
action="${root}/.github/actions/setup-release-cli/action.yml"
cli_dir="${root}/internal/cli"

die() {
	printf '%s\n' "$*" >&2
	exit 1
}

# matching_lines prints 1-based line numbers of lines in FILE matching PATTERN.
matching_lines() {
	# grep exits 1 when there are no matches; keep going.
	grep -n -E "$1" "$2" | sed -n 's/^\([0-9][0-9]*\):.*/\1/p' || true
}

# count_lines returns the number of newline-delimited items in TEXT.
count_lines() {
	if [ -z "${1:-}" ]; then
		printf '0\n'
		return 0
	fi
	printf '%s\n' "$1" | grep -c .
}

if [ ! -f "$action" ]; then
	die "check-protocol-stamp: missing ${action}"
fi

if [ ! -d "$cli_dir" ]; then
	die "check-protocol-stamp: missing ${cli_dir}"
fi

start_lines="$(matching_lines '^[[:space:]]*#[[:space:]]*x-release-please-start-version([[:space:]]|$)' "$action")"
end_lines="$(matching_lines '^[[:space:]]*#[[:space:]]*x-release-please-end([[:space:]]|$)' "$action")"
start_count="$(count_lines "$start_lines")"
end_count="$(count_lines "$end_lines")"

if [ "$start_count" -eq 0 ]; then
	die "check-protocol-stamp: missing # x-release-please-start-version in ${action}"
fi
if [ "$end_count" -eq 0 ]; then
	die "check-protocol-stamp: missing # x-release-please-end in ${action}"
fi
if [ "$start_count" -ne 1 ]; then
	die "check-protocol-stamp: expected exactly one # x-release-please-start-version in ${action}, found ${start_count}"
fi
if [ "$end_count" -ne 1 ]; then
	die "check-protocol-stamp: expected exactly one # x-release-please-end in ${action}, found ${end_count}"
fi

start_line="$start_lines"
end_line="$end_lines"
if [ "$start_line" -ge "$end_line" ]; then
	die "check-protocol-stamp: # x-release-please-start-version (line ${start_line}) must precede # x-release-please-end (line ${end_line}) in ${action}"
fi

version_lines="$(matching_lines '^[[:space:]]*DEFAULT_VERSION:' "$action")"
enclosed=""
if [ -n "$version_lines" ]; then
	while IFS= read -r lineno; do
		if [ "$lineno" -gt "$start_line" ] && [ "$lineno" -lt "$end_line" ]; then
			enclosed="${enclosed}${enclosed:+
}${lineno}"
		fi
	done <<EOF
${version_lines}
EOF
fi

enclosed_count="$(count_lines "$enclosed")"
if [ "$enclosed_count" -eq 0 ]; then
	die "check-protocol-stamp: # x-release-please-start-version / # x-release-please-end must enclose exactly one DEFAULT_VERSION: line in ${action}"
fi
if [ "$enclosed_count" -ne 1 ]; then
	die "check-protocol-stamp: expected exactly one DEFAULT_VERSION: line between release-please markers in ${action}, found ${enclosed_count}"
fi

printf 'action version stamp markers ok (DEFAULT_VERSION at line %s)\n' "$enclosed"

action_proto="$(
	sed -n 's/^[[:space:]]*EXPECTED_PROTOCOL:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$action"
)"
if [ -z "$action_proto" ]; then
	die "check-protocol-stamp: EXPECTED_PROTOCOL not found in ${action}"
fi
action_count="$(printf '%s\n' "$action_proto" | grep -c .)"
if [ "$action_count" -ne 1 ]; then
	die "check-protocol-stamp: expected exactly one EXPECTED_PROTOCOL in ${action}, found ${action_count}"
fi

go_matches=""
for src in "${cli_dir}"/*.go; do
	[ -f "$src" ] || continue
	case "$src" in
	*_test.go) continue ;;
	esac
	found="$(
		grep -E '^[[:space:]]*(const[[:space:]]+)?Protocol[[:space:]]+(int[[:space:]]+)?=[[:space:]]*[0-9]+' "$src" \
			| sed -n 's/.*=[[:space:]]*\([0-9][0-9]*\).*/\1/p' || true
	)"
	if [ -n "$found" ]; then
		go_matches="${go_matches}${go_matches:+
}${found}"
	fi
done

if [ -z "$go_matches" ]; then
	die "check-protocol-stamp: Protocol constant not found in ${cli_dir}"
fi
go_count="$(printf '%s\n' "$go_matches" | grep -c .)"
if [ "$go_count" -ne 1 ]; then
	die "check-protocol-stamp: expected exactly one Protocol constant in ${cli_dir}, found ${go_count}"
fi
go_proto="$go_matches"

printf 'action EXPECTED_PROTOCOL=%s\n' "$action_proto"
printf 'source Protocol=%s\n' "$go_proto"

if [ "$action_proto" != "$go_proto" ]; then
	die "check-protocol-stamp: protocol mismatch: action EXPECTED_PROTOCOL=${action_proto}, source Protocol=${go_proto}"
fi
