#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

cd "${REPO_ROOT}"

echo "checking go.mod..."

TMPDIR=$(mktemp -d)
trap "rm -rf ${TMPDIR}" EXIT

cp go.mod "${TMPDIR}/go.mod"
if [[ -f go.sum ]]; then
    cp go.sum "${TMPDIR}/go.sum"
fi

go mod tidy

if ! diff -q go.mod "${TMPDIR}/go.mod" > /dev/null 2>&1; then
    echo "ERROR: go.mod is not tidy" >&2
    echo "" >&2
    diff -u "${TMPDIR}/go.mod" go.mod >&2 || true
    echo "" >&2
    echo "Run 'make update-gomod' to fix this." >&2
    cp "${TMPDIR}/go.mod" go.mod
    exit 1
fi

if [[ -f "${TMPDIR}/go.sum" ]]; then
    if ! diff -q go.sum "${TMPDIR}/go.sum" > /dev/null 2>&1; then
        echo "ERROR: go.sum is not tidy" >&2
        echo "" >&2
        echo "Run 'make update-gomod' to fix this." >&2
        # Restore original
        cp "${TMPDIR}/go.sum" go.sum
        exit 1
    fi
fi

echo "go.mod check passed!"
