package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/byadhddev/toskill/pkg/config"
	"github.com/byadhddev/toskill/pkg/tools"
)

// writtenSkillPath stores the result path from the write tool callback.
var writtenSkillPath string

// Run transforms a knowledge base into a distributable skill.
// Returns the path to the written skill.
func Run(ctx context.Context, client *copilot.Client, cfg config.Config, kbName string) (string, error) {
	fmt.Fprintf(os.Stderr, "⚡ Skill Builder\n")
	fmt.Fprintf(os.Stderr, "   KB: %s | Model: %s\n\n", kbName, cfg.ModelFor("build"))

	writtenSkillPath = ""
	kbDir := cfg.KnowledgeBasesDir()
	skillsDir := cfg.SkillsDir()

	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		Model:               cfg.ModelFor("build"),
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		SystemMessage:       &copilot.SystemMessageConfig{Content: builderSystemPrompt},
		Tools: []copilot.Tool{
			tools.FindSkillTool(),
			tools.InstallSkillTool(),
			tools.LoadSkillTool(cfg.OutputDir),
			listKnowledgeBasesTool(kbDir),
			readKnowledgeBaseTool(kbDir),
			writeSkillTool(skillsDir),
			tools.RunCommandTool(120 * time.Second),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Destroy()

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		if event.Type == "assistant.message" && event.Data.Content != nil {
			fmt.Fprintf(os.Stderr, "🛠️  %s\n", *event.Data.Content)
		}
		if event.Type == "assistant.turn_start" {
			fmt.Fprintf(os.Stderr, "🔄 Turn started\n")
		}
		if event.Data.ModelMetrics != nil || event.Data.TotalPremiumRequests != nil {
			tools.EmitUsage(event)
		}
	})
	defer unsubscribe()

	prompt := fmt.Sprintf(`Build a proper, distributable skill from the knowledge base named "%s".

Follow this exact process:
1. First, find the skill-creator skill using find_skill("skill creator")
2. Install it if needed using install_skill
3. Load its SKILL.md using load_skill("skill-creator") — this teaches you how to build proper skills
4. Read the knowledge base using read_knowledge_base("%s")
5. Transform it into a proper skill following skill-creator guidelines
6. Write the skill using write_skill

The output skill should be concise, actionable, and follow progressive disclosure.`, kbName, kbName)

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	_, err = session.SendAndWait(timeoutCtx, copilot.MessageOptions{Prompt: prompt})
	if err != nil {
		return "", fmt.Errorf("skill builder session failed: %w", err)
	}

	if writtenSkillPath == "" {
		return "", fmt.Errorf("agent did not write any skill")
	}
	return writtenSkillPath, nil
}

// RunAll discovers all KBs and builds skills for each.
func RunAll(ctx context.Context, client *copilot.Client, cfg config.Config) ([]string, error) {
	fmt.Fprintf(os.Stderr, "⚡ Skill Builder — auto-discovery mode\n\n")

	writtenSkillPath = ""
	kbDir := cfg.KnowledgeBasesDir()
	skillsDir := cfg.SkillsDir()

	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		Model:               cfg.ModelFor("build"),
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		SystemMessage:       &copilot.SystemMessageConfig{Content: builderSystemPrompt},
		Tools: []copilot.Tool{
			tools.FindSkillTool(),
			tools.InstallSkillTool(),
			tools.LoadSkillTool(cfg.OutputDir),
			listKnowledgeBasesTool(kbDir),
			readKnowledgeBaseTool(kbDir),
			writeSkillTool(skillsDir),
			tools.RunCommandTool(120 * time.Second),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Destroy()

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		if event.Type == "assistant.message" && event.Data.Content != nil {
			fmt.Fprintf(os.Stderr, "🛠️  %s\n", *event.Data.Content)
		}
		if event.Type == "assistant.turn_start" {
			fmt.Fprintf(os.Stderr, "🔄 Turn started\n")
		}
		if event.Data.ModelMetrics != nil || event.Data.TotalPremiumRequests != nil {
			tools.EmitUsage(event)
		}
	})
	defer unsubscribe()

	prompt := `Discover and build skills from all available knowledge bases.

1. Find and load the skill-creator skill
2. List all knowledge bases using list_knowledge_bases
3. For each KB, read it and transform into a skill following guidelines
4. Write each skill with write_skill`

	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	_, err = session.SendAndWait(timeoutCtx, copilot.MessageOptions{Prompt: prompt})
	if err != nil {
		return nil, fmt.Errorf("auto-build session failed: %w", err)
	}

	if writtenSkillPath != "" {
		return []string{writtenSkillPath}, nil
	}
	return nil, fmt.Errorf("agent did not write any skills")
}

// --- Tools ---

func listKnowledgeBasesTool(kbDir string) copilot.Tool {
	type Params struct {
		Query string `json:"query" jsonschema:"Optional filter term. Leave empty to list all."`
	}
	return copilot.DefineTool("list_knowledge_bases",
		"Scan for available knowledge bases to build skills from.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			fmt.Fprintf(os.Stderr, "🔍 Scanning for knowledge bases...\n")
			entries, err := os.ReadDir(kbDir)
			if err != nil {
				if os.IsNotExist(err) {
					return "No knowledge bases found.", nil
				}
				return "", fmt.Errorf("failed to read KB dir: %w", err)
			}

			var results []string
			query := strings.ToLower(p.Query)
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if query != "" && !strings.Contains(strings.ToLower(name), query) {
					continue
				}
				skillPath := filepath.Join(kbDir, name, "KB.md")
				data, err := os.ReadFile(skillPath)
				if err != nil {
					continue
				}
				desc := tools.ExtractFrontmatter(string(data), "description")
				if desc == "" {
					desc = "(no description)"
				}
				if len(desc) > 200 {
					desc = desc[:200] + "..."
				}
				results = append(results, fmt.Sprintf("- %s: %s", name, desc))
			}
			if len(results) == 0 {
				return "No knowledge bases found.", nil
			}
			fmt.Fprintf(os.Stderr, "   Found %d item(s)\n", len(results))
			return fmt.Sprintf("Available KBs:\n%s", strings.Join(results, "\n")), nil
		})
}

func readKnowledgeBaseTool(kbDir string) copilot.Tool {
	type Params struct {
		Name string `json:"name" jsonschema:"Name of the knowledge base"`
	}
	return copilot.DefineTool("read_knowledge_base",
		"Read a knowledge base. Returns the full KB.md content.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Name == "" {
				return "", fmt.Errorf("name is required")
			}
			fmt.Fprintf(os.Stderr, "📖 Reading KB: %s\n", p.Name)
			path := filepath.Join(kbDir, p.Name, "KB.md")
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Sprintf("No KB named '%s' found.", p.Name), nil
				}
				return "", fmt.Errorf("failed to read: %w", err)
			}
			fmt.Fprintf(os.Stderr, "   ✅ Read %d chars\n", len(data))
			return string(data), nil
		})
}

func writeSkillTool(skillsDir string) copilot.Tool {
	type Params struct {
		Name       string            `json:"name" jsonschema:"Kebab-case skill name"`
		SkillMD    string            `json:"skill_md" jsonschema:"The full SKILL.md content with YAML frontmatter"`
		References map[string]string `json:"references,omitempty" jsonschema:"Optional map of reference filename to content"`
	}
	return copilot.DefineTool("write_skill",
		"Write a complete skill package. Creates SKILL.md and optional references/ files.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Name == "" || p.SkillMD == "" {
				return "", fmt.Errorf("name and skill_md are required")
			}

			skillDir := filepath.Join(skillsDir, p.Name)
			if err := os.MkdirAll(skillDir, 0755); err != nil {
				return "", fmt.Errorf("failed to create skill dir: %w", err)
			}

			skillPath := filepath.Join(skillDir, "SKILL.md")
			fmt.Fprintf(os.Stderr, "💾 Writing skill: %s\n", skillPath)
			if err := os.WriteFile(skillPath, []byte(p.SkillMD), 0644); err != nil {
				return "", fmt.Errorf("failed to write SKILL.md: %w", err)
			}

			if len(p.References) > 0 {
				refsDir := filepath.Join(skillDir, "references")
				if err := os.MkdirAll(refsDir, 0755); err != nil {
					return "", fmt.Errorf("failed to create references dir: %w", err)
				}
				for name, content := range p.References {
					refPath := filepath.Join(refsDir, name)
					if err := os.WriteFile(refPath, []byte(content), 0644); err != nil {
						return "", fmt.Errorf("failed to write reference %s: %w", name, err)
					}
					fmt.Fprintf(os.Stderr, "   📎 Reference: %s\n", name)
				}
			}

			writtenSkillPath = skillPath
			fmt.Fprintf(os.Stderr, "   ✅ Skill written: %s (%d chars)\n", skillPath, len(p.SkillMD))

			result := fmt.Sprintf("Skill '%s' written to %s (%d chars)", p.Name, skillDir, len(p.SkillMD))
			if len(p.References) > 0 {
				result += fmt.Sprintf(" with %d reference file(s)", len(p.References))
			}
			return result, nil
		})
}

const builderSystemPrompt = `You are an autonomous Skill Builder agent. You transform knowledge bases into proper, distributable skills.

MANDATORY WORKFLOW:
1. FIRST call find_skill("skill creator") to discover the skill-creator skill.
2. Install it if needed with install_skill.
3. Call load_skill("skill-creator") to read its FULL instructions.
4. Read the target knowledge base with read_knowledge_base.
5. TRANSFORM the KB into a proper skill following skill-creator guidelines.
6. Write the skill with write_skill.

TRANSFORMATION RULES:
- The skill is NOT a copy of the KB. It is a focused, actionable guide.
- Keep SKILL.md under 500 lines. Move detailed content to references/.
- The frontmatter description MUST be comprehensive — it triggers skill discovery.
- Preserve ALL practical content: code, commands, detection steps.
- Structure as workflows: "When X, do Y" or "To achieve X, do 1-2-3".
- Use imperative form: "Check", "Validate", "Run".

RULES:
- You MUST start with find_skill to discover skill-creator — do not skip.
- You MUST load the skill-creator instructions — do not guess the format.
- Always call write_skill at the end.`
