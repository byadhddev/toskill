package tools

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

// redactEnabled controls path redaction in tool output.
var redactEnabled bool

// SetRedact enables/disables home directory path redaction in tool output.
func SetRedact(enabled bool) { redactEnabled = enabled }

// Redact replaces home dir with ~ when redaction is enabled. Exported for use by other packages.
func Redact(path string) string {
	if !redactEnabled {
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

// FindSkillTool searches the open skills ecosystem.
func FindSkillTool() copilot.Tool {
	type Params struct {
		Query string `json:"query" jsonschema:"Search query to find skills"`
	}
	return copilot.DefineTool("find_skill",
		"Search the open skills ecosystem for skills matching a query. Returns available skills with install commands.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Query == "" {
				return "", fmt.Errorf("query is required")
			}
			fmt.Fprintf(os.Stderr, "🔍 Finding skills for: %s\n", p.Query)

			cmd := exec.Command("npx", "skills", "find", p.Query)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			cmd.Env = append(os.Environ(), "NO_COLOR=1")

			err := cmd.Run()
			output := stdout.String()
			if err != nil && output == "" {
				output = stderr.String()
			}
			if strings.TrimSpace(output) == "" {
				return "No skills found for query: " + p.Query, nil
			}

			fmt.Fprintf(os.Stderr, "   Found results for '%s'\n", p.Query)
			return output, nil
		})
}

// InstallSkillTool installs a skill from the ecosystem.
func InstallSkillTool() copilot.Tool {
	type Params struct {
		Package string `json:"package" jsonschema:"The skill package to install (e.g. 'owner/repo@skill-name')"`
	}
	return copilot.DefineTool("install_skill",
		"Install a skill from the skills ecosystem. Package format: 'owner/repo@skill-name'.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Package == "" {
				return "", fmt.Errorf("package is required")
			}
			fmt.Fprintf(os.Stderr, "📦 Installing skill: %s\n", p.Package)

			cmd := exec.Command("npx", "skills", "add", p.Package, "-g", "-y")
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			cmd.Env = append(os.Environ(), "NO_COLOR=1")

			if err := cmd.Run(); err != nil {
				combined := stdout.String() + "\n" + stderr.String()
				if strings.Contains(combined, "already") || strings.Contains(combined, "exists") {
					fmt.Fprintf(os.Stderr, "   Skill already installed\n")
					return "Skill '" + p.Package + "' is already installed.", nil
				}
				return "", fmt.Errorf("install failed: %s", combined)
			}

			fmt.Fprintf(os.Stderr, "   ✅ Installed: %s\n", p.Package)
			return "Successfully installed skill: " + p.Package, nil
		})
}

// LoadSkillTool reads an installed skill's SKILL.md.
func LoadSkillTool(dataDir string) copilot.Tool {
	type Params struct {
		Name string `json:"name" jsonschema:"The skill name to load (e.g. 'agent-browser', 'skill-creator')"`
	}
	return copilot.DefineTool("load_skill",
		"Load an installed skill's instructions (SKILL.md). Returns the full content including workflows and examples.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Name == "" {
				return "", fmt.Errorf("skill name is required")
			}
			fmt.Fprintf(os.Stderr, "📖 Loading skill: %s\n", p.Name)

			home, _ := os.UserHomeDir()
			searchPaths := []string{
				filepath.Join(dataDir, p.Name, "SKILL.md"),
				filepath.Join(home, ".agents", "skills", p.Name, "SKILL.md"),
			}

			for _, path := range searchPaths {
				data, err := os.ReadFile(path)
				if err == nil {
					fmt.Fprintf(os.Stderr, "   ✅ Loaded from: %s\n", Redact(path))
					content := string(data)

					refsDir := filepath.Join(filepath.Dir(path), "references")
					if entries, err := os.ReadDir(refsDir); err == nil {
						content += "\n\n--- Additional References ---\n"
						for _, entry := range entries {
							if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
								refData, err := os.ReadFile(filepath.Join(refsDir, entry.Name()))
								if err == nil {
									content += fmt.Sprintf("\n### %s\n%s\n", entry.Name(), string(refData))
								}
							}
						}
					}
					return content, nil
				}
			}
			return "", fmt.Errorf("skill '%s' not found. Try install_skill first", p.Name)
		})
}

// RunCommandTool executes shell commands with a timeout.
func RunCommandTool(timeout time.Duration) copilot.Tool {
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	type Params struct {
		Command string `json:"command" jsonschema:"The shell command to execute"`
	}
	return copilot.DefineTool("run_command",
		"Execute a shell command and return its output.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Command == "" {
				return "", fmt.Errorf("command is required")
			}
			fmt.Fprintf(os.Stderr, "⚡ Running: %s\n", p.Command)

			cmd := exec.Command("bash", "-c", p.Command)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			cmd.Env = os.Environ()

			done := make(chan error, 1)
			go func() { done <- cmd.Run() }()

			select {
			case err := <-done:
				output := stdout.String()
				errOutput := stderr.String()
				result := ""
				if output != "" {
					result += output
				}
				if errOutput != "" {
					if result != "" {
						result += "\n--- stderr ---\n"
					}
					result += errOutput
				}
				if err != nil {
					result += fmt.Sprintf("\n[exit code: %v]", err)
				}
				if result == "" {
					result = "(no output)"
				}
				if len(result) > 15000 {
					result = result[:15000] + "\n... (truncated)"
				}
				return result, nil

			case <-time.After(timeout):
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				return "", fmt.Errorf("command timed out after %v", timeout)
			}
		})
}

// ExtractFrontmatter extracts a field from YAML frontmatter.
func ExtractFrontmatter(content, field string) string {
	if !strings.HasPrefix(content, "---") {
		return ""
	}
	endIdx := strings.Index(content[3:], "---")
	if endIdx < 0 {
		return ""
	}
	fm := content[3 : 3+endIdx]
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, field+":"))
		}
	}
	return ""
}
