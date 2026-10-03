<p align="center">
  <img src="https://raw.githubusercontent.com/DeprecatedLuar/ireallylovemydots/legacy-bash/other/assets/ireallylovemydots.logo.webp" width="600"/>
</p>

<p align=center> A dead-simple CLI dotfile manager in ~~bash~~ Go</p>

> **This is a complete reimplementation of the [original bash project](https://github.com/DeprecatedLuar/ireallylovemydots/tree/legacy-bash).**
> The bash version lives on in the [`legacy-bash`](https://github.com/DeprecatedLuar/ireallylovemydots/tree/legacy-bash) branch.

<p align="center">
  <a href="https://github.com/DeprecatedLuar/ireallylovemydots/stargazers">
    <img src="https://img.shields.io/github/stars/DeprecatedLuar/ireallylovemydots?style=for-the-badge&logo=github&color=1f6feb&logoColor=white&labelColor=black"/>
  </a>
  <a href="https://github.com/DeprecatedLuar/ireallylovemydots/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/DeprecatedLuar/ireallylovemydots?style=for-the-badge&color=green&labelColor=black"/>
  </a>
</p>

---

<p align="center">
  <img src="https://raw.githubusercontent.com/DeprecatedLuar/ireallylovemydots/legacy-bash/other/assets/ireallylovemydots-profile-switching-demo.gif" width="900"/>
</p>

## Cool Features

- **Dead-simple symlinks** - ~~Configs link to `~/.config/dots`~~ Repositories are cloned into `$XDG_DATA_HOME/dots`, and enabling a namespace symlinks its files into place
- ~~**Auto-Git** - Your dotfiles are a git repo, auto-commits on changes~~ **Multiple repos** - Register as many dotfile git repositories as you want, each holding named bundles of files (*namespaces*). `sync` commits and merges per top-level entry; nothing commits behind your back
- **Dots are safe** - Files get moved to trash, never deleted (XDG Trash spec)
- ~~**Batch operations** - There's also `-A` flag & argument chaining~~ **Batch operations** - `-A` enables every disabled namespace, and verbs take several namespaces at once (`dots enable nvim tmux`)
- ~~**Zero dependencies** - Literally just bash and git (and lOvE)~~ **One static binary** - Just git on your `PATH` (and lOvE)
- **Profile system** - Manage multiple variants of a config, now per entry: `main` is the namespace root and the active profile's overrides sit on top
- ~~**Hidden configs** - Just add the underscore prefix (`_nvim`) to auto-gitignore sensitive files~~ **Sync modes and read-only repos** - Choose per namespace how `sync` reconciles (`merge`, `overlay`, `overwrite-local`, `overwrite-remote`), and repositories you can't push to are detected and handled
- **Self-healing** - Manifests correct machine state, machine state corrects the symlinks; `dots doctor` shows what it found

---

## Installation

~~`curl -sSL https://raw.githubusercontent.com/DeprecatedLuar/ireallylovemydots/main/install.sh | bash`~~

```bash
curl -sSL https://raw.githubusercontent.com/DeprecatedLuar/the-satellite/main/satellite.sh | bash -s -- install DeprecatedLuar/ireallylovemydots:dots
```

Downloads the prebuilt release binary, or builds from source when none matches your platform.

With Go:

```bash
go install github.com/DeprecatedLuar/ireallylovemydots/cmd/dots@latest
```

<details>
<summary>Manual Install</summary>

```bash
git clone https://github.com/DeprecatedLuar/ireallylovemydots.git
cd ireallylovemydots
go build -o dots ./cmd/dots
```

Requires Go (see `go.mod`) and git.

</details>

---

<p align="center">
  <img src="https://raw.githubusercontent.com/DeprecatedLuar/ireallylovemydots/legacy-bash/other/assets/dots.gif" width="450"/>
</p>


## Commands

Grammar: `dots <noun> [<name>] <verb> [args]`, or verb-first (`dots enable nvim`), or namespace-first (`dots nvim enable`). `dots help` has the full list.

| Command     | Arguments                  | Description                                          |
|-------------|----------------------------|------------------------------------------------------|
| ~~setup~~ repo add | `<url>`             | ~~Connect GitHub repo (creates if needed)~~ Register and clone a repository |
| repo init   | `[path]`                   | Register a local folder, no remote                   |
| repo rm     | `<repo>`                   | Deregister a repository                              |
| repo list   |                            | List registered repositories                         |
| ~~snatch~~ add | `<namespace> <path>...` | ~~Adopt config into dots repo and create symlink~~ Track files or directories in a namespace |
| ~~link~~ enable | `<namespace> ...`      | ~~Create symlinks from dots repo to system~~ Materialize and symlink |
|             | `-A, --all`                | ~~Link all configs (skip conflicts)~~ Enable every disabled namespace |
| ~~unlink~~ disable | `<namespace>`       | ~~Remove symlink (must point to dots)~~ Remove symlinks, keep files |
| ~~eject~~ restore | `<namespace> ...`    | ~~Move config out of dots repo back to system~~ Replace symlinks with real copies, keep tracking |
| rm          | `<namespace> <path>...`    | ~~Move config files/symlink to trash~~ Untrack files or directories (`--purge` trashes instead of restoring) |
| install / uninstall | `<namespace> ...`  | Put files on disk without linking / take them off disk, keep tracked |
| sync        | `[<namespace>...]`         | ~~Git pull and check for unpushed commits~~ Pull, merge and push every repository, or only these namespaces |
| ~~pull~~    |                            | ~~Alias for sync~~                                   |
| ~~push~~    |                            | ~~Git push to remote~~ Handled by `sync`             |
| syncmode    | `<namespace> [<mode>]`     | Show or save how this machine syncs the namespace    |
| cp / mv     | `<repo/ns> <repo/ns>`      | Copy or move a namespace between repositories        |
| rn          | `<namespace> <newname>`    | Rename a namespace                                   |
| edit        | `<namespace>`              | Edit its manifest in `$EDITOR`                       |
| ~~status~~ list | (`ls`, `status`)       | ~~Show link status of all configs~~ Every namespace, with state |
| doctor      |                            | Every finding self-heal has on record                |

### Profile System (Multi-variant configs)

| Command                                    | Description                                      |
|--------------------------------------------|--------------------------------------------------|
| ~~`<config> track`~~ `<ns> profiles main add <entry>` | ~~Mark files for profiling~~ Declare an entry profiled |
| ~~`<config> init`~~ `<ns> profiles add <profile>` | ~~Create new profile (copies from existing)~~ Create a profile (`--from <source>` to seed it) |
| `<ns> profiles list`                       | Show available profiles, active marked           |
| `<ns> <profile>`                           | Switch to profile                                |
| `<ns> main`                                | Back to main                                     |
| `<ns> profiles rm <profile>`               | Delete profile                                   |
| ~~`<config> untrack`~~ `<ns> profiles main rm <entry>` | ~~Stop profiling files~~ Undeclare an entry |

## Quick Start

```bash
# Register your dotfiles repository
dots repo add https://github.com/yourusername/dotfiles.git

# Track your first config in a namespace
dots add nvim ~/.config/nvim

# Check what's enabled
dots list

# Sync with remote
dots sync

# On a new machine: install dots, add the repo, then enable everything
dots enable -A
```

<details>
<summary>Full Workflow Example</summary>

```bash
# Start managing your configs
dots add shell ~/.bashrc ~/.config/tmux

# See what's enabled
dots list

# Push to your dotfiles repo
dots sync

# On another machine:
# 1. Install dots and register your repository
# 2. Enable all namespaces
dots repo add https://github.com/yourusername/dotfiles.git
dots enable -A

# Stop linking a namespace but keep its files
dots disable shell

# Replace symlinks with real copies
dots restore shell
```

</details> 

---


### Feel free to check some of my other projects I enjoyed makig:
https://github.com/DeprecatedLuar/better-curl-saul


---

<p align="center">
  <a href="https://github.com/DeprecatedLuar/better-curl-saul/issues">
    <img src="https://img.shields.io/badge/Found%20a%20bug%3F-Report%20it!-red?style=for-the-badge&logo=github&logoColor=white&labelColor=black"/>
  </a>
</p>
