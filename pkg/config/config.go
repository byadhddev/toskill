package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds shared configuration for all agents.
type Config struct {
	CopilotURL string // Copilot CLI server address
	OutputDir  string // Base output directory (articles/, knowledge-bases/, skills/)
	Model      string // LLM model to use
	Verbose    bool   // Enable verbose output
}

// DefaultConfig returns a Config with sensible defaults.
// It loads from config file first, then env vars override.
func DefaultConfig() Config {
	cfg := Config{
		CopilotURL: "localhost:44321",
		OutputDir:  defaultOutputDir(),
		Model:      "claude-opus-4.6",
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
