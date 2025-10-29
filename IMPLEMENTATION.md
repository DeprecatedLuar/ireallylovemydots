# Profile System Implementation Plan

This document maps out the implementation strategy for the profile system, ensuring we reuse existing patterns and avoid code duplication.

---

## Current System Analysis

### Existing Patterns to Reuse

#### 1. Command Structure
```bash
cmd_<name>() {
    check_dotfiles_repo
    [handle flags like -A/--all]
    [validate args]
    # Batch mode: loop calling <name>_single()
    for item in "$@"; do
        <name>_single "$item"
    done
}

<name>_single() {
    # Actual work here
    # Git operations with cd "$DOTFILES"
    # Auto-commit with descriptive message
}
```

**Profile Commands Must Follow**: `cmd_profile_dispatch()` → individual profile functions

#### 2. Path Resolution (REUSE DIRECTLY)
```bash
find_in_filesystem()  # Already handles config/ vs home/
find_in_dotfiles()    # Already handles dot-stripping
strip_dot()           # Already implemented
```

**DO NOT DUPLICATE** - Profile commands will use these directly for finding config directories

#### 3. Git Integration Pattern (REUSE PATTERN)
```bash
cd "$DOTFILES"
git add <path> >/dev/null 2>&1
git commit -m "<action>: <description>" >/dev/null 2>&1
```

**Profile Commits**:
- `"profile: track <files> in <config>"`
- `"profile: init <name> for <config>"`
- `"profile: switch <config> to <name>"`
- `"profile: remove <name> from <config>"`
- `"profile: untrack <files> from <config>"`
- `"profile: flatten <config>"`

#### 4. Error Handling (REUSE DIRECTLY)
```bash
error()    # Red, exits
success()  # Green
warn()     # Yellow
info()     # Blue
```

**DO NOT CREATE NEW ERROR FUNCTIONS** - Use existing ones

#### 5. Safety Patterns (REUSE LOGIC)
```bash
# From link_single(): Handle symlinks vs real files
if [[ -L "$path" ]]; then
    rm "$path"  # Safe to replace symlinks
else
    error "Real file exists"  # Never silently overwrite
fi
```

**Profile System Must**: Check both outer symlink AND inner profile symlinks

---

## Implementation Phases

### Phase 1: Profile Utilities (Foundation)

**Location**: After `# ====[ UTILS ]=====` section, before `# ====[ MAIN ]=====`

**New Functions**:

```bash
# ====[ PROFILE UTILITIES ]=============

# Check if a config directory has profiles enabled
# Args: $1 = config name (e.g., "kitty")
# Returns: 0 if profiled, 1 if not
is_profiled_config() {
    local config_name="$1"

    # Use find_in_dotfiles() to locate config
    local result=$(find_in_dotfiles "$config_name")
    [[ "$result" == "none" ]] && return 1

    local type found_name
    read type found_name <<< "$result"

    # Profiles only work in config/, not home/
    [[ "$type" != "config" ]] && return 1

    local config_path="$DOTFILES/config/$found_name"
    [[ -f "$config_path/.profile-manifest" ]]
}

# Get config path in dotfiles (helper for profile operations)
# Args: $1 = config name
# Outputs: full path to config dir in dots
get_config_path() {
    local config_name="$1"
    local result=$(find_in_dotfiles "$config_name")
    [[ "$result" == "none" ]] && error "'$config_name' not found in dots repo"

    local type found_name
    read type found_name <<< "$result"
    [[ "$type" != "config" ]] && error "'$config_name' is in home/, profiles only work for config/ items"

    echo "$DOTFILES/config/$found_name"
}

# Read manifest file
# Args: $1 = config path
# Outputs: space-separated list of tracked files
read_manifest() {
    local config_path="$1"
    local manifest="$config_path/.profile-manifest"
    [[ ! -f "$manifest" ]] && return
    cat "$manifest" | tr '\n' ' '
}

# Write manifest file
# Args: $1 = config path, $2+ = files to write
write_manifest() {
    local config_path="$1"
    shift
    local manifest="$config_path/.profile-manifest"
    printf "%s\n" "$@" > "$manifest"
}

# Add to manifest (append files)
# Args: $1 = config path, $2+ = files to add
add_to_manifest() {
    local config_path="$1"
    shift
    local manifest="$config_path/.profile-manifest"
    for file in "$@"; do
        # Avoid duplicates
        if ! grep -Fxq "$file" "$manifest" 2>/dev/null; then
            echo "$file" >> "$manifest"
        fi
    done
}

# Remove from manifest
# Args: $1 = config path, $2+ = files to remove
remove_from_manifest() {
    local config_path="$1"
    shift
    local manifest="$config_path/.profile-manifest"
    [[ ! -f "$manifest" ]] && return

    local temp=$(mktemp)
    for file in "$@"; do
        grep -Fxv "$file" "$manifest" > "$temp" 2>/dev/null || true
        mv "$temp" "$manifest"
    done
}

# No active profile tracking needed - symlinks are the source of truth

# Validate profile name (check for reserved words)
# Args: $1 = profile name
# Returns: 0 if valid, 1 if invalid
validate_profile_name() {
    local name="$1"
    local reserved=("track" "init" "list" "rm" "untrack" "flatten")

    [[ -z "$name" ]] && return 1
    [[ "$name" == *"/"* ]] && return 1
    [[ "$name" == *".."* ]] && return 1

    for word in "${reserved[@]}"; do
        [[ "$name" == "$word" ]] && return 1
    done

    return 0
}

# Setup profile symlinks (create internal links from root to profile/)
# Args: $1 = config path, $2 = profile name
# Note: Silently skips missing files (per PROFILES.md spec)
setup_profile_symlinks() {
    local config_path="$1"
    local profile_name="$2"
    local manifest="$config_path/.profile-manifest"

    [[ ! -f "$manifest" ]] && error "No .profile-manifest found in $(basename "$config_path")"

    local files=$(read_manifest "$config_path")
    local linked_count=0

    for file in $files; do
        local profile_file="$config_path/profiles/$profile_name/$file"
        local root_link="$config_path/$file"

        # Skip if file doesn't exist in profile (partial profiles allowed)
        [[ ! -e "$profile_file" ]] && continue

        # Check if root location has real file (conflict)
        if [[ -e "$root_link" ]] && [[ ! -L "$root_link" ]]; then
            error "Cannot link $file: real file exists at $root_link\nOptions:\n  1. Move to profiles: dots $(basename "$config_path") init <name>\n  2. Remove manually: rm $root_link"
        fi

        # Remove existing symlink if present
        [[ -L "$root_link" ]] && rm "$root_link"

        # Create symlink
        ln -s "profiles/$profile_name/$file" "$root_link"
        ((linked_count++))
    done

    # Error if NO files were linked (completely broken/empty profile)
    [[ $linked_count -eq 0 ]] && error "Profile '$profile_name' has no matching files from manifest"

    return 0
}
```

---

### Phase 2: Profile Command Dispatcher

**Location**: After profile utilities, before `# ====[ MAIN ]=====`

```bash
# ====[ PROFILE COMMANDS ]=============

# Main dispatcher for profile subcommands
# Usage: dots <config> <subcommand|profile-name> [args]
cmd_profile_dispatch() {
    local config_name="$1"
    local subcommand="$2"
    shift 2

    check_dotfiles_repo

    # Get config path early (validates config exists)
    local config_path=$(get_config_path "$config_name")

    case "$subcommand" in
        track)
            profile_track "$config_path" "$@"
            ;;
        init)
            profile_init "$config_path" "$@"
            ;;
        list)
            profile_list "$config_path"
            ;;
        rm)
            profile_rm "$config_path" "$@"
            ;;
        untrack)
            profile_untrack "$config_path" "$@"
            ;;
        flatten)
            profile_flatten "$config_path"
            ;;
        "")
            # No subcommand - show help
            error "Usage: dots $config_name <subcommand>\n\nSubcommands:\n  track <files>     Mark files for profiling\n  init <name>       Create profile\n  list              List profiles\n  rm <name>         Delete profile\n  untrack <files>   Stop tracking files\n  flatten           Remove profile system"
            ;;
        *)
            # Assume it's a profile name (switch command)
            profile_switch "$config_path" "$subcommand"
            ;;
    esac
}
```

**Integration Point**: Main dispatch must detect profiled configs:

```bash
# In main case statement, before error:
*)
    # Check if it's a profiled config
    if is_profiled_config "$1"; then
        cmd_profile_dispatch "$@"
    else
        error "Unknown command: $1\nTry 'dots help' for usage"
    fi
    ;;
```

---

### Phase 3: Setup Phase Commands

**Pattern**: Follow `snatch_single()` and `link_single()` patterns

```bash
# Track files for profiling
# Args: $1 = config path, $2+ = files to track
profile_track() {
    local config_path="$1"
    shift
    [[ -z "$1" ]] && error "Usage: dots $(basename "$config_path") track <file> [files...]"

    local config_name=$(basename "$config_path")
    local manifest="$config_path/.profile-manifest"

    # Validate files exist in config dir
    for file in "$@"; do
        [[ ! -e "$config_path/$file" ]] && error "File '$file' not found in $config_name/"
    done

    # Create or update manifest
    add_to_manifest "$config_path" "$@"

    # Git commit
    cd "$DOTFILES"
    git add "$manifest" >/dev/null 2>&1
    git commit -m "profile: track $(echo $@ | tr ' ' ', ') in $config_name" >/dev/null 2>&1

    success "Tracking $(echo $@ | tr ' ' ', ') in $config_name"
}

# Initialize profile (create first one or copy from active)
# Args: $1 = config path, $2 = profile name
profile_init() {
    local config_path="$1"
    local profile_name="$2"

    [[ -z "$profile_name" ]] && error "Usage: dots $(basename "$config_path") init <profile-name>"

    local config_name=$(basename "$config_path")
    local manifest="$config_path/.profile-manifest"

    # Validate manifest exists
    [[ ! -f "$manifest" ]] && error "No tracked files in $config_name\nRun: dots $config_name track <files>"

    # Validate profile name
    validate_profile_name "$profile_name" || error "Invalid profile name '$profile_name'\nReserved words: track, init, list, rm, untrack, flatten\nAvoid: /, .."

    local profiles_dir="$config_path/profiles"
    local profile_dir="$profiles_dir/$profile_name"

    # Check if profile already exists
    [[ -d "$profile_dir" ]] && error "Profile '$profile_name' already exists"

    mkdir -p "$profile_dir"

    # Check if this is first profile or not
    local is_first_profile=false
    if [[ ! -d "$profiles_dir" ]] || [[ -z "$(ls -A "$profiles_dir" 2>/dev/null)" ]]; then
        is_first_profile=true
    fi

    local files=$(read_manifest "$config_path")

    if [[ "$is_first_profile" == "true" ]]; then
        # First profile - move files from root
        for file in $files; do
            local source="$config_path/$file"
            [[ -e "$source" ]] && mv "$source" "$profile_dir/"
        done

        # Setup .gitignore
        local gitignore="$config_path/.gitignore"
        {
            for file in $files; do
                echo "/$file"
            done
            echo "!profiles/"
        } > "$gitignore"

        # Create symlinks
        setup_profile_symlinks "$config_path" "$profile_name"
    else
        # Copy from any existing profile (just pick first one)
        local existing=$(ls "$profiles_dir" | head -1)
        for file in $files; do
            local source="$profiles_dir/$existing/$file"
            [[ -e "$source" ]] && cp -r "$source" "$profile_dir/"
        done
    fi

    # Git commit
    cd "$DOTFILES"
    git add "$profile_dir" >/dev/null 2>&1
    [[ -f "$config_path/.gitignore" ]] && git add "$config_path/.gitignore" >/dev/null 2>&1
    git commit -m "profile: init $profile_name for $config_name" >/dev/null 2>&1

    success "Created profile '$profile_name' for $config_name"
}
```

---

### Phase 4: Daily Usage Commands

```bash
# Switch to a profile
# Args: $1 = config path, $2 = profile name
profile_switch() {
    local config_path="$1"
    local profile_name="$2"
    local config_name=$(basename "$config_path")
    local profile_dir="$config_path/profiles/$profile_name"

    [[ ! -d "$profile_dir" ]] && {
        error "Profile '$profile_name' not found in $config_name/profiles/\n\nAvailable profiles:\n$(ls "$config_path/profiles" 2>/dev/null | sed 's/^/  /')\n\nCreate it with: dots $config_name init $profile_name"
    }

    # Setup symlinks and set active
    setup_profile_symlinks "$config_path" "$profile_name"
    set_active_profile "$config_path" "$profile_name"

    success "Switched $config_name to '$profile_name'"
}

# List available profiles
# Args: $1 = config path
profile_list() {
    local config_path="$1"
    local profiles_dir="$config_path/profiles"

    [[ ! -d "$profiles_dir" ]] && error "No profiles found"

    ls "$profiles_dir"
}
```

---

### Phase 5: Management Commands

```bash
# Delete a profile
# Args: $1 = config path, $2 = profile name
profile_rm() {
    local config_path="$1"
    local profile_name="$2"
    local config_name=$(basename "$config_path")

    [[ -z "$profile_name" ]] && error "Usage: dots $config_name rm <profile-name>"

    local profile_dir="$config_path/profiles/$profile_name"
    [[ ! -d "$profile_dir" ]] && error "Profile '$profile_name' not found"

    # Move to trash (reuse trash pattern)
    mkdir -p "$TRASH"
    local trash_path="$TRASH/${config_name}_${profile_name}"
    [[ -e "$trash_path" ]] && trash_path="$TRASH/${config_name}_${profile_name}.$(date +%s)"

    info "Moving $profile_dir → $trash_path"
    mv "$profile_dir" "$trash_path"

    # Git commit
    cd "$DOTFILES"
    git add -A >/dev/null 2>&1
    git commit -m "profile: remove $profile_name from $config_name" >/dev/null 2>&1

    success "Deleted profile '$profile_name' from $config_name"
}

# Untrack files from manifest
# Args: $1 = config path, $2+ = files to untrack
profile_untrack() {
    local config_path="$1"
    shift
    [[ -z "$1" ]] && error "Usage: dots $(basename "$config_path") untrack <file> [files...]"

    local config_name=$(basename "$config_path")

    # For each file: convert symlink to real file, remove from manifest
    for file in "$@"; do
        local link="$config_path/$file"

        # If it's a symlink, resolve it to real file
        if [[ -L "$link" ]]; then
            local source=$(readlink -f "$link")
            rm "$link"
            [[ -e "$source" ]] && cp -r "$source" "$link"
        fi

        remove_from_manifest "$config_path" "$file"

        # Remove from .gitignore
        local gitignore="$config_path/.gitignore"
        if [[ -f "$gitignore" ]]; then
            grep -v "^/$file$" "$gitignore" > "$gitignore.tmp"
            mv "$gitignore.tmp" "$gitignore"
        fi
    done

    # Git commit
    cd "$DOTFILES"
    git add "$config_path/.profiled" "$config_path/.gitignore" "$config_path" >/dev/null 2>&1
    git commit -m "profile: untrack $(echo $@ | tr ' ' ', ') from $config_name" >/dev/null 2>&1

    success "Untracked $(echo $@ | tr ' ' ', ') from $config_name"
}

# Remove profile system entirely (flatten to real files)
# Args: $1 = config path
profile_flatten() {
    local config_path="$1"
    local config_name=$(basename "$config_path")

    # Convert all symlinks to real files
    local files=$(read_manifest "$config_path")
    for file in $files; do
        local link="$config_path/$file"

        if [[ -L "$link" ]]; then
            local source=$(readlink -f "$link")
            rm "$link"
            [[ -e "$source" ]] && cp -r "$source" "$link"
        fi
    done

    # Move profiles to trash
    mkdir -p "$TRASH"
    local trash_path="$TRASH/${config_name}_profiles.$(date +%s)"
    mv "$config_path/profiles" "$trash_path"

    # Remove profile files
    rm -f "$config_path/.profiled" "$config_path/.gitignore"

    # Git commit
    cd "$DOTFILES"
    git add -A >/dev/null 2>&1
    git commit -m "profile: flatten $config_name" >/dev/null 2>&1

    success "Flattened $config_name (profiles moved to trash)"
}
```

---

### Phase 6: Enhanced `cmd_link` for Profile Awareness

**Modification**: In `link_single()`, after creating outer symlink, check for profiles:

```bash
link_single() {
    local name="$1"

    # [Existing code for finding and linking config...]

    # NEW: After successful outer symlink creation
    ln -s "$dots_path" "$target_path"

    # Check if this config has profiles enabled
    if [[ -f "$dots_path/.profile-manifest" ]]; then
        # Setup internal profile symlinks
        local active=$(get_active_profile "$dots_path")
        if [[ -n "$active" ]]; then
            setup_profile_symlinks "$dots_path" "$active" 2>/dev/null || {
                warn "Profiled config '$found_name' linked, but profile setup failed"
                warn "Run: dots $found_name list"
            }
        fi
    fi

    success "Linked $found_name"
}
```

---

## Implementation Checkpoints

Each checkpoint is a testable, committable unit. Phases are grouped so each checkpoint can be verified independently.

### CHECKPOINT 1: Minimal Viable Profile System ⭐ ✅ COMPLETED
**Goal**: Can create and initialize profiles

**Includes**:
- Phase 1: Profile utilities (foundation) - ~180 lines ✅
- Phase 2: Dispatcher (routing) - ~45 lines ✅
- Phase 3: Setup commands (`track`, `init`) - ~90 lines ✅
- Phase 4: Profile switching (`switch` command) - ~10 lines ✅
- Main dispatch hook (check `is_profiled_config` in `*` case) ✅

**Implementation Notes**:
- Used `.profiled` instead of `.profile-manifest` (simpler naming)
- No `.active` file - symlinks are the source of truth
- Extracted `activate_profile_symlinks()` function for reusability
- Fixed `((linked_count++))` issue with `set -e`
- Init dereferences symlinks (copies actual content for templates)
- Robust: Loops over actual files in profile directory
- `.gitignore` pattern: Uses `/filename` (root-anchored) + `!profiles/` (explicit exception)
- No active profile tracking - just list directories, user switches manually
- Deleting active profile is fine - creates broken symlinks, user just switches

**Test Commands**:
```bash
# Prerequisite: Have a config to work with
dots snatch kitty

# Test tracking
dots kitty track kitty.conf theme.conf
# Verify: .profiled created with listed files ✅

# Test init (first profile)
dots kitty init gruvbox
# Verify: profiles/gruvbox/ created, files moved, symlinks created, .gitignore added ✅

# Test init (second profile)
dots kitty init minimal
# Verify: profiles/minimal/ created by copying from gruvbox ✅

# Test switching
dots kitty minimal  # ✅
dots kitty gruvbox  # ✅
```

**Commit Message**: `feat: add profile system foundation (track + init + switch)`

---

### CHECKPOINT 2: Profile Listing ⭐ ✅ COMPLETED
**Goal**: Can see available profiles

**Includes**:
- Phase 4: Profile listing (`list` command) - ~10 lines ✅

**Test Commands**:
```bash
# Test list
dots kitty list
# Verify: Shows both profiles (gruvbox, minimal) ✅
```

**Implementation Notes**:
- Simple `ls "$profiles_dir"` - no state tracking
- No active profile marking - user knows what they switched to

**Commit Message**: `feat: add profile listing`

---

### CHECKPOINT 3: Auto-linking (Fresh Clone Support) ⭐ ⏳ TODO
**Goal**: `dots link` auto-sets up profiled configs

**Includes**:
- Phase 6: Enhanced `cmd_link` for profile awareness - ~15 lines

**Test Commands**:
```bash
# Test fresh clone scenario
dots unlink kitty
# Verify: Outer symlink removed

dots link kitty
# Verify: Outer symlink recreated, inner profile symlinks auto-setup

# Test link -A with profiled configs
dots unlink -A
dots link -A
# Verify: All configs linked, profiled ones have internal symlinks setup
```

**Commit Message**: `feat: auto-setup profiles on link`

---

### CHECKPOINT 4: Management & Cleanup ⭐ ⏳ TODO
**Goal**: Can delete profiles, untrack files, remove profile system

**Includes**:
- Phase 5: Management (`rm`, `untrack`, `flatten`) - ~150 lines

**Test Commands**:
```bash
# Test profile deletion
dots kitty init nord
dots kitty rm nord
# Verify: nord moved to trash, no longer listed

# Test untrack
dots kitty untrack theme.conf
# Verify: theme.conf removed from manifest, symlink replaced with real file

# Test flatten
dots kitty flatten
# Verify: profiles/ removed, all symlinks replaced with real files, manifest deleted
```

**Commit Message**: `feat: add profile management (rm, untrack, flatten)`

---

### CHECKPOINT 5: Polish & Documentation ⭐ ⏳ TODO
**Goal**: Complete user-facing experience

**Includes**:
- Update `cmd_help()` with profile examples
- Update `lib/completions.sh` for profile subcommands
- Final integration testing with full workflow

**Test Commands**:
```bash
# Full lifecycle test
dots snatch ranger
dots ranger track rc.conf rifle.conf scope.sh
dots ranger init work
dots ranger init personal
dots ranger list
dots ranger personal
dots ranger work
dots ranger rm personal

dots eject ranger

# Test help
dots help | grep profile  # Should show profile-related examples

# Test completions
dots kitty <TAB>  # Should suggest: track, init, list, rm, untrack, flatten, [profile-names]
```

**Commit Message**: `docs: add profile system to help and completions`

---

## Checkpoint Dependencies

```
✅ CHECKPOINT 1 (foundation + switching)
    ↓
✅ CHECKPOINT 2 (listing)
    ↓
⏳ CHECKPOINT 3 (auto-link)
    ↓
⏳ CHECKPOINT 4 (management)
    ↓
⏳ CHECKPOINT 5 (polish)
```

**Status Legend**:
- ✅ COMPLETED
- 🔄 IN PROGRESS
- ⏳ TODO

**Each checkpoint must pass tests before moving to next.**

**Next Step**: CHECKPOINT 3 - Auto-linking (Fresh Clone Support)
