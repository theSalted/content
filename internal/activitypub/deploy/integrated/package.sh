#!/bin/sh
set -eu
# Must be called from the source repository, with a new external output directory.
if [ "$#" -ne 1 ]; then echo 'Usage: package.sh /absolute/new-output-directory' >&2; exit 2; fi
case "$1" in /*) ;; *) echo 'Absolute output directory required' >&2; exit 2;; esac
content_root=$(CDPATH= cd -- "$(dirname "$0")/../../../.." && pwd)
module_root="$content_root/internal/activitypub"
output_dir="$1"
mkdir "$output_dir"
(cd "$content_root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$output_dir/content-delivery" ./cmd/content)
cp "$module_root/package.json" "$module_root/package-lock.json" "$output_dir/"
cp -R "$module_root/src" "$output_dir/src"
mkdir -p "$output_dir/deploy/integrated"
for name in entrypoint.ts delivery-child.mjs; do cp "$module_root/deploy/integrated/$name" "$output_dir/deploy/integrated/$name"; done
cp "$module_root/deploy/integrated/Dockerfile" "$output_dir/Dockerfile"
printf '%s\n' 'Prepared source and binary only. No environment files, credentials or operator tools copied.'
