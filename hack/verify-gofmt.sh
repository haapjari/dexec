#!/usr/bin/env bash

# This script checks if Go source files are properly formatted.
# Run update-gofmt.sh to fix any issues.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

cd "${REPO_ROOT}"

echo "checking gofmt..."

# find all Go files, excluding generated and vendor directories
find_files() {
    find . -not \( \
        \( \
            -wholename './.git' \
            -o -wholename './_output' \
            -o -wholename './vendor/*' \
            -o -wholename './testdata/*' \
        \) -prune \
    \) -name '*.go'
}

# check for formatting issues
diff=$(find_files | xargs gofmt -d -s 2>&1) || true

if [[ -n "${diff}" ]]; then
    echo "ERROR: some files are not properly formatted:" >&2
    echo "${diff}" >&2
    echo "" >&2
    echo "run 'make update-gofmt' to fix formatting issues." >&2
    exit 1
fi

echo "gofmt check passed!"
