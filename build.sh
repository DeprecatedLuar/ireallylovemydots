#!/usr/bin/env bash
# Builds the dots binary at the repository root, stamping main.version with
# `git describe --always --dirty` so `dots --version` reports the commit (and
# dirty state) this exact binary was built from.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

version=$(git describe --always --dirty)

go build -ldflags "-X main.version=${version}" -o dotz ./cmd/dotz
