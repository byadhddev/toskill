package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/byadhddev/toskill/pkg/builder"
	"github.com/byadhddev/toskill/pkg/config"
	"github.com/byadhddev/toskill/pkg/curator"
	"github.com/byadhddev/toskill/pkg/extractor"
	"github.com/byadhddev/toskill/pkg/ghauth"
	"github.com/byadhddev/toskill/pkg/ghstore"
	"github.com/byadhddev/toskill/pkg/interactive"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		// No args → interactive mode
		runInteractive()
		return
	}

	command := os.Args[1]
	if command == "version" || command == "--version" || command == "-v" {
		fmt.Printf("toskill %s\n", version)
		os.Exit(0)
	}
	if command == "help" || command == "--help" || command == "-h" {
		printUsage()
		os.Exit(0)
	}

	// Parse flags after the subcommand
	fs := flag.NewFlagSet(command, flag.ExitOnError)
	copilotURL := fs.String("copilot-url", "", "Copilot CLI server address (default: $COPILOT_CLI_URL or localhost:44321)")
	output := fs.String("output", "", "Output directory (default: ./skill-store/)")
	model := fs.String("model", "", "LLM model (default: claude-opus-4.6)")
	extractModel := fs.String("extract-model", "", "Model for extraction phase")
	curateModel := fs.String("curate-model", "", "Model for curation phase")
	buildModel := fs.String("build-model", "", "Model for skill building phase")
	verbose := fs.Bool("verbose", false, "Enable verbose output")
	githubRepo := fs.String("github-repo", "", "GitHub repo for artifact storage (e.g. 'owner/toskill-store')")
	githubToken := fs.String("github-token", "", "GitHub token for repo access (or $GITHUB_TOKEN)")
	fs.Parse(os.Args[2:])

	cfg := config.DefaultConfig()
	if *copilotURL != "" {
		cfg.CopilotURL = *copilotURL
	}
	if *output != "" {
		cfg.OutputDir = *output
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *extractModel != "" {
		cfg.ExtractModel = *extractModel
	}
	if *curateModel != "" {
		cfg.CurateModel = *curateModel
	}
	if *buildModel != "" {
		cfg.BuildModel = *buildModel
	}
	cfg.Verbose = *verbose

	// GitHub storage setup
	ghToken := *githubToken
	if ghToken == "" {
		ghToken = os.Getenv("GITHUB_TOKEN")
	}
	ghRepo := *githubRepo
	if ghRepo == "" {
		ghRepo = os.Getenv("TOSKILL_GITHUB_REPO")
	}
	// Fallback to config file
	if fileCfg, err := config.LoadConfigFile(); err == nil {
		if ghToken == "" && fileCfg["github-token"] != "" {
			ghToken = fileCfg["github-token"]
		}
		if ghRepo == "" && fileCfg["github-repo"] != "" {
			ghRepo = fileCfg["github-repo"]
		}
	}
	// Fallback to gh CLI auth
	if ghToken == "" && ghRepo != "" {
		ghToken = ghauth.GetToken()
	}
	var store *ghstore.GitHubStore
	if ghToken != "" && ghRepo != "" {
		store = ghstore.New(ghToken, ghRepo)
		fmt.Fprintf(os.Stderr, "📦 GitHub storage: %s\n", ghRepo)
	}

	// Ensure output directories exist
	if err := cfg.EnsureDirs(); err != nil {
		fatal("Failed to create output directories: %v", err)
	}

	args := fs.Args()
	var err error

	switch command {
	case "run":
		if len(args) == 0 {
			// No URLs → interactive mode
			runInteractive()
			return
		}
		client := connectCopilot(cfg.CopilotURL)
		err = runPipeline(client, cfg, store, args)

	case "extract":
		if len(args) == 0 {
			fatal("No URLs provided.\nUsage: toskill extract [flags] <url1> [url2] ...")
		}
		client := connectCopilot(cfg.CopilotURL)
		err = runExtract(client, cfg, args)

	case "curate":
		client := connectCopilot(cfg.CopilotURL)
		err = runCurate(client, cfg, args)

	case "build":
		if len(args) == 0 {
			fatal("No KB name provided.\nUsage: toskill build [flags] <kb-name>")
		}
		client := connectCopilot(cfg.CopilotURL)
		err = runBuild(client, cfg, args)

	case "status":
		err = runStatus(cfg)

	case "config":
		err = runConfig(args)

	default:
		fmt.Fprintf(os.Stderr, "❌ Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fatal("Error: %v", err)
	}

	fmt.Fprintf(os.Stderr, "\n🎉 Done.\n")
}

// --- Commands ---

func runPipeline(client *copilot.Client, cfg config.Config, store *ghstore.GitHubStore, urls []string) error {
	fmt.Fprintf(os.Stderr, "🚀 Starting full pipeline for %d URL(s)\n", len(urls))
	if store != nil {
		fmt.Fprintf(os.Stderr, "   Storage: GitHub (%s/%s)\n", store.Owner(), store.Repo())
		fmt.Fprintf(os.Stderr, "   Working dir: %s\n", cfg.OutputDir)
	} else {
		fmt.Fprintf(os.Stderr, "   Output: %s\n", cfg.OutputDir)
	}
	fmt.Fprintf(os.Stderr, "   Model: %s\n", cfg.Model)
	if cfg.ExtractModel != "" || cfg.CurateModel != "" || cfg.BuildModel != "" {
		fmt.Fprintf(os.Stderr, "   Extract: %s | Curate: %s | Build: %s\n",
			cfg.ModelFor("extract"), cfg.ModelFor("curate"), cfg.ModelFor("build"))
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Ensure GitHub repo exists
	if store != nil {
		htmlURL, err := store.EnsureRepo()
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  GitHub repo init failed: %v (continuing with local)\n", err)
			store = nil
		} else {
			fmt.Fprintf(os.Stderr, "📦 GitHub repo ready: %s\n\n", htmlURL)
		}
	}

	// Phase 1: Extract
	ctx := context.Background()
	results, err := extractor.Run(ctx, client, cfg, urls)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Save articles
	var articlePaths []string
	for url, content := range results {
		slug := urlToSlug(url)
		articlePath := filepath.Join(cfg.ArticlesDir(), slug+".md")
		if err := os.WriteFile(articlePath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to save article: %v\n", err)
			continue
		}
		articlePaths = append(articlePaths, articlePath)
		fmt.Fprintf(os.Stderr, "📄 Saved: %s\n", articlePath)

		// Commit to GitHub
		if store != nil {
			ghURL, err := store.WriteFile("articles/"+slug+".md", content, "Extract: "+slug)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  GitHub commit failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "   → GitHub: %s\n", ghURL)
			}
		}
	}

	if len(articlePaths) == 0 {
		return fmt.Errorf("no articles extracted successfully")
	}

	fmt.Fprintf(os.Stderr, "\n✅ Extracted %d/%d articles\n\n", len(articlePaths), len(urls))

	// Phase 2: Curate
	kbPath, err := curator.Run(ctx, client, cfg, articlePaths)
	if err != nil {
		return fmt.Errorf("curation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n✅ Knowledge base: %s\n", kbPath)

	// Commit KB to GitHub
	if store != nil {
		kbName := filepath.Base(filepath.Dir(kbPath))
		kbContent, err := os.ReadFile(kbPath)
		if err == nil {
			ghURL, err := store.WriteFile("knowledge-bases/"+kbName+"/SKILL.md", string(kbContent), "Curate: "+kbName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  GitHub KB commit failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "   → GitHub: %s\n", ghURL)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Phase 3: Build
	kbName := filepath.Base(filepath.Dir(kbPath))
	skillPath, err := builder.Run(ctx, client, cfg, kbName)
	if err != nil {
		return fmt.Errorf("skill building failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n✅ Skill built: %s\n", skillPath)

	// Commit skill to GitHub
	if store != nil {
		skillName := filepath.Base(filepath.Dir(skillPath))
		skillContent, err := os.ReadFile(skillPath)
		if err == nil {
			ghURL, err := store.WriteFile("skills/"+skillName+"/SKILL.md", string(skillContent), "Build: "+skillName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  GitHub skill commit failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "   → GitHub: %s\n", ghURL)
			}
		}
		// Also commit references/ if they exist
		refsDir := filepath.Join(filepath.Dir(skillPath), "references")
		if entries, err := os.ReadDir(refsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					refContent, err := os.ReadFile(filepath.Join(refsDir, e.Name()))
					if err == nil {
						store.WriteFile("skills/"+skillName+"/references/"+e.Name(), string(refContent), "Build ref: "+e.Name())
					}
				}
			}
		}
	}

	// Summary
	fmt.Fprintf(os.Stderr, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(os.Stderr, "📊 Pipeline Summary\n")
	fmt.Fprintf(os.Stderr, "   Articles: %d extracted\n", len(articlePaths))
	fmt.Fprintf(os.Stderr, "   KB:       %s\n", kbPath)
	fmt.Fprintf(os.Stderr, "   Skill:    %s\n", skillPath)
	if store != nil {
		fmt.Fprintf(os.Stderr, "   GitHub:   https://github.com/%s/%s\n", store.Owner(), store.Repo())
	}
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	return nil
}

func runExtract(client *copilot.Client, cfg config.Config, urls []string) error {
	ctx := context.Background()
	results, err := extractor.Run(ctx, client, cfg, urls)
	if err != nil {
		return err
	}

	for url, content := range results {
		slug := urlToSlug(url)
		articlePath := filepath.Join(cfg.ArticlesDir(), slug+".md")
		if err := os.WriteFile(articlePath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to save: %v\n", err)
			continue
		}
		fmt.Fprintf(os.Stderr, "📄 Saved: %s (%d chars)\n", articlePath, len(content))
		// Output path to stdout for piping
		fmt.Println(articlePath)
	}
	return nil
}

func runCurate(client *copilot.Client, cfg config.Config, args []string) error {
	ctx := context.Background()

	var articlePaths []string
	if len(args) > 0 {
		// Use provided paths
		for _, arg := range args {
			abs, err := filepath.Abs(arg)
			if err != nil {
				return fmt.Errorf("invalid path: %s", arg)
			}
			if _, err := os.Stat(abs); os.IsNotExist(err) {
				return fmt.Errorf("file not found: %s", abs)
			}
			articlePaths = append(articlePaths, abs)
		}
	} else {
		// Auto-discover articles
		entries, err := os.ReadDir(cfg.ArticlesDir())
		if err != nil {
			return fmt.Errorf("no articles found in %s", cfg.ArticlesDir())
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				articlePaths = append(articlePaths, filepath.Join(cfg.ArticlesDir(), entry.Name()))
			}
		}
		if len(articlePaths) == 0 {
			return fmt.Errorf("no .md articles found in %s", cfg.ArticlesDir())
		}
		fmt.Fprintf(os.Stderr, "📂 Auto-discovered %d article(s)\n", len(articlePaths))
	}

	kbPath, err := curator.Run(ctx, client, cfg, articlePaths)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✅ Knowledge base: %s\n", kbPath)
	fmt.Println(kbPath)
	return nil
}

func runBuild(client *copilot.Client, cfg config.Config, args []string) error {
	ctx := context.Background()

	if args[0] == "--auto" {
		paths, err := builder.RunAll(ctx, client, cfg)
		if err != nil {
			return err
		}
		for _, p := range paths {
			fmt.Println(p)
		}
		return nil
	}

	for _, kbName := range args {
		skillPath, err := builder.Run(ctx, client, cfg, kbName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to build %s: %v\n", kbName, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "✅ Skill built: %s\n", skillPath)
		fmt.Println(skillPath)
	}
	return nil
}

func runStatus(cfg config.Config) error {
	fmt.Fprintf(os.Stderr, "📊 Skill Builder Status\n")
	fmt.Fprintf(os.Stderr, "   Output: %s\n\n", cfg.OutputDir)

	// Articles
	articles := listDir(cfg.ArticlesDir())
	fmt.Fprintf(os.Stderr, "📄 Articles (%d):\n", len(articles))
	for _, a := range articles {
		fmt.Fprintf(os.Stderr, "   - %s\n", a)
	}

	// Knowledge bases
	kbs := listDirs(cfg.KnowledgeBasesDir())
	fmt.Fprintf(os.Stderr, "\n📚 Knowledge Bases (%d):\n", len(kbs))
	for _, kb := range kbs {
		fmt.Fprintf(os.Stderr, "   - %s\n", kb)
	}

	// Skills
	skills := listDirs(cfg.SkillsDir())
	fmt.Fprintf(os.Stderr, "\n🛠️  Skills (%d):\n", len(skills))
	for _, s := range skills {
		fmt.Fprintf(os.Stderr, "   - %s\n", s)
	}

	if len(articles) == 0 && len(kbs) == 0 && len(skills) == 0 {
		fmt.Fprintf(os.Stderr, "\n   (empty — run 'toskill run <url>' to get started)\n")
	}
	return nil
}

// --- Helpers ---

func runConfig(args []string) error {
	if len(args) == 0 || args[0] == "show" {
		cfgFile := config.ConfigFilePath()
		vals, err := config.LoadConfigFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "No config file found at %s\n", cfgFile)
			fmt.Fprintf(os.Stderr, "Set values with: toskill config set <key> <value>\n")
			fmt.Fprintf(os.Stderr, "\nValid keys: copilot-url, output, model\n")
			return nil
		}
		fmt.Fprintf(os.Stderr, "Config (%s):\n", cfgFile)
		for k, v := range vals {
			fmt.Fprintf(os.Stderr, "  %s = %s\n", k, v)
		}
		return nil
	}

	if args[0] == "set" {
		if len(args) < 3 {
			return fmt.Errorf("usage: toskill config set <key> <value>\nValid keys: copilot-url, output, model")
		}
		key, value := args[1], args[2]
		validKeys := map[string]bool{"copilot-url": true, "output": true, "model": true, "github-repo": true, "github-token": true, "extract-model": true, "curate-model": true, "build-model": true}
		if !validKeys[key] {
			return fmt.Errorf("unknown config key: %s (valid: copilot-url, output, model)", key)
		}
		if err := config.SaveConfigValue(key, value); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "✅ Set %s = %s\n", key, value)
		return nil
	}

	if args[0] == "path" {
		fmt.Println(config.ConfigFilePath())
		return nil
	}

	return fmt.Errorf("unknown config subcommand: %s (valid: show, set, path)", args[0])
}

func connectCopilot(url string) *copilot.Client {
	opts := &copilot.ClientOptions{
		CLIUrl:   url,
		LogLevel: "error",
	}
	client := copilot.NewClient(opts)

	fmt.Fprintf(os.Stderr, "🔌 Connecting to Copilot CLI at %s...\n", url)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to connect: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nTip: Start Copilot CLI first:\n")
		fmt.Fprintf(os.Stderr, "  copilot --headless --port 44321\n\n")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✅ Connected\n\n")
	return client
}

func urlToSlug(url string) string {
	slug := strings.ReplaceAll(url, "https://", "")
	slug = strings.ReplaceAll(slug, "http://", "")
	replacer := strings.NewReplacer("/", "-", ".", "-", "?", "-", "&", "-", "=", "-", "#", "-")
	slug = replacer.Replace(slug)
	if len(slug) > 80 {
		slug = slug[:80]
	}
	return strings.Trim(slug, "-")
}

func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	return names
}

func listDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
}

func runInteractive() {
	// Load saved GitHub repo from config for pre-fill
	savedRepo := ""
	if fileCfg, err := config.LoadConfigFile(); err == nil {
		savedRepo = fileCfg["github-repo"]
	}

	result, err := interactive.RunWizard(savedRepo)
	if err != nil {
		fatal("Interactive wizard failed: %v", err)
	}
	if !result.Confirmed {
		fmt.Fprintf(os.Stderr, "\n👋 Cancelled.\n")
		os.Exit(0)
	}

	// Build config from wizard result
	cfg := config.DefaultConfig()
	cfg.Model = result.Model
	if result.ExtractModel != "" {
		cfg.ExtractModel = result.ExtractModel
	}
	if result.CurateModel != "" {
		cfg.CurateModel = result.CurateModel
	}
	if result.BuildModel != "" {
		cfg.BuildModel = result.BuildModel
	}

	// GitHub storage
	var store *ghstore.GitHubStore
	if result.StorageMode == "github" && result.GitHubRepo != "" {
		ghToken := result.GitHubToken
		if ghToken == "" {
			// Fallback: try gh CLI, then env, then config
			ghToken = ghauth.GetToken()
		}
		if ghToken == "" {
			fmt.Fprintf(os.Stderr, "⚠️  No GitHub token found. Falling back to local storage.\n\n")
		} else {
			store = ghstore.New(ghToken, result.GitHubRepo)
		}
	}

	if err := cfg.EnsureDirs(); err != nil {
		fatal("Failed to create output directories: %v", err)
	}

	client := connectCopilot(cfg.CopilotURL)
	if err := runPipeline(client, cfg, store, result.URLs); err != nil {
		fatal("Error: %v", err)
	}
	fmt.Fprintf(os.Stderr, "\n🎉 Done.\n")
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `toskill — Autonomous AI Skill Builder (%s)

Transform web articles into structured, reusable AI skills.

Usage:
  toskill                        Interactive mode (guided setup)
  toskill <command> [flags] [args...]

Commands:
  run <url1> [url2] ...     Full pipeline: extract → curate → build
  run                       Interactive mode (no URLs → launches wizard)
  extract <url1> [url2] ... Extract content from URLs
  curate [article-paths]    Curate articles into a knowledge base
  build <kb-name>           Build a skill from a knowledge base
  build --auto              Build skills from all knowledge bases
  status                    Show pipeline state (articles, KBs, skills)
  config show               Show current configuration
  config set <key> <value>  Set a config value
  config path               Print config file path
  version                   Print version

Flags:
  --copilot-url <addr>     Copilot CLI server (default: $COPILOT_CLI_URL or localhost:44321)
  --output <dir>           Output directory (default: ./skill-store/)
  --model <name>           LLM model for all phases (default: claude-opus-4.6)
  --extract-model <name>   Model override for extraction phase
  --curate-model <name>    Model override for curation phase
  --build-model <name>     Model override for skill building phase
  --github-repo <repo>     GitHub repo for storage (e.g. 'owner/toskill-store')
  --github-token <tok>     GitHub token (or $GITHUB_TOKEN)
  --verbose                Enable verbose output

Examples:
  # Interactive mode
  toskill

  # Full pipeline
  toskill run https://example.com/article1 https://example.com/article2

  # With GitHub storage
  toskill run --github-repo myuser/my-skills --github-token ghp_xxx https://example.com/article

  # Per-phase models (cheap extraction, premium building)
  toskill run --extract-model claude-haiku-4.5 --build-model claude-opus-4.6 https://example.com/article

  # Individual phases
  toskill extract https://example.com/article
  toskill curate
  toskill build idor-vulnerabilities

Environment:
  COPILOT_CLI_URL           Copilot CLI server address
  TOSKILL_OUTPUT            Default output directory
  TOSKILL_MODEL             Default model
  GITHUB_TOKEN              GitHub token for storage

Requires:
  - GitHub Copilot CLI running: copilot --headless --port 44321
  - Node.js 18+ (for npx skills)
`, version)
}
