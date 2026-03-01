package interactive

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	copilot "github.com/github/copilot-sdk/go"

	"github.com/byadhddev/toskill/pkg/config"
	"github.com/byadhddev/toskill/pkg/ghauth"
)

// RunConfig holds the collected configuration from the interactive wizard.
type RunConfig struct {
	URLs          []string
	StorageMode   string // "local" or "github"
	GitHubRepo    string
	GitHubToken   string
	CreateRepo    bool   // true if user chose to create a new repo
	OutputDir     string
	Model         string
	ExtractModel  string
	CurateModel   string
	BuildModel    string
	Confirmed     bool

	// Auth
	AuthMethod         string                  // config.Auth* constant
	CopilotURL         string                  // for AuthCLIUrl
	GitHubCopilotToken string                  // for AuthGitHubToken
	BYOKProvider       *copilot.ProviderConfig // for AuthBYOK
}

// fallbackModels used by legacy FetchModels() only (non-interactive CLI flag mode).
var fallbackModels = []huh.Option[string]{
	huh.NewOption("claude-opus-4.6", "claude-opus-4.6"),
	huh.NewOption("claude-sonnet-4.5", "claude-sonnet-4.5"),
	huh.NewOption("gpt-4.1", "gpt-4.1"),
	huh.NewOption("claude-haiku-4.5", "claude-haiku-4.5"),
}

var theme = huh.ThemeCharm()

// FetchModelsWithAuth creates a temporary client using the selected auth method to get models.
// Returns the model list and whether it succeeded (vs fallback).
func FetchModelsWithAuth(authMethod string, copilotURL string, githubToken string) ([]huh.Option[string], bool) {
	var opts *copilot.ClientOptions

	switch authMethod {
	case config.AuthCLIUrl:
		opts = &copilot.ClientOptions{
			CLIUrl:   copilotURL,
			LogLevel: "error",
		}
	case config.AuthGitHubToken:
		opts = &copilot.ClientOptions{
			GitHubToken:     githubToken,
			UseLoggedInUser: copilot.Bool(false),
			LogLevel:        "error",
		}
	case config.AuthBYOK:
		return nil, true // BYOK uses custom model names
	default: // AuthAuto, AuthEnvVar
		opts = &copilot.ClientOptions{
			LogLevel: "error",
		}
	}

	client := copilot.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "   ❌ Could not connect: %v\n", err)
		return nil, false
	}
	defer client.Stop()

	models, err := client.ListModels(ctx)
	if err != nil || len(models) == 0 {
		fmt.Fprintf(os.Stderr, "   ❌ No models returned\n")
		return nil, false
	}

	options := make([]huh.Option[string], 0, len(models))
	for _, m := range models {
		label := m.Name
		if label == "" {
			label = m.ID
		}
		options = append(options, huh.NewOption(label, m.ID))
	}
	return options, true
}

// FetchModels connects to Copilot CLI via CLIUrl and returns available models (legacy).
func FetchModels(copilotURL string) []huh.Option[string] {
	models, _ := FetchModelsWithAuth(config.AuthCLIUrl, copilotURL, "")
	if len(models) == 0 {
		return fallbackModels
	}
	return models
}

// RunWizard launches the interactive configuration wizard.
func RunWizard(savedGitHubRepo string, modelOptions []huh.Option[string]) (*RunConfig, error) {
	cfg := &RunConfig{
		StorageMode: "local",
		GitHubRepo:  savedGitHubRepo,
		OutputDir:   "./skill-store",
		Model:       "claude-opus-4.6",
		AuthMethod:  config.AuthAuto,
		CopilotURL:  "localhost:44321",
	}

	if len(modelOptions) == 0 {
		modelOptions = fallbackModels
	}

	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Render("⚡ toskill — Autonomous Skill Builder")
	fmt.Println(banner)
	fmt.Println()

	// --- Page 1: Auth Method ---
	authOptions := []huh.Option[string]{
		huh.NewOption("Auto — SDK manages CLI (Recommended)", config.AuthAuto),
		huh.NewOption("External CLI — connect to headless server", config.AuthCLIUrl),
		huh.NewOption("GitHub Token — explicit PAT / OAuth token", config.AuthGitHubToken),
		huh.NewOption("Environment Variable — COPILOT_GITHUB_TOKEN / GH_TOKEN", config.AuthEnvVar),
		huh.NewOption("BYOK — Bring Your Own Key (OpenAI / Anthropic / Azure)", config.AuthBYOK),
	}

	// Auto-detect hints
	envHint := ""
	for _, key := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if os.Getenv(key) != "" {
			envHint = fmt.Sprintf(" (%s detected)", key)
			break
		}
	}
	authDesc := "How should toskill connect to the AI backend?" + envHint

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("🔑 Authentication").
				Description(authDesc).
				Options(authOptions...).
				Value(&cfg.AuthMethod),
		),
	).WithTheme(theme).Run()
	if err != nil {
		return nil, err
	}

	// Auth-specific follow-up
	switch cfg.AuthMethod {
	case config.AuthCLIUrl:
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("🔌 Copilot CLI address").
					Description("Format: host:port (e.g. localhost:44321)").
					Placeholder("localhost:44321").
					Value(&cfg.CopilotURL),
			),
		).WithTheme(theme).Run()
		if err != nil {
			return nil, err
		}

	case config.AuthGitHubToken:
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("🔑 GitHub Token").
					Description("Paste your token (gho_, ghu_, or github_pat_ prefix)").
					EchoMode(huh.EchoModePassword).
					Value(&cfg.GitHubCopilotToken).
					Validate(func(s string) error {
						s = strings.TrimSpace(s)
						if s == "" {
							return fmt.Errorf("token is required")
						}
						return nil
					}),
			),
		).WithTheme(theme).Run()
		if err != nil {
			return nil, err
		}

	case config.AuthEnvVar:
		// Verify an env var is actually set
		found := false
		for _, key := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
			if os.Getenv(key) != "" {
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(
					fmt.Sprintf("✅ Using %s from environment", key)))
				fmt.Println()
				found = true
				break
			}
		}
		if !found {
			fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(
				"⚠️  No token env var found. Set COPILOT_GITHUB_TOKEN, GH_TOKEN, or GITHUB_TOKEN"))
			fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
				"   Falling back to Auto mode"))
			fmt.Println()
			cfg.AuthMethod = config.AuthAuto
		}

	case config.AuthBYOK:
		providerType := "openai"
		var baseURL, apiKey string

		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("🏷️  Provider").
					Options(
						huh.NewOption("OpenAI", "openai"),
						huh.NewOption("Anthropic", "anthropic"),
						huh.NewOption("Azure AI Foundry", "azure"),
					).
					Value(&providerType),
				huh.NewInput().
					Title("🌐 Base URL").
					Description("API endpoint URL").
					Placeholder("https://api.openai.com/v1").
					Value(&baseURL).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("base URL is required")
						}
						return nil
					}),
				huh.NewInput().
					Title("🔑 API Key").
					Description("Your provider API key (optional for local providers)").
					EchoMode(huh.EchoModePassword).
					Value(&apiKey),
			),
		).WithTheme(theme).Run()
		if err != nil {
			return nil, err
		}

		cfg.BYOKProvider = &copilot.ProviderConfig{
			Type:    providerType,
			BaseURL: baseURL,
			APIKey:  apiKey,
		}
	}

	// Fetch models dynamically after auth is configured — retry loop on failure
	if cfg.AuthMethod != config.AuthBYOK {
		for {
			fmt.Fprintf(os.Stderr, "🔍 Loading available models...\n")
			fetched, ok := FetchModelsWithAuth(cfg.AuthMethod, cfg.CopilotURL, cfg.GitHubCopilotToken)
			if ok && len(fetched) > 0 {
				modelOptions = fetched
				fmt.Fprintf(os.Stderr, "   ✅ Found %d model(s)\n\n", len(modelOptions))
				break
			}

			// Connection failed — ask user what to do
			fmt.Fprintf(os.Stderr, "\n")
			var recovery string
			retryOptions := []huh.Option[string]{
				huh.NewOption("Switch to External CLI (connect to headless server)", config.AuthCLIUrl),
				huh.NewOption("Switch to GitHub Token (explicit token)", config.AuthGitHubToken),
				huh.NewOption("Switch to Environment Variable", config.AuthEnvVar),
				huh.NewOption("Retry current method", "__retry__"),
				huh.NewOption("Cancel", "__cancel__"),
			}
			err = huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("⚠️  Could not load models with current auth").
						Description("The AI backend is not reachable. Choose how to proceed:").
						Options(retryOptions...).
						Value(&recovery),
				),
			).WithTheme(theme).Run()
			if err != nil {
				return nil, err
			}

			if recovery == "__cancel__" {
				cfg.Confirmed = false
				return cfg, nil
			}
			if recovery == "__retry__" {
				continue
			}

			// User switched auth method — collect new details
			cfg.AuthMethod = recovery
			switch recovery {
			case config.AuthCLIUrl:
				cfg.CopilotURL = "localhost:44321"
				err = huh.NewForm(
					huh.NewGroup(
						huh.NewInput().
							Title("🔌 Copilot CLI address").
							Description("Format: host:port").
							Placeholder("localhost:44321").
							Value(&cfg.CopilotURL),
					),
				).WithTheme(theme).Run()
				if err != nil {
					return nil, err
				}
			case config.AuthGitHubToken:
				err = huh.NewForm(
					huh.NewGroup(
						huh.NewInput().
							Title("🔑 GitHub Token").
							Description("Paste your token (gho_, ghu_, or github_pat_ prefix)").
							EchoMode(huh.EchoModePassword).
							Value(&cfg.GitHubCopilotToken).
							Validate(func(s string) error {
								if strings.TrimSpace(s) == "" {
									return fmt.Errorf("token is required")
								}
								return nil
							}),
					),
				).WithTheme(theme).Run()
				if err != nil {
					return nil, err
				}
			case config.AuthEnvVar:
				found := false
				for _, key := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
					if os.Getenv(key) != "" {
						fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(
							fmt.Sprintf("   ✅ Using %s from environment", key)))
						found = true
						break
					}
				}
				if !found {
					fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(
						"   ⚠️  No token env var found"))
				}
			}
		}
	} else {
		// BYOK: let user type a custom model name
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			"   BYOK mode: enter your model name below"))
		fmt.Println()
	}

	// --- Page 2: URLs ---
	var urlsRaw string
	err = huh.NewForm(
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

	// --- Page 3: Storage ---
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
		// Check gh CLI auth
		auth := ghauth.Check()
		if !auth.LoggedIn {
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(
				"⚠️  GitHub CLI not authenticated. Let's fix that."))
			fmt.Println()
			auth, err = ghauth.Login()
			if err != nil || !auth.LoggedIn {
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(
					"❌ GitHub login failed. Falling back to local storage."))
				cfg.StorageMode = "local"
			}
		}

		if cfg.StorageMode == "github" {
			cfg.GitHubToken = auth.Token
			fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(
				fmt.Sprintf("✅ Logged in as %s", auth.Username)))
			fmt.Println()

			// List repos + offer create
			repos, _ := ghauth.ListRepos(20)
			repoOptions := []huh.Option[string]{
				huh.NewOption("+ Create new repository", "__create__"),
			}
			for _, r := range repos {
				repoOptions = append(repoOptions, huh.NewOption(r, r))
			}

			var repoChoice string
			err = huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("📂 Select repository").
						Description("Choose an existing repo or create a new one").
						Options(repoOptions...).
						Value(&repoChoice),
				),
			).WithTheme(theme).Run()
			if err != nil {
				return nil, err
			}

			if repoChoice == "__create__" {
				newName := auth.Username + "/toskill-store"
				err = huh.NewForm(
					huh.NewGroup(
						huh.NewInput().
							Title("New repository name").
							Placeholder(auth.Username + "/toskill-store").
							Value(&newName).
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
				cfg.GitHubRepo = newName
				cfg.CreateRepo = true
			} else {
				cfg.GitHubRepo = repoChoice
			}
		}
	}

	// --- Page 4: Model ---
	if cfg.AuthMethod == config.AuthBYOK {
		// BYOK: free-text model name
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("🧠 Model name").
					Description("Enter the model ID for your provider").
					Placeholder("gpt-4o").
					Value(&cfg.Model).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("model name is required")
						}
						return nil
					}),
			),
		).WithTheme(theme).Run()
	} else {
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("🧠 Model").
					Description("LLM for all pipeline phases").
					Options(modelOptions...).
					Value(&cfg.Model),
			),
		).WithTheme(theme).Run()
	}
	if err != nil {
		return nil, err
	}

	// --- Page 5: Per-phase models (optional) ---
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

		if cfg.AuthMethod == config.AuthBYOK {
			err = huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("🔍 Extract Model").
						Description("For browsing and content extraction").
						Value(&cfg.ExtractModel),
					huh.NewInput().
						Title("📚 Curate Model").
						Description("For knowledge base creation").
						Value(&cfg.CurateModel),
					huh.NewInput().
						Title("🛠️  Build Model").
						Description("For skill generation").
						Value(&cfg.BuildModel),
				),
			).WithTheme(theme).Run()
		} else {
			err = huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("🔍 Extract Model").
						Description("For browsing and content extraction").
						Options(modelOptions...).
						Value(&cfg.ExtractModel),
					huh.NewSelect[string]().
						Title("📚 Curate Model").
						Description("For knowledge base creation").
						Options(modelOptions...).
						Value(&cfg.CurateModel),
					huh.NewSelect[string]().
						Title("🛠️  Build Model").
						Description("For skill generation").
						Options(modelOptions...).
						Value(&cfg.BuildModel),
				),
			).WithTheme(theme).Run()
		}
		if err != nil {
			return nil, err
		}
	}

	// --- Summary & Confirm ---
	var authSummary string
	switch cfg.AuthMethod {
	case config.AuthAuto:
		authSummary = "Auto (SDK-managed)"
	case config.AuthCLIUrl:
		authSummary = fmt.Sprintf("External CLI (%s)", cfg.CopilotURL)
	case config.AuthGitHubToken:
		authSummary = "GitHub Token"
	case config.AuthEnvVar:
		authSummary = "Environment Variable"
	case config.AuthBYOK:
		authSummary = fmt.Sprintf("BYOK (%s)", cfg.BYOKProvider.Type)
	}

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
		"  Auth:     %s\n  URLs:     %d\n  Storage:  %s\n  Model:    %s",
		authSummary, len(cfg.URLs), storageSummary, modelSummary,
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
