#!/bin/bash

# ===== CONFIGURATION =====
PROJECT_NAME="I Really Love My Dots"
REPO_USER="DeprecatedLuar"
REPO_NAME="ireallylovemydots"
BRANCH="main"

SCRIPT_FILES=("dots") # Files to install (script names without path)
COMPLETION_FILES=("completions")  # Optional: ("completions")

# Installation directory
INSTALL_DIR="$HOME/.local/bin"
COMPLETION_DIR="$HOME/.local/share/bash-completion/completions"

# Custom messages override
MSG_REMOTE_INSTALL="Downloading from GitHub..."
MSG_INSTALL_COMPLETE="Yippie!! I guess we're finished."
MSG_TRY_COMMAND="Try running: ${SCRIPT_FILES[0]} --help"
MSG_NO_CURL_WGET="Neither curl nor wget found. Please install one of them first."
NEXT_STEPS=(
    " Try dots --help and hope it works"
    "Then: dots setup <username/repo> to connect your GitHub"
    "Or:  dots snatch <config> to start tracking configs locally"
)

ASCII_ART='                .     .  . ..
                @@@@@@@@@@@@@@=  ..
            @@@@@@@@@@@@@@@@@@@@@@.
         @@@@@@@@@@@@@@@@@@@@@@@@@@@..
       @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
      @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
     @@@@@@@@@@@@@@@@@@@@@@@*...@@@@@@@@
     @@@@@@-     ..:@@@@@@:      . @@@@@
     @@@@@@  @@     -@@@@:  @@*    @@@@@*
     @@@@@@   .      @@@@* . .  .  @@@@@@
    +@@@@@@@ .      @@@@@@:      *@@@@@@@
   .@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
      @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
       +@@@@@@@@@@@@. . . #@@@@@@@@@@
        .:@@@@@@@@@@@@@@@@@@@@@@@@+.
            @@@@@@@@@@@@@@@@:.    . .
              .  .      ..   @@@@@@@@@@@@.
                 @@@@@@@@@@@@@@@@@@@@@@@@@
                +@@@@@@@@@@@@@@@@@@@@@@@@@.
                 @@@@@@@@@@@@@@@@@@@@@@@@@
               . @@@@@@: *@@@@%. .   @@@@@.
                 @@@@@@* -@@@@.@@@*  @@@@@.
                 .@@@@@@ +@@@@. @@=  @@@@+
                 .@@@@@@  @@@@.  -    @@%
                 . @@@@    @@@         .
                  .          .                   ' # ASCII art (leave empty to disable)

# ===== END CONFIGURATION =====

set -e

# ===========================================
# MESSAGES LIBRARY
# ===========================================

# Color detection: disable colors if not outputting to a terminal
if [ -t 1 ] && [ -n "$TERM" ]; then
    BLUE='\033[0;34m'
    CYAN='\033[0;36m'
    GREEN='\033[0;32m'
    RED='\033[0;31m'
    YELLOW='\033[1;33m'
    NC='\033[0m'
else
    BLUE=''
    CYAN=''
    GREEN=''
    RED=''
    YELLOW=''
    NC=''
fi

action() {
    echo -e "${BLUE}→${NC} $1"
}

info() {
    echo -e "${CYAN}ℹ${NC} $1"
}

success() {
    echo -e "${GREEN}✓ $1${NC}"
}

error() {
    echo -e "${RED}✗ $1${NC}" >&2
    exit 1
}

warn() {
    echo -e "${YELLOW}! $1${NC}"
}

separator() {
    local text="$1"
    local width=$(tput cols 2>/dev/null || echo 80)
    local text_length=$((${#text} + 4))
    local dash_count=$((width - text_length))

    if [ $dash_count -lt 0 ]; then
        dash_count=0
    fi

    local dashes=$(printf '%*s' "$dash_count" '' | tr ' ' '-')
    echo -e "${BLUE}-- ${text} ${dashes}${NC}"
}

# ===========================================
# OS DETECTION LIBRARY
# ===========================================

detect_os() {
    local os_type
    os_type=$(uname -s | tr '[:upper:]' '[:lower:]')

    case "$os_type" in
        linux*)
            echo "linux"
            ;;
        darwin*)
            echo "darwin"
            ;;
        mingw* | msys* | cygwin*)
            echo "windows"
            ;;
        freebsd*)
            echo "freebsd"
            ;;
        openbsd*)
            echo "openbsd"
            ;;
        netbsd*)
            echo "netbsd"
            ;;
        *)
            echo "unknown"
            ;;
    esac
}

detect_arch() {
    local arch
    arch=$(uname -m)

    case "$arch" in
        x86_64 | x86-64 | x64 | amd64)
            echo "amd64"
            ;;
        aarch64 | arm64)
            echo "arm64"
            ;;
        armv7* | armv8l)
            echo "arm"
            ;;
        armv6*)
            echo "armv6"
            ;;
        i386 | i686)
            echo "386"
            ;;
        *)
            echo "$arch"
            ;;
    esac
}

is_nixos() {
    [[ -f /etc/NIXOS ]]
}

parse_os_release() {
    local key="$1"
    local value=""

    if [[ -f /etc/os-release ]]; then
        value=$(grep -E "^${key}=" /etc/os-release | cut -d= -f2- | tr -d '"')
    fi

    echo "$value"
}

detect_distro() {
    local os="$1"

    if [[ "$os" != "linux" ]]; then
        echo "none"
        return
    fi

    if is_nixos; then
        echo "nixos"
        return
    fi

    local distro_id
    distro_id=$(parse_os_release "ID")

    if [[ -n "$distro_id" ]]; then
        echo "$distro_id"
    else
        echo "unknown"
    fi
}

detect_distro_family() {
    local distro="$1"

    case "$distro" in
        nixos)
            echo "nixos"
            ;;
        ubuntu | debian | pop | linuxmint | raspbian)
            echo "debian"
            ;;
        arch | manjaro | endeavouros)
            echo "arch"
            ;;
        fedora | rhel | centos | rocky | alma)
            echo "rhel"
            ;;
        alpine)
            echo "alpine"
            ;;
        gentoo)
            echo "gentoo"
            ;;
        opensuse* | sles)
            echo "suse"
            ;;
        *)
            echo "unknown"
            ;;
    esac
}

detect_distro_version() {
    local os="$1"

    if [[ "$os" != "linux" ]]; then
        echo ""
        return
    fi

    parse_os_release "VERSION_ID"
}

detect_kernel() {
    uname -r
}

get_system_info() {
    local os arch distro distro_family distro_version kernel

    os=$(detect_os)
    arch=$(detect_arch)
    distro=$(detect_distro "$os")
    distro_family=$(detect_distro_family "$distro")
    distro_version=$(detect_distro_version "$os")
    kernel=$(detect_kernel)

    cat <<EOF
{
  "os": "$os",
  "arch": "$arch",
  "distro": "$distro",
  "distro_family": "$distro_family",
  "distro_version": "$distro_version",
  "kernel": "$kernel"
}
EOF
}

# ===========================================
# PATH MANAGEMENT LIBRARY
# ===========================================

ensure_in_path() {
    local install_dir="$1"

    # Check if already in PATH
    if [[ ":$PATH:" == *":$install_dir:"* ]]; then
        return 0
    fi

    echo ""
    warn "$install_dir is not in your PATH"
    echo ""

    # Detect OS
    local system_info=$(get_system_info)
    local distro=$(echo "$system_info" | grep -o '"distro":"[^"]*"' | cut -d'"' -f4)

    # NixOS special handling
    if [[ "$distro" == "nixos" ]]; then
        handle_nixos_path "$install_dir"
        return 0
    fi

    # Detect shell
    local user_shell=$(basename "$SHELL")
    local rc_file=""

    case "$user_shell" in
        bash)
            rc_file="$HOME/.bashrc"
            ;;
        zsh)
            rc_file="$HOME/.zshrc"
            ;;
        fish)
            rc_file="$HOME/.config/fish/config.fish"
            ;;
        *)
            warn "Unknown shell: $user_shell"
            info "Add this to your shell config manually:"
            echo "  export PATH=\"$install_dir:\$PATH\""
            return 1
            ;;
    esac

    # Create rc file if it doesn't exist
    if [[ ! -f "$rc_file" ]]; then
        touch "$rc_file"
    fi

    # Check if PATH export already exists
    if grep -q "$install_dir" "$rc_file" 2>/dev/null; then
        info "PATH export already in $rc_file"
        info "Reload your shell: source $rc_file"
        return 0
    fi

    # Add to rc file
    echo "" >> "$rc_file"
    echo "# Added by installer" >> "$rc_file"
    echo "export PATH=\"$install_dir:\$PATH\"" >> "$rc_file"

    success "Added $install_dir to PATH in $rc_file"
    info "Reload your shell: source $rc_file"
}

handle_nixos_path() {
    local install_dir="$1"

    echo "NixOS detected. Choose installation method:"
    echo ""
    echo "  1) Quick way - Add to .bashrc (works immediately)"
    echo "  2) NixOS way - Use declarative configuration (proper NixOS style)"
    echo ""
    read -p "Choice [1/2]: " choice

    case "$choice" in
        1)
            # Add to .bashrc even on NixOS
            local rc_file="$HOME/.bashrc"
            if [[ ! -f "$rc_file" ]]; then
                touch "$rc_file"
            fi

            if grep -q "$install_dir" "$rc_file" 2>/dev/null; then
                info "PATH export already in $rc_file"
            else
                echo "" >> "$rc_file"
                echo "# Added by installer" >> "$rc_file"
                echo "export PATH=\"$install_dir:\$PATH\"" >> "$rc_file"
                success "Added $install_dir to PATH in $rc_file"
            fi
            info "Reload your shell: source $rc_file"
            ;;
        2)
            # Show declarative instructions
            echo ""
            info "For declarative configuration, add to your home-manager config:"
            echo ""
            echo "  home.sessionPath = [ \"$install_dir\" ];"
            echo ""
            info "Or in configuration.nix (system-wide):"
            echo ""
            echo "  environment.sessionVariables = {"
            echo "    PATH = [ \"$install_dir\" ];"
            echo "  };"
            echo ""
            info "Then run: nixos-rebuild switch"
            echo ""
            ;;
        *)
            error "Invalid choice. Exiting."
            ;;
    esac
}

# ===========================================
# MAIN INSTALLATION LOGIC
# ===========================================

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd 2>/dev/null || dirname "$0")"

# separator "Installing $PROJECT_NAME"

# Detect if running from cloned repo or remote install
LOCAL_INSTALL=false
for script in "${SCRIPT_FILES[@]}"; do
    if [[ -f "$SCRIPT_DIR/$script" ]]; then
        LOCAL_INSTALL=true
        break
    fi
done

# Create installation directories
mkdir -p "$INSTALL_DIR"
if [ ${#COMPLETION_FILES[@]} -gt 0 ]; then
    mkdir -p "$COMPLETION_DIR"
fi

if [ "$LOCAL_INSTALL" = true ]; then
    # Local install - copy files
    for script in "${SCRIPT_FILES[@]}"; do
        # Remove existing file/symlink if exists
        [[ -e "$INSTALL_DIR/$script" ]] || [[ -L "$INSTALL_DIR/$script" ]] && rm -f "$INSTALL_DIR/$script"

        cp "$SCRIPT_DIR/$script" "$INSTALL_DIR/$script"
        chmod +x "$INSTALL_DIR/$script"
    done

    # Install completions
    for completion in "${COMPLETION_FILES[@]}"; do
        [[ -e "$COMPLETION_DIR/$completion" ]] || [[ -L "$COMPLETION_DIR/$completion" ]] && rm -f "$COMPLETION_DIR/$completion"
        cp "$SCRIPT_DIR/$completion" "$COMPLETION_DIR/$completion"
    done
else
    # Remote install - download from GitHub
    action "$MSG_REMOTE_INSTALL"

    # Check for curl or wget
    if command -v curl >/dev/null 2>&1; then
        DOWNLOAD="curl -fsSL"
    elif command -v wget >/dev/null 2>&1; then
        DOWNLOAD="wget -qO-"
    else
        error "$MSG_NO_CURL_WGET"
    fi

    BASE_URL="https://raw.githubusercontent.com/$REPO_USER/$REPO_NAME/$BRANCH"

    # Download scripts
    for script in "${SCRIPT_FILES[@]}"; do
        [[ -e "$INSTALL_DIR/$script" ]] || [[ -L "$INSTALL_DIR/$script" ]] && rm -f "$INSTALL_DIR/$script"

        $DOWNLOAD "$BASE_URL/$script" > "$INSTALL_DIR/$script" || error "Failed to download $script"
        chmod +x "$INSTALL_DIR/$script"
    done

    # Download completions
    if [ ${#COMPLETION_FILES[@]} -gt 0 ]; then
        for completion in "${COMPLETION_FILES[@]}"; do
            [[ -e "$COMPLETION_DIR/$completion" ]] || [[ -L "$COMPLETION_DIR/$completion" ]] && rm -f "$COMPLETION_DIR/$completion"
            $DOWNLOAD "$BASE_URL/$completion" > "$COMPLETION_DIR/$completion" || error "Failed to download $completion"
        done
    fi
fi

# Ensure install directory is in PATH
ensure_in_path "$INSTALL_DIR"

echo ""
success "$MSG_INSTALL_COMPLETE"

# Show ASCII art if configured
if [ -n "$ASCII_ART" ]; then
    echo ""
    echo "$ASCII_ART"
fi

# Show next steps
for step in "${NEXT_STEPS[@]}"; do
    echo "$step"
done
