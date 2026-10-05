#!/bin/sh
set -eu

snapshot_dir=$(mktemp -d)
trap 'rm -rf "$snapshot_dir"' EXIT

if [ -d docs ]; then
	cp -R docs "$snapshot_dir/docs"
else
	mkdir "$snapshot_dir/docs"
fi
cp -R internal/asc "$snapshot_dir/asc"

# The vendored specs must be the exact ASC artifacts SOURCE.md records.
for pkg in internal/asc/infra internal/asc/uam; do
	want=$(sed -n 's/^- sha256: `\([0-9a-f]*\)`$/\1/p' "$pkg/SOURCE.md")
	got=$(shasum -a 256 "$pkg/openapi.json" | cut -d' ' -f1)
	if [ -z "$want" ] || [ "$want" != "$got" ]; then
		echo "$pkg/openapi.json sha256 $got does not match SOURCE.md ($want)" >&2
		exit 1
	fi
done

go generate ./...

if ! diff -ru "$snapshot_dir/docs" docs; then
	echo "generated documentation is stale; run 'make generate'" >&2
	exit 1
fi

if ! diff -ru "$snapshot_dir/asc" internal/asc; then
	echo "generated ASC clients are stale; run 'make generate'" >&2
	exit 1
fi
