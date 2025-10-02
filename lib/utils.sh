# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m'

error() { echo -e "${RED}→ $1${NC}" >&2; exit 1; }
success() { echo -e "${GREEN}→ $1${NC}"; }
warn() { echo -e "${YELLOW}! $1${NC}"; }
info() { echo -e "${BLUE}→ $1${NC}"; }

check_dotfiles_repo() {
    # Create dots repo if doesn't exist
    if [[ ! -d "$DOTFILES" ]]; then
        mkdir -p "$DOTFILES"
        cd "$DOTFILES"
        git init >/dev/null 2>&1
        mkdir -p config home
    elif [[ ! -d "$DOTFILES/.git" ]]; then
        # Directory exists but not a git repo
        cd "$DOTFILES"
        git init >/dev/null 2>&1
        mkdir -p config home
    fi
}

ensure_dotfiles_structure() {
    mkdir -p "$DOTFILES/config"
    mkdir -p "$DOTFILES/home"
}

# (.nvim -> nvim)
strip_dot() {
    local raw_folder_name="$1"
    echo "${raw_folder_name#.}"
}

# Find where a config currently lives in the filesystem
# Returns: "config nvim" or "home .gitconfig" or "none"
find_in_filesystem() {
    local raw_config_name="$1"
    local config_name=$(strip_dot "$raw_config_name")

    # First: Check literal input (highest priority)
    #1 Check literal in ~/.config
    if [[ -e "$HOME/.config/$raw_config_name" ]] || [[ -L "$HOME/.config/$raw_config_name" ]]; then
        echo "config $raw_config_name"
        return
    fi

    #2 Check literal in ~/
    if [[ -e "$HOME/$raw_config_name" ]] || [[ -L "$HOME/$raw_config_name" ]]; then
        echo "home $raw_config_name"
        return
    fi

    # Second: Check variations if literal not found
    #3 Check stripped version in ~/.config (if different from raw)
    if [[ "$raw_config_name" != "$config_name" ]] && ([[ -e "$HOME/.config/$config_name" ]] || [[ -L "$HOME/.config/$config_name" ]]); then
        echo "config $config_name"
        return
    fi

    #4 Check stripped version in ~/ (if different from raw)
    if [[ "$raw_config_name" != "$config_name" ]] && ([[ -e "$HOME/$config_name" ]] || [[ -L "$HOME/$config_name" ]]); then
        echo "home $config_name"
        return
    fi

    #5 Check with dot prepended in ~/.config (if user didn't type dot)
    if [[ "$raw_config_name" == "$config_name" ]] && ([[ -e "$HOME/.config/.$config_name" ]] || [[ -L "$HOME/.config/.$config_name" ]]); then
        echo "config .$config_name"
        return
    fi

    #6 Check with dot prepended in ~/ (if user didn't type dot)
    if [[ "$raw_config_name" == "$config_name" ]] && ([[ -e "$HOME/.$config_name" ]] || [[ -L "$HOME/.$config_name" ]]); then
        echo "home .$config_name"
        return
    fi

    echo "none"
}

# Check if a config exists in dots repo
# Returns: "config nvim" or "home .gitconfig" or "none"
find_in_dotfiles() {
    local name="$1"
    local stripped=$(strip_dot "$name")

    # Check both with and without dots
    if [[ -e "$DOTFILES/config/$stripped" ]]; then
        echo "config $stripped"
        return
    fi

    if [[ -e "$DOTFILES/config/$name" ]]; then
        echo "config $name"
        return
    fi

    if [[ -e "$DOTFILES/home/$name" ]]; then
        echo "home $name"
        return
    fi

    if [[ -e "$DOTFILES/home/$stripped" ]]; then
        echo "home $stripped"
        return
    fi

    echo "none"
}
