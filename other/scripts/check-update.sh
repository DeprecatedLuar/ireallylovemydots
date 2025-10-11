#!/bin/bash
# Quick update checker - executed via curl, no dependencies on main script

REPO="DeprecatedLuar/ireallylovemydots"
CURRENT_VERSION="$1"

# Semantic version comparison (returns 0 if v1 < v2, 1 if v1 >= v2)
version_lt() {
    local v1="$1" v2="$2"

    # Split versions into major.minor.patch
    IFS='.' read -r -a ver1 <<< "$v1"
    IFS='.' read -r -a ver2 <<< "$v2"

    # Compare major
    [[ ${ver1[0]:-0} -lt ${ver2[0]:-0} ]] && return 0
    [[ ${ver1[0]:-0} -gt ${ver2[0]:-0} ]] && return 1

    # Compare minor
    [[ ${ver1[1]:-0} -lt ${ver2[1]:-0} ]] && return 0
    [[ ${ver1[1]:-0} -gt ${ver2[1]:-0} ]] && return 1

    # Compare patch
    [[ ${ver1[2]:-0} -lt ${ver2[2]:-0} ]] && return 0

    return 1
}

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

# Strip 'v' prefix for comparison
current="${CURRENT_VERSION#v}"
remote="${latest_tag#v}"

# Only notify if remote version is NEWER than current
if version_lt "$current" "$remote"; then
    echo "UPDATE_AVAILABLE:$latest_tag"
    exit 0
else
    echo "UP_TO_DATE"
    exit 1
fi
