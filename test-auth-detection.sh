#!/bin/bash

# Test script to simulate auth detection logic
# Run with: ./test-auth-detection.sh [scenario]
# Scenarios: gh, ssh, none, current

scenario="${1:-current}"

echo "Testing authentication detection..."
echo ""

test_auth_detection() {
    local has_gh="$1"
    local has_ssh="$2"

    if [[ "$has_gh" == "true" ]]; then
        echo "✓ gh CLI detected → Would use: https://github.com/user/repo.git"
    elif [[ "$has_ssh" == "true" ]]; then
        echo "✓ SSH keys detected → Would use: git@github.com:user/repo.git"
        echo "  (with note: ensure they're added to GitHub)"
    else
        echo "✗ No auth detected → Would fail with instructions:"
        echo ""
        echo "  No GitHub authentication configured!"
        echo ""
        echo "  Option 1: Install gh CLI + gh auth login"
        echo "  Option 2: Set up SSH keys"
    fi
}

case "$scenario" in
    gh)
        echo "=== Scenario 1: gh CLI authenticated ==="
        test_auth_detection "true" "false"
        ;;
    ssh)
        echo "=== Scenario 2: SSH keys only ==="
        test_auth_detection "false" "true"
        ;;
    none)
        echo "=== Scenario 3: No authentication ==="
        test_auth_detection "false" "false"
        ;;
    current)
        echo "=== Current System State ==="

        # Check actual system
        if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
            test_auth_detection "true" "false"
        elif [[ -f ~/.ssh/id_rsa ]] || [[ -f ~/.ssh/id_ed25519 ]] || [[ -f ~/.ssh/id_ecdsa ]]; then
            test_auth_detection "false" "true"
        else
            test_auth_detection "false" "false"
        fi
        ;;
    all)
        echo "=== Scenario 1: gh CLI authenticated ==="
        test_auth_detection "true" "false"
        echo ""
        echo "=== Scenario 2: SSH keys only ==="
        test_auth_detection "false" "true"
        echo ""
        echo "=== Scenario 3: No authentication ==="
        test_auth_detection "false" "false"
        ;;
    *)
        echo "Usage: $0 [gh|ssh|none|current|all]"
        exit 1
        ;;
esac
