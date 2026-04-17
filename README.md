# 🚀 lo (Liftoff)

**lo** (a.k.a. *Liftoff*) is a fast, cross-platform CLI designed to remove friction from your development workflow.

Instead of manually navigating folders, installing dependencies, and starting projects one by one, **lo** lets you launch everything from a single command — instantly.

It can:

* interactively pick a project from your workspace
* detect the runtime and package manager automatically
* install dependencies when needed
* start the project in a detached terminal

You stay in flow. ⚡

---

## Features

* Launch any project from your workspace with a single command
* Zero-config runtime detection (Node, Rust, Python, Go, Java)
* Automatic dependency installation
* Interactive project picker
* **Launchpads**: group multiple projects and start them together
* Watch Mode: monitor your projects resources while its running
* Cross-platform: Linux, macOS, Windows

---

## What is a Launchpad?

A **launchpad** is a named group of projects that you can start at once.

Perfect for full-stack environments, microservices, or any setup where multiple services need to run together.

```bash
lo --pad backend-stack
```

---

## Supported Languages & Frameworks

### Node.js / JavaScript / TypeScript

* Detects `package.json` with `dev` or `start` scripts
* Automatically selects package manager via lockfile: `pnpm`, `bun`, `npm`, `yarn`
* Framework hints:
  Next.js, Nuxt, SvelteKit, Astro, NestJS, Remix, Vite, React, Vue, Angular, Express, Fastify, Hono

### Rust

* Detects `Cargo.toml`
* Runs with `cargo run`
* Framework hints:
  Axum, Actix, Rocket, Tauri, Bevy

### Python

* Detects `pyproject.toml`, `requirements.txt`, `setup.py`
* Parses `pyproject.toml` for smarter detection
* Execution strategy: `uv`, `poetry`, or `python` (`py` on Windows)
* Framework hints:
  FastAPI, Flask, Django, Streamlit, Gradio

### Java

* Detects `pom.xml`, `build.gradle`, `build.gradle.kts`
* Maven: `spring-boot:run` or `exec:java`
* Gradle: `bootRun`, `run`, or `build`
* Framework hints:
  Spring, Quarkus, Micronaut

### Go

* Detects `go.mod`
* Runs with `go run .` or `go run ./cmd/<name>`
* Framework hints:
  Gin, Fiber, Echo, Chi, Temporal

## Requirements

* Go (`1.25+`) for build/install from source

## Installation

### Option 1: Local install script

From this repository root:

```bash
bash install.sh
```

This builds `./cmd/lo` and installs `lo` to `~/.local/bin/lo`.
If present, it also installs the man page to `~/.local/share/man/man1/lo.1`.

### Option 2: Remote install

```bash
curl -fsSL https://raw.githubusercontent.com/notliad/liftoff/main/install.sh \
  | bash -s -- \
      --from-module github.com/notliad/liftoff/cmd/lo@latest \
      --man-from-url https://raw.githubusercontent.com/notliad/liftoff/main
```

## Usage

```bash
lo [project-name]      # run a project
lo --list, -l          # list projects
lo --pad, -p [name]    # run a launchpad
lo --pad --list [name] # list your launchpads
lo --pad --edit [name] # edit your launchpads
lo --edit, -e          # edit your directories
lo --watch, -w         # run project in another terminal and monitor stats here
lo --print-config      # display current directories
lo --help              # i need somebody :)
lo --version           # display version
```

### Watch mode

* `lo --watch` (`-w`): launches the selected project in another terminal window
* Shows Bubble Tea live stats in the terminal where `lo` was executed
* Stats refresh every 2s for process tree (root + child processes): CPU and memory (RSS)
* Current limitation: `--watch` is only available for single project launch (not `--pad`)

### Launchpad

* `lo --pad my-work`: runs launchpad `my-work`; if missing, opens a checklist to create it
* When a launchpad is created, projects are not started automatically
* `lo --pad --edit my-work`: edits projects in launchpad `my-work`
* `lo --pad --edit`: first choose an existing launchpad, then edit it
* `lo --pad --list` (`-p -l`): lists launchpads
* `lo --pad --list my-work`: shows projects from launchpad `my-work`
* `lo --list` (`-l`): lists projects across all configured directories

### First run

On first run, `lo` asks for your projects directories (comma-separated) and saves them to:

* `~/.config/lo/config.json`

Example value:

```json
{
  "projectsDir": "/home/you/Projects",
  "projectsDirs": [
    "/home/you/Projects",
    "/home/you/Work"
  ],
  "launchpads": {
    "my-work": [
      "api",
      "web"
    ]
  }
}
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
bash -n install.sh
go test ./...
go build ./cmd/lo
man ./man/man1/lo.1
```

## License

MIT
