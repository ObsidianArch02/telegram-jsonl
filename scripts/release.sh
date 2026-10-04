#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

validate_tag() {
  local tag=${RELEASE_TAG:?Set RELEASE_TAG to a version such as v0.1.0}
  local identifier
  if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
    printf 'Invalid release tag: %s\n' "$tag" >&2
    exit 1
  fi
  if [[ "$tag" == *-* ]]; then
    local identifiers=${tag#*-}
    local parts=()
    IFS=. read -r -a parts <<< "$identifiers"
    for identifier in "${parts[@]}"; do
      if [[ "$identifier" =~ ^[0-9]+$ && "$identifier" =~ ^0[0-9]+$ ]]; then
        printf 'Numeric prerelease identifiers must not have leading zeros.\n' >&2
        exit 1
      fi
    done
  fi
}

validate_tag
dist_dir=${DIST_DIR:-dist}

case "${1:-}" in
  validate)
    ;;
  build)
    target="${GOOS:?Set GOOS}/${GOARCH:?Set GOARCH}"
    case "$target" in
      linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;;
      *) printf 'Unsupported target: %s\n' "$target" >&2; exit 1 ;;
    esac
    commit=${BUILD_COMMIT:-$(git rev-parse HEAD)}
    if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
      printf 'BUILD_COMMIT must be a full Git commit hash.\n' >&2
      exit 1
    fi
    name="telegram-jsonl_${RELEASE_TAG}_${GOOS}_${GOARCH}"
    stage=$(mktemp -d)
    trap 'rm -rf "$stage"' EXIT
    mkdir -p "$stage/$name" "$dist_dir"
    binary=telegram-jsonl
    if [[ "$GOOS" == windows ]]; then
      binary+=.exe
    fi
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${RELEASE_TAG#v} -X main.commit=$commit" \
      -o "$stage/$name/$binary" .
    for file in LICENSE README.md; do
      cp "$file" "$stage/$name/"
    done
    for file in NOTICE README.zh-CN.md DISCLAIMER.md DISCLAIMER.zh-CN.md THIRD_PARTY_NOTICES.md THIRD_PARTY_NOTICES.zh-CN.md CONTRIBUTING.md CONTRIBUTING.zh-CN.md SECURITY.md SECURITY.zh-CN.md go.mod go.sum; do
      if [[ -f "$file" ]]; then
        cp "$file" "$stage/$name/"
      fi
    done
    if [[ -d docs ]]; then
      mkdir -p "$stage/$name/docs"
      # Only tracked documentation is eligible for release packages.
      git archive HEAD docs | tar -x -C "$stage/$name"
    fi
    git archive HEAD licenses | tar -x -C "$stage/$name"
    if [[ "$GOOS" == windows ]]; then
      destination=$(cd "$dist_dir" && pwd)
      (cd "$stage" && zip -qr "$destination/$name.zip" "$name")
    else
      tar -czf "$dist_dir/$name.tar.gz" -C "$stage" "$name"
    fi
    ;;
  source)
    mkdir -p "$dist_dir"
    git archive --format=tar.gz \
      --prefix="telegram-jsonl_${RELEASE_TAG}_source/" \
      --output="$dist_dir/telegram-jsonl_${RELEASE_TAG}_source.tar.gz" HEAD
    ;;
  *)
    printf 'Usage: bash scripts/release.sh {validate|build|source}\n' >&2
    exit 2
    ;;
esac
