<img width="1683" height="934" alt="liftoff" src="https://github.com/user-attachments/assets/bfb78c2d-7d02-4741-9833-9d2aed931292" />
<div align="center">
  
[![GitHub Stars](https://img.shields.io/github/stars/notliad/liftoff?style=flat&color=FFD700&logo=starship&logoColor=white)](https://github.com/notliad/liftoff/stargazers)
[![GitHub Forks](https://img.shields.io/github/forks/notliad/liftoff?style=flat&color=0891b2&logo=github&logoColor=white)](https://github.com/notliad/liftoff/network)
[![GitHub License](https://img.shields.io/github/license/notliad/liftoff?style=flat&color=22c55e)](https://github.com/notliad/liftoff/blob/main/LICENSE)
![Visitors](https://api.visitorbadge.io/api/visitors?path=https%3A%2F%2Fgithub.com%2Fnotliad%2Fliftoff&label=visitors&countColor=%230c7ebe&style=flat&labelStyle=none)
![release](https://img.shields.io/github/v/release/notliad/liftoff) ![Go](https://img.shields.io/badge/Rust-000000?style=flat&logo=rust&logoColor=white)

</div>

**lo** (a.k.a. *Liftoff*) is a fast, cross-platform CLI designed to remove friction from your development workflow.

Instead of manually navigating folders, installing dependencies, and starting projects one by one, **lo** lets you launch everything from a single command — instantly.

<img width="426" height="238" alt="lifoff" src="https://github.com/user-attachments/assets/13816e5d-d63d-4c61-a3e1-e3988f6a427b" />

## Features

* Launch any project from your workspace with a single command
* Zero-config runtime detection (Node, Rust, Python, Go, Java)
* Automatic dependency installation
* **Launchpads**: group multiple projects and start them together
* **Watch Mode**: monitor your projects resources while its running
* Cross-platform: Linux, macOS, Windows

## Installation

### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/notliad/liftoff/main/install.sh | bash
```

### Arch Linux (AUR)

You can install **lo** directly from the AUR using an AUR helper like `yay` or `paru`:

```bash
yay -S liftoff
```

or

```bash
paru -S liftoff
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/notliad/liftoff/main/install.ps1 | iex
```

### Build from source (requires Go 1.22+)

```bash
bash install.sh --from-local                               # build from ./cmd/lo
bash install.sh --from-module github.com/notliad/liftoff/cmd/lo@latest
bash install.sh --uninstall
```

```powershell
.\install.ps1 -FromLocal
.\install.ps1 -FromModule github.com/notliad/liftoff/cmd/lo@latest
.\install.ps1 -Uninstall
```

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

## Usage

```bash
lo [project-name]      # run a project
lo --list, -l          # list projects
lo --pad, -p [name]    # run/create a launchpad
lo --pad --list        # list your launchpads
lo --pad --list [name] # list projects of a launchpad
lo --pad --edit [name] # edit your launchpad
lo --edit, -e          # edit your directories
lo --watch, -w [name]  # run project in watch mode
lo --print-config, -c  # display current directories
lo --help              # i need somebody :)
lo --version           # display version
```

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
