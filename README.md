# toskill

Autonomous AI agent swarm that transforms web articles into structured, reusable AI skills.

> Built with [GitHub Copilot SDK for Go](https://github.com/github/copilot-sdk) + the open [skills ecosystem](https://skills.sh/)

---

## What It Does

Give `toskill` any URL — a blog post, research paper, tutorial, or documentation page — and it autonomously:

1. **Extracts** — Discovers and uses browser automation to read the page content
2. **Curates** — Analyzes the content, detects the domain, creates a structured knowledge base
3. **Builds** — Transforms the knowledge base into a distributable AI skill with progressive disclosure

Each agent self-discovers the tools it needs at runtime using the open skills ecosystem. No hardcoded dependencies.

## Install

### From source (requires Go 1.24+)

```bash
go install github.com/byadhddev/toskill/cmd/toskill@latest
```

### From release binary

```bash
# Linux (amd64)
curl -sSL https://github.com/byadhddev/toskill/releases/latest/download/toskill-linux-amd64 -o toskill
chmod +x toskill && sudo mv toskill /usr/local/bin/

# macOS (Apple Silicon)
curl -sSL https://github.com/byadhddev/toskill/releases/latest/download/toskill-darwin-arm64 -o toskill
chmod +x toskill && sudo mv toskill /usr/local/bin/
```

### Build from source

```bash
git clone https://github.com/byadhddev/toskill.git
cd toskill
make build       # → bin/toskill
make install     # → $GOPATH/bin/toskill
```

## Prerequisites

- **[GitHub Copilot CLI](https://github.com/github/copilot-cli)** — installed and authenticated
- **[GitHub CLI (`gh`)](https://cli.github.com/)** — for GitHub storage integration (optional)
- **Node.js 18+** — for `npx skills` (skill discovery)

## Quick Start

```bash
# 1. Start Copilot CLI (in a separate terminal)
copilot --headless --port 44321

# 2. Interactive mode — guided setup wizard
toskill

# 3. Or run directly with URLs
toskill run https://example.com/blog/interesting-article

# 4. Check what was generated
toskill status
```

## Interactive Mode

Run `toskill` with no arguments for a step-by-step wizard:

```
⚡ toskill — Autonomous Skill Builder

┃ 🔗 URLs to process
┃ > https://example.com/article

┃ 📦 Storage
┃ > GitHub Repository
┃   Local (./skill-store/)

┃ ✅ Logged in as yourname
┃ 📂 Select repository
┃ > yourname/toskill-store
┃   yourname/other-repo
┃   + Create new repository

┃ 🧠 Model
┃ > claude-opus-4.6 (Recommended)

┃ ⚙️  Use different models per phase?
┃ > No

┃ 🚀 Run pipeline? Yes, let's go!
```

The wizard:
- **Auto-detects your GitHub login** via `gh` CLI — no tokens to copy-paste
- **Lists your repos** for selection, or lets you create a new one
- **Supports per-phase model selection** (e.g., cheap model for extraction, premium for building)

## Usage

```
toskill                        Interactive wizard
toskill <command> [flags] [args...]

Commands:
  run <url1> [url2] ...     Full pipeline: extract → curate → build
  extract <url1> [url2] ... Extract content from URLs only
  curate [article-paths]    Curate articles into a knowledge base
  build <kb-name>           Build a skill from a knowledge base
  build --auto              Build skills from all knowledge bases
  status                    Show current pipeline state
  config show               Show configuration
  config set <key> <value>  Set a persistent config value
  version                   Print version

Flags:
  --copilot-url <addr>     Copilot CLI server (default: localhost:44321)
  --output <dir>           Output directory (default: ./skill-store/)
  --model <name>           LLM model (default: claude-opus-4.6)
  --extract-model <name>   Model override for extraction phase
  --curate-model <name>    Model override for curation phase
  --build-model <name>     Model override for skill building phase
  --github-repo <repo>     GitHub repo (e.g. 'owner/toskill-store')
  --github-token <tok>     GitHub token (auto-detected from gh CLI)
  --verbose                Verbose output
```

## GitHub Storage

Artifacts are committed directly to a GitHub repository after each pipeline phase.

**Authentication** is automatic — toskill uses your `gh` CLI login. No manual token needed.

```bash
# If you have gh CLI authenticated:
toskill run --github-repo yourname/toskill-store https://example.com/article

# Or use the interactive wizard — it lists your repos
toskill
```

If `gh` CLI is not installed or not authenticated, toskill falls back to:
`GITHUB_TOKEN` env var → config file → local-only storage.

**What gets committed:**
- `articles/{slug}.md` — extracted content
- `knowledge-bases/{name}/KB.md` — curated knowledge base
- `skills/{name}/SKILL.md` — distributable skill
- `skills/{name}/references/*.md` — supporting reference material

## Per-Phase Models

Use different models for each pipeline stage to optimize cost vs quality:

```bash
# Fast extraction, premium skill building
toskill run \
  --extract-model claude-haiku-4.5 \
  --curate-model claude-sonnet-4.5 \
  --build-model claude-opus-4.6 \
  https://example.com/article

# Or persist in config
toskill config set extract-model claude-haiku-4.5
toskill config set build-model claude-opus-4.6
```

## Configuration

Settings are loaded in order (later overrides earlier):
1. Config file (`~/.config/toskill/config`)
2. Environment variables
3. CLI flags

```bash
# Persistent config
toskill config set copilot-url localhost:44321
toskill config set model claude-opus-4.6
toskill config set github-repo yourname/toskill-store

# Environment variables
export COPILOT_CLI_URL=localhost:44321
export TOSKILL_OUTPUT=./my-skills
export TOSKILL_MODEL=claude-opus-4.6
export GITHUB_TOKEN=ghp_xxx
```

**Valid config keys:** `copilot-url`, `output`, `model`, `extract-model`, `curate-model`, `build-model`, `github-repo`, `github-token`

## Output Structure

```
skill-store/
├── articles/                        # Raw extracted content
│   └── example-com-blog-article.md
├── knowledge-bases/                 # Curated knowledge bases
│   └── web-security/
│       └── KB.md
└── skills/                          # Distributable AI skills
    └── web-security/
        ├── SKILL.md
        └── references/
            ├── techniques.md
            └── hardening-guide.md
```

## How It Works

Three AI agents run in sequence inside a single binary. Each agent autonomously discovers and loads the skills it needs from the [open skills ecosystem](https://skills.sh/).

### 1. Content Extractor
Finds the `agent-browser` skill → loads its instructions → uses browser automation CLI to open the URL, wait for load, extract title and full body text → saves structured markdown.

### 2. Knowledge Curator
Reads extracted articles → auto-detects the domain → checks for existing knowledge bases to merge with → creates or updates a comprehensive KB with zero information loss. All code examples, data, and technical detail are preserved verbatim.

### 3. Skill Builder
Finds the `skill-creator` skill → loads its guidelines → transforms a knowledge base into a proper distributable skill following progressive disclosure: quick reference up front, detailed content in `references/`.

## Architecture

```
toskill run <urls>
    │
    ├── Content Extractor (in-process)
    │     Tools: find_skill, install_skill, load_skill, run_command, save_result
    │
    ├── Knowledge Curator (in-process)
    │     Tools: read_article, list_knowledge_bases, write_knowledge_base
    │
    └── Skill Builder (in-process)
          Tools: find_skill, load_skill, read_knowledge_base, write_skill
```

All agents run in-process — single binary, no subprocesses, no separate services.

**Stack:**
- [GitHub Copilot SDK for Go](https://github.com/github/copilot-sdk) — AI backend
- [charmbracelet/huh](https://github.com/charmbracelet/huh) — interactive terminal forms
- [skills.sh](https://skills.sh/) — open skill discovery ecosystem

## Contributing

Contributions are welcome! Here's how to get started:

1. **Fork** the repository
2. **Create a branch** for your feature: `git checkout -b feat/my-feature`
3. **Make your changes** and ensure the build passes: `make build`
4. **Commit** with a descriptive message
5. **Push** and open a Pull Request

### Development Setup

```bash
git clone https://github.com/byadhddev/toskill.git
cd toskill
make build          # Build the binary
make test           # Run tests
make release        # Cross-compile for all platforms
```

### Areas for Contribution

- **New agent types** — add agents for different content sources (PDFs, videos, repos)
- **Skill formats** — support additional output formats beyond SKILL.md
- **Model providers** — extend beyond Copilot SDK to other LLM backends
- **Caching** — skip re-extraction for already-processed URLs
- **Batch processing** — parallel URL extraction for large sets
- **Dashboard** — the web UI in `../dashboard/` needs work (see issues)

### Code Structure

```
cmd/toskill/main.go          # CLI entry point, flag parsing, pipeline orchestration
pkg/config/config.go          # Config loading (file → env → flags)
pkg/extractor/extractor.go    # Content extraction agent
pkg/curator/curator.go         # Knowledge curation agent
pkg/builder/builder.go         # Skill builder agent
pkg/tools/tools.go             # Shared agent tools (find/install/load skill, run command)
pkg/ghstore/ghstore.go         # GitHub REST API storage client
pkg/ghauth/ghauth.go           # GitHub CLI auth integration
pkg/interactive/interactive.go # Interactive wizard (charmbracelet/huh)
```

## License

MIT License

Copyright (c) 2026 byadhddev

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
