package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
)

// AuthMethod identifies how the SDK connects and authenticates.
const (
	AuthAuto        = "auto"         // SDK auto-manages CLI process, uses stored credentials
	AuthCLIUrl      = "cli-url"      // Connect to external headless CLI server
	AuthGitHubToken = "github-token" // Explicit GitHub token, SDK starts CLI
	AuthEnvVar      = "env-var"      // Use environment variables (COPILOT_GITHUB_TOKEN etc.)
	AuthBYOK        = "byok"         // Bring your own API key (no Copilot subscription)
)

// Config holds shared configuration for all agents.
type Config struct {
	CopilotURL    string // Copilot CLI server address (for AuthCLIUrl)
	OutputDir     string // Base output directory (articles/, knowledge-bases/, skills/)
	Model         string // LLM model to use (default for all phases)
	ExtractModel  string // Model override for extraction phase
	CurateModel   string // Model override for curation phase
	BuildModel    string // Model override for skill building phase
	Verbose       bool   // Enable verbose output

	// Auth
	AuthMethod        string                // One of Auth* constants (default: AuthAuto)
	GitHubCopilotToken string               // For AuthGitHubToken method
	BYOKProvider      *copilot.ProviderConfig // For AuthBYOK method

	// Skill mode
	SkillMode    string // "new" or "evolve" (default: "new")
	EvolveSkill  string // Name of existing skill to evolve (for SkillMode "evolve")

	// Display
	RedactPaths bool // Replace $HOME with ~ in all output paths
}

// ModelFor returns the model to use for a given phase.
// Falls back to the default Model if no per-phase override is set.
func (c Config) ModelFor(phase string) string {
	switch phase {
	case "extract":
		if c.ExtractModel != "" {
			return c.ExtractModel
		}
	case "curate":
		if c.CurateModel != "" {
			return c.CurateModel
		}
	case "build":
		if c.BuildModel != "" {
			return c.BuildModel
		}
	}
	return c.Model
}

// ApplyBYOK sets the BYOK provider on a SessionConfig if configured.
func (c Config) ApplyBYOK(sc *copilot.SessionConfig) {
	if c.BYOKProvider != nil {
		sc.Provider = c.BYOKProvider
	}
}

// DefaultConfig returns a Config with sensible defaults.
// It loads from config file first, then env vars override.
func DefaultConfig() Config {
	cfg := Config{
		CopilotURL: "localhost:44321",
		OutputDir:  defaultOutputDir(),
		Model:      "claude-opus-4.6",
		AuthMethod: AuthAuto,
		SkillMode:  "new",
	}

	// Load from config file
	if fileCfg, err := LoadConfigFile(); err == nil {
		if fileCfg["copilot-url"] != "" {
			cfg.CopilotURL = fileCfg["copilot-url"]
		}
		if fileCfg["output"] != "" {
			cfg.OutputDir = fileCfg["output"]
		}
		if fileCfg["model"] != "" {
			cfg.Model = fileCfg["model"]
		}
		if fileCfg["extract-model"] != "" {
			cfg.ExtractModel = fileCfg["extract-model"]
		}
		if fileCfg["curate-model"] != "" {
			cfg.CurateModel = fileCfg["curate-model"]
		}
		if fileCfg["build-model"] != "" {
			cfg.BuildModel = fileCfg["build-model"]
		}
		if fileCfg["auth-method"] != "" {
			cfg.AuthMethod = fileCfg["auth-method"]
		}
		if fileCfg["redact-paths"] == "true" {
			cfg.RedactPaths = true
		}
	}

	// Env vars override config file
	if v := os.Getenv("COPILOT_CLI_URL"); v != "" {
		cfg.CopilotURL = v
	}
	if v := os.Getenv("TOSKILL_OUTPUT"); v != "" {
		cfg.OutputDir = v
	}
	if v := os.Getenv("TOSKILL_MODEL"); v != "" {
		cfg.Model = v
	}

	return cfg
}

// ArticlesDir returns the articles subdirectory.
func (c Config) ArticlesDir() string {
	return filepath.Join(c.OutputDir, "articles")
}

// KnowledgeBasesDir returns the knowledge-bases subdirectory.
func (c Config) KnowledgeBasesDir() string {
	return filepath.Join(c.OutputDir, "knowledge-bases")
}

// SkillsDir returns the skills subdirectory.
func (c Config) SkillsDir() string {
	return filepath.Join(c.OutputDir, "skills")
}

// EnsureDirs creates all output subdirectories.
func (c Config) EnsureDirs() error {
	for _, dir := range []string{c.ArticlesDir(), c.KnowledgeBasesDir(), c.SkillsDir()} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

// Redact replaces the user's home directory with ~ when RedactPaths is enabled.
func (c Config) Redact(path string) string {
	if !c.RedactPaths {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// --- Config File ---

// ConfigDir returns ~/.config/toskill/
func ConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "toskill")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "toskill")
}

// ConfigFilePath returns the config file path.
func ConfigFilePath() string {
	return filepath.Join(ConfigDir(), "config")
}

// LoadConfigFile loads key=value pairs from the config file.
func LoadConfigFile() (map[string]string, error) {
	data, err := os.ReadFile(ConfigFilePath())
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result, nil
}

// SaveConfigValue sets a key=value in the config file.
func SaveConfigValue(key, value string) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	existing, _ := LoadConfigFile()
	if existing == nil {
		existing = make(map[string]string)
	}
	existing[key] = value

	var lines []string
	for k, v := range existing {
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}
	return os.WriteFile(ConfigFilePath(), []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func defaultOutputDir() string {
	if v := os.Getenv("SHARED_DATA_DIR"); v != "" {
		return v
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, "skill-store")
}
