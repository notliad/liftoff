# 🚀 liftoff

`lo` is a small Bash CLI to quickly launch Node/Bun projects in development mode.

It chooses a project, detects the package manager (`pnpm`, `bun`, `npm`), finds a runnable script (`dev` or `start`), installs dependencies if needed, and starts the project in a detached terminal.

## Features

- Interactive project selection with `fzf` (optional but recommended)
- Project fuzzy matching
- Detects script in this order: `dev`, then `start`
- Detects package manager by lockfile:
  - `pnpm-lock.yaml` -> `pnpm`
  - `bun.lock*` -> `bun`
  - `package-lock.json` -> `npm`
- Installs dependencies automatically when `node_modules` is missing
- Opens the app in a detached terminal when possible

## Requirements

- Bash (Linux)
- `jq` (required)
- One package manager installed: `pnpm`, `bun`, or `npm`
- Optional: `fzf` for interactive selection

## Installation

### Option 1: Local install script

From this repository root:

```bash
bash install.sh
```

This installs `lo` to `~/.local/bin/lo`.

### Option 2: Remote install (after publishing)

```bash
curl -fsSL https://raw.githubusercontent.com/notliad/liftoff/main/install.sh \
  | bash -s -- --from-url https://raw.githubusercontent.com/notliad/liftoff/main
```

## Usage

```bash
lo [project-name]
lo --edit
lo --print-config
lo --help
lo --version
```

### First run

On first run, `lo` asks for your projects directory and saves it to:

- `~/.config/lo/config`

Example value:

```bash
PROJECTS_DIR="/home/you/Projects"
```

## Recommended shell setup

Make sure `~/.local/bin` is in your `PATH`.

For `bash` (`~/.bashrc`):

```bash
export PATH="$HOME/.local/bin:$PATH"
```

For `zsh` (`~/.zshrc`):

```bash
export PATH="$HOME/.local/bin:$PATH"
```

## Uninstall

```bash
rm -f ~/.local/bin/lo
rm -rf ~/.config/lo
```

## Development

Run checks:

```bash
bash -n lo
bash -n install.sh
```

## License

MIT
