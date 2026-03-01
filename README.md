# toskill

Autonomous AI agent swarm that transforms web articles into structured, reusable AI skills.

> Built with [GitHub Copilot SDK for Go](https://github.com/github/copilot-sdk) + Claude Opus 4.6

## Install

### From source (requires Go 1.24+)

```bash
go install github.com/byadhddev/toskill/cmd/toskill@latest
```

### From release binary

```bash
# Linux (amd64)
curl -sSL https://github.com/byadhddev/ai/releases/latest/download/toskill-linux-amd64 -o toskill
chmod +x toskill && sudo mv toskill /usr/local/bin/

# macOS (Apple Silicon)
curl -sSL https://github.com/byadhddev/ai/releases/latest/download/toskill-darwin-arm64 -o toskill
chmod +x toskill && sudo mv toskill /usr/local/bin/
```

### Build from source

```bash
git clone https://github.com/byadhddev/ai.git
cd ai/toskill
make build       # → bin/toskill
make install     # → $GOPATH/bin/toskill
```

## Prerequisites

- **[GitHub Copilot CLI](https://github.com/github/copilot-cli)** installed and authenticated
- **Node.js 18+** (for `npx skills` — skill discovery)

## Quick Start

```bash
# 1. Start Copilot CLI in another terminal
copilot --headless --port 44321

# 2. Interactive mode (guided setup)
toskill

# 3. Or run directly
toskill run https://owasp.org/www-project-web-security-testing-guide/latest/4-Web_Application_Security_Testing/05-Authorization_Testing/04-Testing_for_Insecure_Direct_Object_References

# 4. Check results
toskill status
```

This autonomously:
1. **Extracts** — Discovers and uses browser automation to get article content
2. **Curates** — Detects the domain, creates a structured knowledge base
3. **Builds** — Transforms the KB into a distributable skill with progressive disclosure

## Usage

```
toskill                        Interactive mode (guided setup)
toskill <command> [flags] [args...]

Commands:
  run <url1> [url2] ...       Full pipeline: extract → curate → build
  run                         Interactive mode (no URLs → launches wizard)
  extract <url1> [url2] ...   Extract content from URLs
  curate [article-paths]      Curate articles into a knowledge base
  build <kb-name>             Build a skill from a knowledge base
  build --auto                Build skills from all knowledge bases
  status                      Show pipeline state
  config show                 Show current configuration
  config set <key> <value>    Set a config value
  version                     Print version

Flags:
  --copilot-url <addr>     Copilot CLI server (default: localhost:44321)
  --output <dir>           Output directory (default: ./skill-store/)
  --model <name>           LLM model for all phases (default: claude-opus-4.6)
  --extract-model <name>   Model override for extraction phase
  --curate-model <name>    Model override for curation phase
  --build-model <name>     Model override for skill building phase
  --github-repo <repo>     GitHub repo for storage (e.g. 'owner/toskill-store')
  --github-token <tok>     GitHub token (or $GITHUB_TOKEN)
  --verbose                Enable verbose output
```

## Interactive Mode

Run `toskill` with no arguments for a guided setup wizard:

```bash
toskill
```

The wizard walks you through:
1. **URLs** — Paste one or more URLs to process
2. **Storage** — Choose Local or GitHub repository
3. **Model** — Select the LLM model
4. **Per-phase models** — Optionally use different models for each phase
5. **Confirm** — Review settings and run

## Per-Phase Models

Use cheaper models for extraction and premium models for skill building:

```bash
# Fast extraction, premium building
toskill run --extract-model claude-haiku-4.5 --build-model claude-opus-4.6 https://example.com/article

# Or save in config
toskill config set extract-model claude-haiku-4.5
toskill config set build-model claude-opus-4.6
```

## Configuration

Settings are loaded in order (later overrides earlier):
1. Config file (`~/.config/toskill/config`)
2. Environment variables
3. CLI flags

```bash
# Save persistent config
toskill config set copilot-url localhost:44321
toskill config set model claude-opus-4.6
toskill config set github-repo myuser/toskill-store
toskill config set github-token ghp_xxx

# Or use environment variables
export COPILOT_CLI_URL=localhost:44321
export TOSKILL_OUTPUT=./my-skills
export TOSKILL_MODEL=claude-opus-4.6
export GITHUB_TOKEN=ghp_xxx
export TOSKILL_GITHUB_REPO=myuser/toskill-store
```

## GitHub Storage

Store extracted articles, knowledge bases, and skills directly in a GitHub repository:

```bash
# One-time setup
toskill config set github-repo myuser/toskill-store
toskill config set github-token ghp_xxx

# Run pipeline — artifacts are committed to GitHub automatically
toskill run https://example.com/article

# Or pass flags directly
toskill run --github-repo myuser/toskill-store --github-token ghp_xxx https://example.com/article
```

The GitHub repo is auto-created (private) if it doesn't exist. Artifacts are committed after each phase:
- `articles/{slug}.md` — extracted content
- `knowledge-bases/{name}/SKILL.md` — curated knowledge base
- `skills/{name}/SKILL.md` — distributable skill
- `skills/{name}/references/*.md` — skill reference files

## Output Structure

```
skill-store/
├── articles/                          # Extracted article content
│   └── owasp-org-idor-testing.md
├── knowledge-bases/                   # Curated knowledge bases
│   └── idor-vulnerabilities/
│       └── SKILL.md
└── skills/                            # Distributable skills
    └── idor-vulnerabilities/
        ├── SKILL.md
        └── references/
            └── techniques.md
```

## How It Works

Three AI agents work in sequence, each powered by the Copilot SDK:

1. **Content Extractor** — Self-discovers the `agent-browser` skill, installs it, reads its instructions, then uses browser automation to extract article content from any URL.

2. **Knowledge Curator** — Reads extracted articles, auto-detects the domain, checks for existing knowledge bases, and creates/updates a comprehensive KB with zero information loss.

3. **Skill Builder** — Self-discovers the `skill-creator` skill, loads its guidelines, then transforms a KB into a proper distributable skill following progressive disclosure principles.

Each agent autonomously discovers the skills it needs via the open [skills ecosystem](https://skills.sh/).

## Architecture

```
toskill run <urls>
    │
    ├─── Content Extractor (in-process)
    │      └─ find_skill → install_skill → load_skill → run_command → save_result
    │
    ├─── Knowledge Curator (in-process)
    │      └─ read_article → list_knowledge_bases → write_knowledge_base
    │
    └─── Skill Builder (in-process)
           └─ find_skill → load_skill → read_knowledge_base → write_skill
```

All three agents run in the same process — no subprocess spawning, no separate binaries.

## License

MIT
