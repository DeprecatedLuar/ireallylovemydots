#!/bin/bash

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

error() { echo -e "${RED}→ $1${NC}" >&2; exit 1; }
success() { echo -e "${GREEN}→ $1${NC}"; }
info() { echo -e "${BLUE}→ $1${NC}"; }
warn() { echo -e "${YELLOW}! $1${NC}"; }

# Paths
BIN_DIR="$HOME/.local/bin"
COMPLETION_DIR="$HOME/.local/share/bash-completion/completions"
DOTS_DIR="$HOME/.config/dots"

# GitHub repo info
REPO_USER="DeprecatedLuar"
REPO_NAME="ireallylovemydots"
BRANCH="main"
BASE_URL="https://raw.githubusercontent.com/$REPO_USER/$REPO_NAME/$BRANCH"

# Detect if running from cloned repo or remote install
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd 2>/dev/null || dirname "$0")"

if [[ -f "$SCRIPT_DIR/dots" ]]; then
    # Running from cloned repo (developer install)
    info "Installing from local repository..."

    mkdir -p "$BIN_DIR"
    mkdir -p "$COMPLETION_DIR"

    cp "$SCRIPT_DIR/dots" "$BIN_DIR/dots"
    chmod +x "$BIN_DIR/dots"

    cp "$SCRIPT_DIR/completions" "$COMPLETION_DIR/dots"

    success "Installed from local files"
else
    # Remote install - download directly from GitHub
    info "Downloading from GitHub..."

    # Check for curl or wget
    if command -v curl >/dev/null 2>&1; then
        DOWNLOAD="curl -fsSL"
    elif command -v wget >/dev/null 2>&1; then
        DOWNLOAD="wget -qO-"
    else
        error "Neither curl nor wget found. Please install one of them first."
    fi

    mkdir -p "$BIN_DIR"
    mkdir -p "$COMPLETION_DIR"

    # Download dots binary
    info "Downloading dots binary..."
    $DOWNLOAD "$BASE_URL/dots" > "$BIN_DIR/dots" || error "Failed to download dots binary"
    chmod +x "$BIN_DIR/dots"

    # Download completions
    info "Downloading bash completions..."
    $DOWNLOAD "$BASE_URL/completions" > "$COMPLETION_DIR/dots" || error "Failed to download completions"

    success "Downloaded and installed from GitHub"
fi

# Initialize dots repo if doesn't exist
if [[ ! -d "$DOTS_DIR" ]]; then
    info "Initializing dotfiles repo at $DOTS_DIR..."
    mkdir -p "$DOTS_DIR"
    cd "$DOTS_DIR"
    git init >/dev/null 2>&1
    mkdir -p config home
    success "Created dotfiles repo at $DOTS_DIR"
else
    info "Dotfiles repo already exists at $DOTS_DIR"
fi

echo ""
success "Whohoo, I guess we're finished. Try dots --help and hope it works"

# Check if ~/.local/bin is in PATH
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
    echo ""
    warn "~/.local/bin is not in your PATH. You may wanna check that"
    echo "Add this to your ~/.bashrc or ~/.bash_profile:"
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

# Check if bash-completion is available
if [[ -f /usr/share/bash-completion/bash_completion ]] || [[ -f /etc/bash_completion ]]; then
    echo ""
    info "Bash completions installed at $COMPLETION_DIR/dots"
    info "Reload your shell to activate: source ~/.bashrc"
else
    echo ""
    warn "bash-completion not detected on your system"
    echo "To enable tab completions, add this to your ~/.bashrc:"
    echo "  source $COMPLETION_DIR/dots"
fi

echo ""
cat << "EOF"
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⣀⣤⡴⠶⠿⠛⢏⡿⠖⠂⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⢀⡴⣞⠯⠉⠈⠀⣠⡶⠊⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⣠⣴⠟⠙⠈⣎⡹⠂⣴⠋⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⢠⣯⡟⢚⣀⠀⠀⠀⡰⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⡿⡃⢋⣌⠂⠈⠆⡠⡎⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⣸⢿⠎⢰⡈⠀⠈⠀⣹⠃⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⣸⣿⡄⠷⣠⠀⠀⠀⡸⡗⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⢹⣾⣿⣷⣛⠀⠀⠀⣜⣷⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠸⣿⢻⣏⠸⡃⠀⠀⠈⢹⢧⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⢻⣿⡮⣠⣗⠒⠤⠀⠀⠹⣳⣤⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⣼⢿⣗⣧⣦⢤⠇⢀⣤⣄⠙⣿⡦⣄⠀⠀⠀⠀⠀⠀⠀⠀⢀⣤⠄
⠀⠀⠈⠛⢏⣷⣶⣜⣤⣈⠂⠜⠀⢀⣀⡉⡭⠯⠖⠲⠒⢶⢖⣯⠟⠁⠀
⠀⠀⠀⠀⠀⠙⠻⣿⣿⣷⣷⣯⣔⡿⣃⠦⡵⣠⠠⢤⣤⠿⠋⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠉⠛⠓⠿⠽⣷⣿⣾⡿⠞⠛⠉⠀⠀⠀⠀⠀⠀⠀
EOF

echo ""
echo "  Now reload your shell or run: source ~/.bashrc"
echo "  THEN, you can try running \`dots setup <username/repo>\` to connect your GitHub"
echo "  SO THEN, you can try: dots snatch <config> to send it to the source control"
echo ""
