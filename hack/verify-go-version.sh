#!/usr/bin/env bash

# This script verifies that the Go version meets the minimum requirement
# specified in go.mod.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

GO_VERSION_REQ=$(grep '^go ' "${REPO_ROOT}/go.mod" | awk '{print $2}')

if [[ -z "${GO_VERSION_REQ}" ]]; then
    echo "ERROR: Could not determine required Go version from go.mod" >&2
    exit 1
fi

GO_VERSION_CURRENT=$(go version | awk '{print $3}' | sed 's/go//')

echo "checking go version..."
echo "  required: >= ${GO_VERSION_REQ}"
echo "  current:  ${GO_VERSION_CURRENT}"

version_gte() {
    # Returns 0 if $1 >= $2
    printf '%s\n%s\n' "$2" "$1" | sort -V -C
}

if version_gte "${GO_VERSION_CURRENT}" "${GO_VERSION_REQ}"; then
    echo "go version check passed!"
else
    echo "ERROR: go version ${GO_VERSION_CURRENT} is less than required ${GO_VERSION_REQ}" >&2
    echo "please upgrade Go: https://golang.org/dl/" >&2
    exit 1
fi
