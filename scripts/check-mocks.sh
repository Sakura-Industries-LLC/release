#!/usr/bin/env bash
#
# Prove checked-in Mockery output matches the pinned generator.
# Snapshots existing generated mocks, regenerates in place with the
# pinned mockery, diffs bytes against the snapshot, then restores the
# tree. The compared set is whatever mockery leaves under
# internal/adapter/*/mocks/*.go after generation (except hand-maintained
# doc.go), keyed by full relative path so same-named files in different
# adapters cannot collide.

set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd -- "$root"

die() { printf 'check-mocks: %s\n' "$*" >&2; exit 1; }

pin="$(sed -n 's/^"aqua:vektra\/mockery" = "\([^"]*\)"$/\1/p' mise.toml)"
pin_count="$(printf '%s\n' "$pin" | grep -c . || true)"
if [ "$pin_count" -ne 1 ]; then
	die "expected exactly one aqua:vektra/mockery pin in mise.toml, found ${pin_count}"
fi
expected="v${pin}"

version="$(mise exec -- mockery version | tr -d '[:space:]')"
if [ "$version" != "$expected" ]; then
	die "expected mockery ${expected}, got ${version:-<empty>}"
fi

list_generated() {
	find internal/adapter -path 'internal/adapter/*/mocks/*.go' ! -name doc.go -type f | LC_ALL=C sort
}

tmp="$(mktemp -d)"
restore() {
	status=$?
	if [ -d "${tmp}/tree" ]; then
		# Restore snapshotted files and drop anything mockery created.
		find "${tmp}/tree" -type f -print0 | while IFS= read -r -d '' staged; do
			rel="${staged#"${tmp}/tree/"}"
			mkdir -p "$(dirname "$rel")"
			cp "$staged" "$rel"
		done
		list_generated | while IFS= read -r path; do
			if [ ! -f "${tmp}/tree/${path}" ]; then
				rm -f "$path"
			fi
		done
	fi
	rm -rf "$tmp"
	exit "$status"
}
trap restore EXIT

mkdir -p "${tmp}/tree"
list_generated | while IFS= read -r path; do
	mkdir -p "${tmp}/tree/$(dirname "$path")"
	cp "$path" "${tmp}/tree/${path}"
done

mise exec -- mockery

mapfile -t generated < <(list_generated)
if [ "${#generated[@]}" -eq 0 ]; then
	die "mockery produced no generated mock files under internal/adapter/*/mocks/"
fi

failed=0
for path in "${generated[@]}"; do
	staged="${tmp}/tree/${path}"
	if [ ! -f "$staged" ]; then
		printf 'check-mocks: mockery wrote %s but it was not present in the tree\n' "$path" >&2
		failed=1
		continue
	fi
	if ! cmp -s "$path" "$staged"; then
		printf 'check-mocks: %s does not match mockery %s output\n' "$path" "$expected" >&2
		diff -u "$staged" "$path" >&2 || true
		failed=1
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi
