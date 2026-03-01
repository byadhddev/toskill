package interactive

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// RunConfig holds the collected configuration from the interactive wizard.
type RunConfig struct {
	URLs          []string
	StorageMode   string // "local" or "github"
	GitHubRepo    string
	OutputDir     string
	Model         string
	ExtractModel  string
	CurateModel   string
	BuildModel    string
	Confirmed     bool
}

// Available models for selection
var defaultModels = []huh.Option[string]{
	huh.NewOption("claude-opus-4.6 (Recommended)", "claude-opus-4.6"),
	huh.NewOption("claude-sonnet-4.5", "claude-sonnet-4.5"),
	huh.NewOption("claude-sonnet-4", "claude-sonnet-4"),
	huh.NewOption("gpt-4.1", "gpt-4.1"),
	huh.NewOption("claude-haiku-4.5 (Fast/Cheap)", "claude-haiku-4.5"),
}

var theme = huh.ThemeCharm()

// RunWizard launches the interactive configuration wizard.
func RunWizard(savedGitHubRepo string) (*RunConfig, error) {
	cfg := &RunConfig{
		StorageMode: "local",
		GitHubRepo:  savedGitHubRepo,
		OutputDir:   "./skill-store",
		Model:       "claude-opus-4.6",
	}

	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Render("⚡ toskill — Autonomous Skill Builder")
	fmt.Println(banner)
	fmt.Println()

	// --- Page 1: URLs ---
	var urlsRaw string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Title("🔗 URLs to process").
				Description("Paste one URL per line").
				CharLimit(2000).
				Value(&urlsRaw).
				Validate(func(s string) error {
					lines := parseURLs(s)
					if len(lines) == 0 {
						return fmt.Errorf("enter at least one URL")
					}
					for _, u := range lines {
						if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
							return fmt.Errorf("invalid URL: %s", u)
						}
					}
					return nil
				}),
		),
	).WithTheme(theme).Run()
	if err != nil {
		return nil, err
	}
	cfg.URLs = parseURLs(urlsRaw)

	// --- Page 2: Storage ---
	storageOptions := []huh.Option[string]{
		huh.NewOption("Local (./skill-store/)", "local"),
		huh.NewOption("GitHub Repository", "github"),
	}
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("📦 Storage").
				Description("Where should artifacts be saved?").
				Options(storageOptions...).
				Value(&cfg.StorageMode),
		),
	).WithTheme(theme).Run()
	if err != nil {
		return nil, err
	}

	if cfg.StorageMode == "github" {
		if cfg.GitHubRepo == "" {
			cfg.GitHubRepo = "your-username/toskill-store"
		}
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("GitHub Repository").
					Description("Format: owner/repo-name").
					Placeholder("owner/toskill-store").
					Value(&cfg.GitHubRepo).
					Validate(func(s string) error {
						if !strings.Contains(s, "/") {
							return fmt.Errorf("use format: owner/repo-name")
						}
						return nil
					}),
			),
		).WithTheme(theme).Run()
		if err != nil {
			return nil, err
		}
	}

	// --- Page 3: Model ---
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("🧠 Model").
				Description("LLM for all pipeline phases").
				Options(defaultModels...).
				Value(&cfg.Model),
		),
	).WithTheme(theme).Run()
	if err != nil {
		return nil, err
	}

	// --- Page 4: Per-phase models (optional) ---
	var perPhase bool
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("⚙️  Use different models per phase?").
				Description("e.g. fast model for extraction, premium for skill building").
				Affirmative("Yes").
				Negative("No").
				Value(&perPhase),
		),
	).WithTheme(theme).Run()
	if err != nil {
		return nil, err
	}

	if perPhase {
		cfg.ExtractModel = cfg.Model
		cfg.CurateModel = cfg.Model
		cfg.BuildModel = cfg.Model

		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("🔍 Extract Model").
					Description("For browsing and content extraction").
					Options(defaultModels...).
					Value(&cfg.ExtractModel),
				huh.NewSelect[string]().
					Title("📚 Curate Model").
					Description("For knowledge base creation").
					Options(defaultModels...).
					Value(&cfg.CurateModel),
				huh.NewSelect[string]().
					Title("🛠️  Build Model").
					Description("For skill generation").
					Options(defaultModels...).
					Value(&cfg.BuildModel),
			),
		).WithTheme(theme).Run()
		if err != nil {
			return nil, err
		}
	}

	// --- Summary & Confirm ---
	storageSummary := "Local (./skill-store/)"
	if cfg.StorageMode == "github" {
		storageSummary = fmt.Sprintf("GitHub (%s)", cfg.GitHubRepo)
	}
	modelSummary := cfg.Model
	if perPhase {
		modelSummary = fmt.Sprintf("Extract: %s, Curate: %s, Build: %s",
			cfg.ExtractModel, cfg.CurateModel, cfg.BuildModel)
	}

	summaryStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(0, 1)

	summary := fmt.Sprintf(
		"  URLs:     %d\n  Storage:  %s\n  Model:    %s",
		len(cfg.URLs), storageSummary, modelSummary,
	)
	fmt.Println(summaryStyle.Render(summary))
	fmt.Println()

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("🚀 Run pipeline?").
				Affirmative("Yes, let's go!").
				Negative("Cancel").
				Value(&cfg.Confirmed),
		),
	).WithTheme(theme).Run()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func parseURLs(raw string) []string {
	var urls []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			urls = append(urls, line)
		}
	}
	return urls
}
