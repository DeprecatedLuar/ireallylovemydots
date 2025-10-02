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
TOOL_DIR="$HOME/.config/ireallylovemydots"
BIN_DIR="$HOME/.local/bin"
DOTS_DIR="$HOME/.config/dots"

# Detect if running from cloned repo or one-liner
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd 2>/dev/null || dirname "$0")"

if [[ -f "$SCRIPT_DIR/bin/dots" ]]; then
    # Running from cloned repo
    info "Installing from local repository..."
    SOURCE_DIR="$SCRIPT_DIR"
    CLEANUP_TEMP=false
else
    # One-liner install - clone the repo
    REPO_URL="https://github.com/DeprecatedLuar/ireallylovemydots.git"  
    TEMP_DIR=$(mktemp -d)

    info "Cloning repository from $REPO_URL..."
    if ! command -v git >/dev/null 2>&1; then
        error "git is required but not installed. Please install git first."
    fi

    git clone --quiet "$REPO_URL" "$TEMP_DIR" || error "Failed to clone repository"
    SOURCE_DIR="$TEMP_DIR"
    CLEANUP_TEMP=true

    # Cleanup temp dir on exit
    trap "rm -rf $TEMP_DIR" EXIT
fi

# Ensure directories exist
mkdir -p "$BIN_DIR"

# Copy tool to ~/.config/ireallylovemydots and set permissions
mkdir -p "$TOOL_DIR"
cp -r "$SOURCE_DIR"/* "$TOOL_DIR/"
chmod +x "$TOOL_DIR/bin/dots"

# Symlink binary to PATH
info "Symlinking to your PATH: $BIN_DIR/dots -> $TOOL_DIR/bin/dots"
ln -sf "$TOOL_DIR/bin/dots" "$BIN_DIR/dots"

# Install completions
if [[ -f /usr/share/bash-completion/bash_completion ]] || [[ -f /etc/bash_completion ]]; then
    COMPLETION_DIR="$HOME/.local/share/bash-completion/completions"
    mkdir -p "$COMPLETION_DIR"
    info "Symlinking completions to $COMPLETION_DIR/dots"
    ln -sf "$TOOL_DIR/lib/completions.sh" "$COMPLETION_DIR/dots"
    success "Completions installed (reload shell to activate)"
else
    info "bash-completion not detected"
    echo "To enable tab completions, just paste this in your ~/.bashrc:"
    echo "  source $TOOL_DIR/lib/completions.sh"
fi

# Initialize dots repo if doesn't exist
if [[ ! -d "$DOTS_DIR" ]]; then
    info "Initializing dotfiles repo at $DOTS_DIR..."
    mkdir -p "$DOTS_DIR"
    cd "$DOTS_DIR"
    git init
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

echo ""
cat << "EOF"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
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
echo "  Now Reload your shell or run: source ~/.bashrc"
echo "  THEN, you can try running \`dots setup <username/repo>\` to connect your GitHub"
echo "  SO THEN, you can try: dots snatch <config> to send it to the source control"
echo ""
