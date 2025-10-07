#!/bin/bash
# Quick update checker - executed via curl, no dependencies on main script

REPO="DeprecatedLuar/ireallylovemydots"
CURRENT_VERSION="$1"

# Fetch latest tag
latest_tag=$(curl -sS --max-time 3 \
    "https://api.github.com/repos/$REPO/releases/latest" \
    2>/dev/null | grep '"tag_name"' | cut -d'"' -f4)

# Fallback to tags if no releases
[[ -z "$latest_tag" ]] && latest_tag=$(curl -sS --max-time 3 \
    "https://api.github.com/repos/$REPO/tags" \
    2>/dev/null | grep '"name"' | head -1 | cut -d'"' -f4)

# No tags found
[[ -z "$latest_tag" ]] && exit 2

# Compare versions
current="${CURRENT_VERSION#v}"
remote="${latest_tag#v}"

# Output: "UPDATE_AVAILABLE" or "UP_TO_DATE" or "ERROR"
if [[ "$remote" != "$current" ]]; then
    echo "UPDATE_AVAILABLE:$latest_tag"
    exit 0
else
    echo "UP_TO_DATE"
    exit 1
fi
