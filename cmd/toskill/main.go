package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	copilot "github.com/github/copilot-sdk/go"

	"github.com/byadhddev/toskill/pkg/builder"
	"github.com/byadhddev/toskill/pkg/config"
	"github.com/byadhddev/toskill/pkg/curator"
	"github.com/byadhddev/toskill/pkg/extractor"
	"github.com/byadhddev/toskill/pkg/ghauth"
	"github.com/byadhddev/toskill/pkg/ghstore"
	"github.com/byadhddev/toskill/pkg/headless"
	"github.com/byadhddev/toskill/pkg/interactive"
	"github.com/byadhddev/toskill/pkg/tools"
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
	authMethod := fs.String("auth", "", "Auth method: auto|cli-url|github-token|env-var|byok (default: auto)")
	copilotToken := fs.String("copilot-token", "", "GitHub token for Copilot auth (with --auth github-token)")
	byokProvider := fs.String("byok-provider", "", "BYOK provider type: openai|anthropic|azure (with --auth byok)")
	byokURL := fs.String("byok-url", "", "BYOK provider base URL (with --auth byok)")
	byokKey := fs.String("byok-key", "", "BYOK API key (with --auth byok)")
	evolve := fs.Bool("evolve", false, "Evolve an existing skill instead of creating new")
	skillName := fs.String("skill-name", "", "Name of existing skill to evolve (with --evolve)")
	redact := fs.Bool("redact", false, "Redact home directory paths in output (show ~ instead)")
	fs.Parse(os.Args[2:])

	cfg := config.DefaultConfig()
	if *copilotURL != "" {
		cfg.CopilotURL = *copilotURL
		if cfg.AuthMethod == config.AuthAuto {
			cfg.AuthMethod = config.AuthCLIUrl
		}
	}
	if *authMethod != "" {
		cfg.AuthMethod = *authMethod
	}
	if *copilotToken != "" {
		cfg.GitHubCopilotToken = *copilotToken
		if cfg.AuthMethod == config.AuthAuto {
			cfg.AuthMethod = config.AuthGitHubToken
		}
	}
	if *byokProvider != "" || *byokURL != "" || *byokKey != "" {
		cfg.BYOKProvider = &copilot.ProviderConfig{
			Type:    *byokProvider,
			BaseURL: *byokURL,
			APIKey:  *byokKey,
		}
		if cfg.AuthMethod == config.AuthAuto {
			cfg.AuthMethod = config.AuthBYOK
		}
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
	if *evolve {
		cfg.SkillMode = "evolve"
		cfg.EvolveSkill = *skillName
	}
	if *redact {
		cfg.RedactPaths = true
	}
	tools.SetRedact(cfg.RedactPaths)

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
		client := createClient(cfg)
		err = runPipeline(client, cfg, store, args)

	case "extract":
		if len(args) == 0 {
			fatal("No URLs provided.\nUsage: toskill extract [flags] <url1> [url2] ...")
		}
		client := createClient(cfg)
		err = runExtract(client, cfg, args)

	case "curate":
		client := createClient(cfg)
		err = runCurate(client, cfg, args)

	case "build":
		if len(args) == 0 {
			fatal("No KB name provided.\nUsage: toskill build [flags] <kb-name>")
		}
		client := createClient(cfg)
		err = runBuild(client, cfg, args)

	case "status":
		err = runStatus(cfg)

	case "remove", "rm":
		if store == nil {
			store = promptGitHubStore()
		}
		err = runRemove(cfg, store)

	case "reset":
		if store == nil {
			store = promptGitHubStore()
		}
		err = runReset(cfg, store)

	case "config":
		err = runConfig(args)

	default:
		fmt.Fprintf(os.Stderr, "❌ Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		headless.Stop()
		fatal("Error: %v", err)
	}

	headless.Stop()
	fmt.Fprintf(os.Stderr, "\n🎉 Done.\n")
}

// --- Commands ---

func runPipeline(client *copilot.Client, cfg config.Config, store *ghstore.GitHubStore, urls []string) error {
	fmt.Fprintf(os.Stderr, "🚀 Starting full pipeline for %d URL(s)\n", len(urls))
	if store != nil {
		fmt.Fprintf(os.Stderr, "   Storage: GitHub (%s/%s)\n", store.Owner(), store.Repo())
		fmt.Fprintf(os.Stderr, "   Working dir: %s\n", cfg.Redact(cfg.OutputDir))
	} else {
		fmt.Fprintf(os.Stderr, "   Output: %s\n", cfg.Redact(cfg.OutputDir))
	}
	fmt.Fprintf(os.Stderr, "   Model: %s\n", cfg.Model)
	if cfg.SkillMode == "evolve" {
		fmt.Fprintf(os.Stderr, "   Mode: evolve (skill: %s)\n", cfg.EvolveSkill)
	}
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
		fmt.Fprintf(os.Stderr, "📄 Saved: %s\n", cfg.Redact(articlePath))

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
	fmt.Fprintf(os.Stderr, "\n✅ Knowledge base: %s\n", cfg.Redact(kbPath))

	// Commit KB to GitHub
	if store != nil {
		kbName := filepath.Base(filepath.Dir(kbPath))
		kbContent, err := os.ReadFile(kbPath)
		if err == nil {
			ghURL, err := store.WriteFile("knowledge-bases/"+kbName+"/KB.md", string(kbContent), "Curate: "+kbName)
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
	fmt.Fprintf(os.Stderr, "\n✅ Skill built: %s\n", cfg.Redact(skillPath))

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
	fmt.Fprintf(os.Stderr, "   KB:       %s\n", cfg.Redact(kbPath))
	fmt.Fprintf(os.Stderr, "   Skill:    %s\n", cfg.Redact(skillPath))
	if store != nil {
		fmt.Fprintf(os.Stderr, "   GitHub:   https://github.com/%s/%s\n", store.Owner(), store.Repo())
	}
	tools.GlobalTracker.PrintSummary()
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
		fmt.Fprintf(os.Stderr, "📄 Saved: %s (%d chars)\n", cfg.Redact(articlePath), len(content))
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
	fmt.Fprintf(os.Stderr, "✅ Knowledge base: %s\n", cfg.Redact(kbPath))
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
		fmt.Fprintf(os.Stderr, "✅ Skill built: %s\n", cfg.Redact(skillPath))
		fmt.Println(skillPath)
	}
	return nil
}

func runStatus(cfg config.Config) error {
	fmt.Fprintf(os.Stderr, "📊 Skill Builder Status\n")
	fmt.Fprintf(os.Stderr, "   Output: %s\n\n", cfg.Redact(cfg.OutputDir))

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

func runRemove(cfg config.Config, store *ghstore.GitHubStore) error {
	type artifact struct {
		Category string // "article", "knowledge-base", "skill"
		Name     string
		LocalDir string // empty if only on GitHub
		GHPrefix string // empty if only local
		HasLocal bool
		HasGH    bool
	}

	seen := make(map[string]*artifact) // key: "category/name"

	// Collect local artifacts
	for _, a := range listDir(cfg.ArticlesDir()) {
		key := "article/" + a
		seen[key] = &artifact{"article", a, filepath.Join(cfg.ArticlesDir(), a), "articles/" + a, true, false}
	}
	for _, kb := range listDirs(cfg.KnowledgeBasesDir()) {
		key := "knowledge-base/" + kb
		seen[key] = &artifact{"knowledge-base", kb, filepath.Join(cfg.KnowledgeBasesDir(), kb), "knowledge-bases/" + kb, true, false}
	}
	for _, s := range listDirs(cfg.SkillsDir()) {
		key := "skill/" + s
		seen[key] = &artifact{"skill", s, filepath.Join(cfg.SkillsDir(), s), "skills/" + s, true, false}
	}

	// Collect GitHub artifacts
	if store != nil {
		fmt.Fprintf(os.Stderr, "🔍 Scanning GitHub repo...\n")
		if files, err := store.ListFiles("articles"); err == nil {
			for _, f := range files {
				key := "article/" + f
				if a, ok := seen[key]; ok {
					a.HasGH = true
				} else {
					seen[key] = &artifact{"article", f, "", "articles/" + f, false, true}
				}
			}
		}
		if dirs, err := store.ListSubDirs("knowledge-bases"); err == nil {
			for _, d := range dirs {
				key := "knowledge-base/" + d
				if a, ok := seen[key]; ok {
					a.HasGH = true
				} else {
					seen[key] = &artifact{"knowledge-base", d, "", "knowledge-bases/" + d, false, true}
				}
			}
		}
		if dirs, err := store.ListSubDirs("skills"); err == nil {
			for _, d := range dirs {
				key := "skill/" + d
				if a, ok := seen[key]; ok {
					a.HasGH = true
				} else {
					seen[key] = &artifact{"skill", d, "", "skills/" + d, false, true}
				}
			}
		}
	}

	// Flatten to slice
	var all []artifact
	for _, a := range seen {
		all = append(all, *a)
	}

	if len(all) == 0 {
		fmt.Fprintf(os.Stderr, "📭 Nothing to remove — store is empty.\n")
		return nil
	}

	// Build options for multi-select
	var options []huh.Option[int]
	for i, a := range all {
		icon := map[string]string{"article": "📄", "knowledge-base": "📚", "skill": "🛠️"}[a.Category]
		loc := ""
		switch {
		case a.HasLocal && a.HasGH:
			loc = "local + GitHub"
		case a.HasLocal:
			loc = "local"
		case a.HasGH:
			loc = "GitHub"
		}
		label := fmt.Sprintf("%s %s (%s) [%s]", icon, a.Name, a.Category, loc)
		options = append(options, huh.NewOption(label, i))
	}

	var selected []int
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[int]().
				Title("🗑️  Select items to remove").
				Description("Space to toggle, Enter to confirm").
				Options(options...).
				Value(&selected),
		),
	).WithTheme(huh.ThemeCharm()).Run()
	if err != nil {
		return err
	}

	if len(selected) == 0 {
		fmt.Fprintf(os.Stderr, "Nothing selected.\n")
		return nil
	}

	// Confirm
	var confirm bool
	targets := make([]string, len(selected))
	for i, idx := range selected {
		targets[i] = all[idx].Name
	}
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete %d item(s)?", len(selected))).
				Description(strings.Join(targets, ", ")).
				Affirmative("Yes, delete").
				Negative("Cancel").
				Value(&confirm),
		),
	).WithTheme(huh.ThemeCharm()).Run()
	if err != nil || !confirm {
		fmt.Fprintf(os.Stderr, "Cancelled.\n")
		return nil
	}

	// Delete
	for _, idx := range selected {
		a := all[idx]
		if a.HasLocal && a.LocalDir != "" {
			if err := os.RemoveAll(a.LocalDir); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Local delete failed for %s: %v\n", a.Name, err)
			} else {
				fmt.Fprintf(os.Stderr, "🗑️  Removed local: %s\n", a.Name)
			}
		}
		if a.HasGH && store != nil {
			if err := store.DeleteDir(a.GHPrefix, "Remove: "+a.Name); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  GitHub delete failed for %s: %v\n", a.Name, err)
			} else {
				fmt.Fprintf(os.Stderr, "   → Removed from GitHub: %s\n", a.GHPrefix)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n✅ Removed %d item(s)\n", len(selected))
	return nil
}

func runReset(cfg config.Config, store *ghstore.GitHubStore) error {
	// Count artifacts
	articles := listDir(cfg.ArticlesDir())
	kbs := listDirs(cfg.KnowledgeBasesDir())
	skills := listDirs(cfg.SkillsDir())
	localTotal := len(articles) + len(kbs) + len(skills)

	summary := fmt.Sprintf("Local: %d articles, %d KBs, %d skills in %s",
		len(articles), len(kbs), len(skills), cfg.Redact(cfg.OutputDir))

	ghTotal := 0
	if store != nil {
		fmt.Fprintf(os.Stderr, "🔍 Scanning GitHub repo...\n")
		var ghArticles, ghKBs, ghSkills int
		if files, err := store.ListFiles("articles"); err == nil {
			ghArticles = len(files)
		}
		if dirs, err := store.ListSubDirs("knowledge-bases"); err == nil {
			ghKBs = len(dirs)
		}
		if dirs, err := store.ListSubDirs("skills"); err == nil {
			ghSkills = len(dirs)
		}
		ghTotal = ghArticles + ghKBs + ghSkills
		summary += fmt.Sprintf("\nGitHub: %d articles, %d KBs, %d skills in %s/%s",
			ghArticles, ghKBs, ghSkills, store.Owner(), store.Repo())
	}

	if localTotal == 0 && ghTotal == 0 {
		fmt.Fprintf(os.Stderr, "📭 Nothing to reset — store is already empty.\n")
		return nil
	}

	// What to reset
	resetChoices := []huh.Option[string]{
		huh.NewOption("Local store only", "local"),
	}
	if store != nil {
		resetChoices = append(resetChoices,
			huh.NewOption("GitHub repo only", "github"),
			huh.NewOption("Both local and GitHub", "both"),
		)
	}

	var resetTarget string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("⚠️  Reset — delete all artifacts").
				Description(summary).
				Options(resetChoices...).
				Value(&resetTarget),
		),
	).WithTheme(huh.ThemeCharm()).Run()
	if err != nil {
		return err
	}

	var confirm bool
	confirmMsg := "This will permanently delete all "
	switch resetTarget {
	case "local":
		confirmMsg += "local artifacts"
	case "github":
		confirmMsg += fmt.Sprintf("files in %s/%s", store.Owner(), store.Repo())
	case "both":
		confirmMsg += fmt.Sprintf("local artifacts AND files in %s/%s", store.Owner(), store.Repo())
	}

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Are you sure?").
				Description(confirmMsg).
				Affirmative("Yes, reset everything").
				Negative("Cancel").
				Value(&confirm),
		),
	).WithTheme(huh.ThemeCharm()).Run()
	if err != nil || !confirm {
		fmt.Fprintf(os.Stderr, "Cancelled.\n")
		return nil
	}

	// Execute reset
	if resetTarget == "local" || resetTarget == "both" {
		for _, dir := range []string{cfg.ArticlesDir(), cfg.KnowledgeBasesDir(), cfg.SkillsDir()} {
			os.RemoveAll(dir)
			os.MkdirAll(dir, 0755)
		}
		fmt.Fprintf(os.Stderr, "🗑️  Local store cleared: %s\n", cfg.Redact(cfg.OutputDir))
	}

	if (resetTarget == "github" || resetTarget == "both") && store != nil {
		fmt.Fprintf(os.Stderr, "🗑️  Clearing GitHub repo...\n")
		for _, prefix := range []string{"articles", "knowledge-bases", "skills"} {
			if err := store.DeleteDir(prefix, "Reset: clear "+prefix); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Failed to clear %s: %v\n", prefix, err)
			} else {
				fmt.Fprintf(os.Stderr, "   → Cleared %s/\n", prefix)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n✅ Reset complete\n")
	return nil
}

// --- Helpers ---

// promptGitHubStore asks the user if they want to include GitHub storage for remove/reset.
func promptGitHubStore() *ghstore.GitHubStore {
	var includeGH bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("🐙 Include GitHub storage?").
				Description("Also remove/reset artifacts from a GitHub repo?").
				Affirmative("Yes").
				Negative("No, local only").
				Value(&includeGH),
		),
	).WithTheme(huh.ThemeCharm()).Run()
	if err != nil || !includeGH {
		return nil
	}

	// Try to get token
	token := ghauth.GetToken()
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		fmt.Fprintf(os.Stderr, "⚠️  No GitHub token found. Run 'gh auth login' first or set GITHUB_TOKEN.\n")
		return nil
	}

	// Get repo name — try config first, then ask
	repo := ""
	if fileCfg, err := config.LoadConfigFile(); err == nil {
		repo = fileCfg["github-repo"]
	}

	if repo == "" {
		// List user's repos for selection
		fmt.Fprintf(os.Stderr, "🔍 Loading your repositories...\n")
		repos, listErr := ghauth.ListRepos(30)
		if listErr == nil && len(repos) > 0 {
			opts := make([]huh.Option[string], 0, len(repos)+1)
			for _, r := range repos {
				opts = append(opts, huh.NewOption(r, r))
			}
			opts = append(opts, huh.NewOption("⌨️  Enter manually", "__manual__"))
			err = huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("📦 Select GitHub repo").
						Description("Choose a repo for your toskill store").
						Options(opts...).
						Value(&repo),
				),
			).WithTheme(huh.ThemeCharm()).Run()
			if err != nil {
				return nil
			}
		}
		if repo == "__manual__" || repo == "" {
			repo = ""
			err = huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("GitHub repo").
						Description("owner/repo for your toskill store").
						Placeholder("yourname/toskill-store").
						Value(&repo),
				),
			).WithTheme(huh.ThemeCharm()).Run()
			if err != nil || repo == "" {
				return nil
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "📦 Using GitHub repo from config: %s\n", repo)
	}

	return ghstore.New(token, repo)
}

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
		validKeys := map[string]bool{"copilot-url": true, "output": true, "model": true, "github-repo": true, "github-token": true, "extract-model": true, "curate-model": true, "build-model": true, "auth-method": true}
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

func createClient(cfg config.Config) *copilot.Client {
	var opts *copilot.ClientOptions

	switch cfg.AuthMethod {
	case config.AuthCLIUrl:
		// Headless already started by wizard or ensureRunning
		addr := headless.EnsureRunning(cfg.CopilotURL)
		if addr == "" {
			fatal("Could not start Copilot CLI headless server")
		}
		opts = &copilot.ClientOptions{
			CLIUrl:   addr,
			LogLevel: "error",
		}

	case config.AuthGitHubToken:
		fmt.Fprintf(os.Stderr, "🔑 Starting Copilot with GitHub token...\n")
		opts = &copilot.ClientOptions{
			GitHubToken:     cfg.GitHubCopilotToken,
			UseLoggedInUser: copilot.Bool(false),
			LogLevel:        "error",
		}

	case config.AuthEnvVar:
		fmt.Fprintf(os.Stderr, "🔑 Starting Copilot with environment variable token...\n")
		opts = &copilot.ClientOptions{
			UseLoggedInUser: copilot.Bool(false),
			LogLevel:        "error",
		}

	case config.AuthBYOK:
		fmt.Fprintf(os.Stderr, "🔑 Starting Copilot in BYOK mode (%s)...\n", cfg.BYOKProvider.Type)
		opts = &copilot.ClientOptions{
			LogLevel: "error",
		}

	default: // AuthAuto
		addr := headless.EnsureRunning(cfg.CopilotURL)
		if addr == "" {
			fatal("Could not start Copilot CLI headless server")
		}
		opts = &copilot.ClientOptions{
			CLIUrl:   addr,
			LogLevel: "error",
		}
	}

	client := copilot.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to connect: %v\n", err)
		switch cfg.AuthMethod {
		case config.AuthCLIUrl:
			fmt.Fprintf(os.Stderr, "\nTip: Start Copilot CLI first:\n")
			fmt.Fprintf(os.Stderr, "  copilot --headless --port 44321\n\n")
		case config.AuthGitHubToken:
			fmt.Fprintf(os.Stderr, "\nTip: Check your token is valid (gho_, ghu_, or github_pat_ prefix)\n\n")
		case config.AuthEnvVar:
			fmt.Fprintf(os.Stderr, "\nTip: Set one of: COPILOT_GITHUB_TOKEN, GH_TOKEN, or GITHUB_TOKEN\n\n")
		case config.AuthBYOK:
			fmt.Fprintf(os.Stderr, "\nTip: Check your API key and base URL\n\n")
		default:
			fmt.Fprintf(os.Stderr, "\nTip: Run 'copilot' to sign in, or use --auth cli-url with a headless server\n\n")
		}
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
	headless.Stop()
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
}

func runInteractive() {
	// Load saved config
	savedRepo := ""
	if fileCfg, err := config.LoadConfigFile(); err == nil {
		savedRepo = fileCfg["github-repo"]
	}

	// Wizard handles auth selection, model fetching, and all config
	result, err := interactive.RunWizard(savedRepo, nil)
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
	cfg.AuthMethod = result.AuthMethod
	cfg.CopilotURL = result.CopilotURL
	cfg.GitHubCopilotToken = result.GitHubCopilotToken
	cfg.BYOKProvider = result.BYOKProvider
	if result.ExtractModel != "" {
		cfg.ExtractModel = result.ExtractModel
	}
	if result.CurateModel != "" {
		cfg.CurateModel = result.CurateModel
	}
	if result.BuildModel != "" {
		cfg.BuildModel = result.BuildModel
	}
	cfg.SkillMode = result.SkillMode
	cfg.EvolveSkill = result.EvolveSkill
	tools.SetRedact(cfg.RedactPaths)

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

	client := createClient(cfg)
	if err := runPipeline(client, cfg, store, result.URLs); err != nil {
		fatal("Error: %v", err)
	}
	headless.Stop()
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
  remove                    Interactively select and delete artifacts
  reset                     Wipe all artifacts (local and/or GitHub)
  config show               Show current configuration
  config set <key> <value>  Set a config value
  config path               Print config file path
  version                   Print version

Authentication:
  --auth <method>           Auth method (default: auto)
    auto                      SDK auto-manages CLI (recommended)
    cli-url                   Connect to external headless CLI server
    github-token              Explicit GitHub token (SDK starts CLI)
    env-var                   Use COPILOT_GITHUB_TOKEN / GH_TOKEN / GITHUB_TOKEN
    byok                      Bring your own key (OpenAI / Anthropic / Azure)
  --copilot-url <addr>      Headless CLI address (with --auth cli-url)
  --copilot-token <token>   GitHub token for Copilot (with --auth github-token)
  --byok-provider <type>    Provider: openai|anthropic|azure (with --auth byok)
  --byok-url <url>          Provider base URL (with --auth byok)
  --byok-key <key>          Provider API key (with --auth byok)

Flags:
  --output <dir>           Output directory (default: ./skill-store/)
  --model <name>           LLM model for all phases (default: claude-opus-4.6)
  --extract-model <name>   Model override for extraction phase
  --curate-model <name>    Model override for curation phase
  --build-model <name>     Model override for skill building phase
  --github-repo <repo>     GitHub repo for storage (e.g. 'owner/toskill-store')
  --github-token <tok>     GitHub token for storage (or $GITHUB_TOKEN)
  --evolve                 Evolve an existing skill instead of creating new
  --skill-name <name>      Name of existing skill to evolve (with --evolve)
  --redact                 Replace home directory with ~ in all output paths
  --verbose                Enable verbose output

Examples:
  # Interactive mode (recommended — guided setup with all options)
  toskill

  # Auto auth: SDK manages everything (no manual headless server needed)
  toskill run https://example.com/article

  # External headless CLI server
  toskill run --auth cli-url --copilot-url localhost:44321 https://example.com/article

  # Explicit GitHub token
  toskill run --copilot-token gho_xxxx https://example.com/article

  # BYOK with OpenAI
  toskill run --auth byok --byok-provider openai --byok-url https://api.openai.com/v1 \
    --byok-key sk-xxx --model gpt-4o https://example.com/article

  # Per-phase models
  toskill run --extract-model claude-haiku-4.5 --build-model claude-opus-4.6 https://example.com/article

  # With GitHub storage
  toskill run --github-repo myuser/my-skills https://example.com/article

  # Evolve an existing skill with new content
  toskill run --evolve --skill-name my-skill https://example.com/new-article

Environment:
  COPILOT_CLI_URL           Copilot CLI server address (sets auth to cli-url)
  COPILOT_GITHUB_TOKEN      GitHub token for Copilot auth (highest priority)
  GH_TOKEN                  GitHub CLI compatible token
  GITHUB_TOKEN              GitHub Actions compatible token
  TOSKILL_OUTPUT            Default output directory
  TOSKILL_MODEL             Default model

Auth priority (auto mode):
  1. Explicit token (--copilot-token)
  2. Env vars: COPILOT_GITHUB_TOKEN → GH_TOKEN → GITHUB_TOKEN
  3. Stored Copilot CLI OAuth credentials
  4. gh CLI auth (gh auth token)
`, version)
}
