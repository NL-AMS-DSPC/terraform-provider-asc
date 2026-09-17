#!/bin/sh
set -eu

snapshot_dir=$(mktemp -d)
trap 'rm -rf "$snapshot_dir"' EXIT

if [ -d docs ]; then
	cp -R docs "$snapshot_dir/docs"
else
	mkdir "$snapshot_dir/docs"
fi

go generate ./...

if ! diff -ru "$snapshot_dir/docs" docs; then
	echo "generated documentation is stale; run 'make generate'" >&2
	exit 1
fi

