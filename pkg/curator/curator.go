package curator

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

// writtenKBPath stores the result path from the write tool callback.
var writtenKBPath string

// Run curates articles into a structured knowledge base.
// Returns the path to the written KB file.
func Run(ctx context.Context, client *copilot.Client, cfg config.Config, articlePaths []string) (string, error) {
	fmt.Fprintf(os.Stderr, "⚡ Knowledge Curator\n")
	fmt.Fprintf(os.Stderr, "   Articles: %d | Model: %s\n\n", len(articlePaths), cfg.Model)

	writtenKBPath = ""
	kbDir := cfg.KnowledgeBasesDir()

	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		Model:               cfg.Model,
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		SystemMessage:       &copilot.SystemMessageConfig{Content: curatorSystemPrompt},
		Tools: []copilot.Tool{
			readArticleTool(),
			listKnowledgeBasesTool(kbDir),
			readKnowledgeBaseTool(kbDir),
			writeKnowledgeBaseTool(kbDir),
			tools.RunCommandTool(60 * time.Second),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Destroy()

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		if event.Type == "assistant.message" && event.Data.Content != nil {
			fmt.Fprintf(os.Stderr, "🧠 %s\n", *event.Data.Content)
		}
		if event.Type == "assistant.turn_start" {
			fmt.Fprintf(os.Stderr, "🔄 Turn started\n")
		}
		if event.Data.ModelMetrics != nil || event.Data.TotalPremiumRequests != nil {
			tools.EmitUsage(event)
		}
	})
	defer unsubscribe()

	var articleList strings.Builder
	for i, path := range articlePaths {
		articleList.WriteString(fmt.Sprintf("%d. %s\n", i+1, path))
	}

	prompt := fmt.Sprintf(`Curate the following %d articles into a structured knowledge base.

Article files to read:
%s
Follow this process:
1. Read ALL articles using read_article (one call per file)
2. Analyze the content to auto-detect the domain/niche
3. Call list_knowledge_bases to check if a KB for this domain already exists
4. If an existing KB is found, call read_knowledge_base to load it
5. Curate the knowledge base (create new or update existing)
6. Write the result using write_knowledge_base

Be thorough — preserve every example, code snippet, and concrete technique.`, len(articlePaths), articleList.String())

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	_, err = session.SendAndWait(timeoutCtx, copilot.MessageOptions{Prompt: prompt})
	if err != nil {
		return "", fmt.Errorf("curator session failed: %w", err)
	}

	if writtenKBPath == "" {
		return "", fmt.Errorf("agent did not write any knowledge base")
	}
	return writtenKBPath, nil
}

// --- Tools ---

func readArticleTool() copilot.Tool {
	type Params struct {
		Path string `json:"path" jsonschema:"File path to the article to read"`
	}
	return copilot.DefineTool("read_article",
		"Read an article file from disk. Returns the full text content.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Path == "" {
				return "", fmt.Errorf("path is required")
			}
			fmt.Fprintf(os.Stderr, "📄 Reading article: %s\n", p.Path)

			data, err := os.ReadFile(p.Path)
			if err != nil {
				return "", fmt.Errorf("failed to read %s: %w", p.Path, err)
			}
			content := string(data)
			if len(content) > 50000 {
				content = content[:50000] + "\n... (truncated at 50k chars)"
			}
			fmt.Fprintf(os.Stderr, "   ✅ Read %d chars\n", len(content))
			return content, nil
		})
}

func listKnowledgeBasesTool(kbDir string) copilot.Tool {
	type Params struct {
		Query string `json:"query" jsonschema:"Optional filter term to match KB names. Leave empty to list all."`
	}
	return copilot.DefineTool("list_knowledge_bases",
		"Scan for existing knowledge bases. Returns names and descriptions.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			fmt.Fprintf(os.Stderr, "🔍 Scanning for existing knowledge bases...\n")

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
				skillPath := filepath.Join(kbDir, name, "SKILL.md")
				data, err := os.ReadFile(skillPath)
				if err != nil {
					continue
				}
				desc := tools.ExtractFrontmatter(string(data), "description")
				if desc == "" {
					desc = "(no description)"
				}
				results = append(results, fmt.Sprintf("- %s: %s", name, desc))
			}

			if len(results) == 0 {
				return "No knowledge bases found.", nil
			}
			fmt.Fprintf(os.Stderr, "   Found %d knowledge base(s)\n", len(results))
			return fmt.Sprintf("Existing knowledge bases:\n%s", strings.Join(results, "\n")), nil
		})
}

func readKnowledgeBaseTool(kbDir string) copilot.Tool {
	type Params struct {
		Name string `json:"name" jsonschema:"Name of the knowledge base to read"`
	}
	return copilot.DefineTool("read_knowledge_base",
		"Read an existing knowledge base. Returns the full SKILL.md content.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Name == "" {
				return "", fmt.Errorf("name is required")
			}
			fmt.Fprintf(os.Stderr, "📖 Reading existing KB: %s\n", p.Name)

			path := filepath.Join(kbDir, p.Name, "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Sprintf("No knowledge base named '%s' found.", p.Name), nil
				}
				return "", fmt.Errorf("failed to read KB: %w", err)
			}
			fmt.Fprintf(os.Stderr, "   ✅ Loaded %d chars\n", len(data))
			return string(data), nil
		})
}

func writeKnowledgeBaseTool(kbDir string) copilot.Tool {
	type Params struct {
		Name    string `json:"name" jsonschema:"Kebab-case name for the KB (e.g. 'idor-vulnerabilities')"`
		Content string `json:"content" jsonschema:"The full markdown content of the knowledge base"`
	}
	return copilot.DefineTool("write_knowledge_base",
		"Write or update a knowledge base. Saves as <name>/SKILL.md.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			if p.Name == "" || p.Content == "" {
				return "", fmt.Errorf("name and content are required")
			}

			dir := filepath.Join(kbDir, p.Name)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("failed to create KB directory: %w", err)
			}
			outPath := filepath.Join(dir, "SKILL.md")

			fmt.Fprintf(os.Stderr, "💾 Writing KB: %s\n", outPath)
			if err := os.WriteFile(outPath, []byte(p.Content), 0644); err != nil {
				return "", fmt.Errorf("failed to write KB: %w", err)
			}

			writtenKBPath = outPath
			fmt.Fprintf(os.Stderr, "   ✅ Saved %d chars\n", len(p.Content))
			return fmt.Sprintf("Knowledge base '%s' saved to %s (%d chars)", p.Name, outPath, len(p.Content)), nil
		})
}

const curatorSystemPrompt = `You are an autonomous Knowledge Curator agent. Your job is to analyze multiple articles and curate them into a single, comprehensive, structured knowledge base.

You have these tools:
- read_article: Read an article file from disk
- list_knowledge_bases: Scan for existing knowledge bases
- read_knowledge_base: Read an existing KB to compare against
- write_knowledge_base: Write/update the curated knowledge base
- run_command: Execute shell commands

WORKFLOW:
1. READ every article using read_article — do not skip any.
2. DETECT the domain/niche automatically from the content.
3. CHECK for existing KBs: call list_knowledge_bases with a relevant search term.
4. If a matching KB exists, call read_knowledge_base to load it.
5. CURATE the knowledge base following the structure below.
6. WRITE the result using write_knowledge_base.

SELF-EVOLVING RULES (when updating an existing KB):
- NEVER remove existing content from the KB.
- Only ADD genuinely new information.
- Update the Changelog section with what was added.

CURATION RULES:
- ZERO EXAMPLE LOSS: Every code example, command, URL pattern MUST be preserved verbatim.
- DEDUPLICATE PROSE ONLY: Keep the best explanation, but ALL unique examples.
- SOURCE ATTRIBUTION: Tag sections with [Source: <filename>].

OUTPUT STRUCTURE:
` + "```" + `markdown
---
name: <kebab-case-domain-name>
description: <Comprehensive description>
---

# <Domain Title>

## Executive Summary
## Core Concepts
## Techniques & Methods
## Examples & Demonstrations
## Common Patterns
## Edge Cases & Pitfalls
## References
## Changelog
` + "```"
