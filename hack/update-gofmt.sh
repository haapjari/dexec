#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TOOLS_DIR="${REPO_ROOT}/_output/tools"

cd "${REPO_ROOT}"

echo "formatting code..."

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

echo "running gofmt..."
find_files | xargs gofmt -w -s

# format with goimports if available
if [[ -x "${TOOLS_DIR}/goimports" ]]; then
    echo "running goimports..."
    find_files | xargs "${TOOLS_DIR}/goimports" -w -local github.com/haapjari/dexec
else
    echo "goimports not found, run 'make tools' to install it"
fi

# format with gofumpt if available
if [[ -x "${TOOLS_DIR}/gofumpt" ]]; then
    echo "running gofumpt..."
    find_files | xargs "${TOOLS_DIR}/gofumpt" -w -extra
else
    echo "gofumpt not found, run 'make tools' to install it"
fi

# format with golines if available
if [[ -x "${TOOLS_DIR}/golines" ]]; then
    echo "running golines..."
    find_files | xargs "${TOOLS_DIR}/golines" -w --max-len=80
else
    echo "golines not found, run 'make tools' to install it"
fi

echo "formatting complete!"
