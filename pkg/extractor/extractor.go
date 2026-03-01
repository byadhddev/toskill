package extractor

import (
	"context"
	"fmt"
	"os"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/byadhddev/toskill/pkg/config"
	"github.com/byadhddev/toskill/pkg/tools"
)

// extractedContent stores the result from the save_result tool callback.
var extractedContent string

// Run extracts content from URLs using browser automation.
// Returns the extracted content for each URL (map of url → content).
func Run(ctx context.Context, client *copilot.Client, cfg config.Config, urls []string) (map[string]string, error) {
	fmt.Fprintf(os.Stderr, "⚡ Content Extractor\n")
	fmt.Fprintf(os.Stderr, "   URLs: %d | Model: %s\n\n", len(urls), cfg.ModelFor("extract"))

	results := make(map[string]string)

	for i, url := range urls {
		fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Fprintf(os.Stderr, "📄 [%d/%d] %s\n", i+1, len(urls), url)
		fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

		content, err := browseURL(ctx, client, cfg, url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			continue
		}

		results[url] = content
		fmt.Fprintf(os.Stderr, "✅ Extracted %d chars\n\n", len(content))
	}

	return results, nil
}

func browseURL(ctx context.Context, client *copilot.Client, cfg config.Config, url string) (string, error) {
	extractedContent = ""

	dataDir := cfg.OutputDir
	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		Model:               cfg.ModelFor("extract"),
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		SystemMessage:       &copilot.SystemMessageConfig{Content: systemPrompt},
		Tools: []copilot.Tool{
			tools.FindSkillTool(),
			tools.InstallSkillTool(),
			tools.LoadSkillTool(dataDir),
			tools.RunCommandTool(120 * time.Second),
			saveResultTool(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Destroy()

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		if event.Type == "assistant.message" && event.Data.Content != nil {
			fmt.Fprintf(os.Stderr, "🤖 %s\n", *event.Data.Content)
		}
		if event.Type == "assistant.turn_start" {
			fmt.Fprintf(os.Stderr, "🔄 Turn started\n")
		}
		if event.Data.ModelMetrics != nil || event.Data.TotalPremiumRequests != nil {
			tools.EmitUsage(event)
		}
	})
	defer unsubscribe()

	prompt := fmt.Sprintf(`Browse this URL and extract its content: %s

Follow this process:
1. First, find a browser automation skill using find_skill
2. Install it if needed using install_skill
3. Load its instructions using load_skill to learn the exact commands
4. Use run_command to execute the browser commands following the loaded skill's workflow
5. Extract the page title, a summary, and the main text content
6. Save the result using save_result

Be autonomous — decide each next step based on the output of the previous step.`, url)

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	_, err = session.SendAndWait(timeoutCtx, copilot.MessageOptions{Prompt: prompt})
	if err != nil {
		return "", fmt.Errorf("agent session failed: %w", err)
	}

	if extractedContent == "" {
		return "", fmt.Errorf("agent did not save any extracted content")
	}
	return extractedContent, nil
}

func saveResultTool() copilot.Tool {
	type Params struct {
		Title   string `json:"title" jsonschema:"Title of the extracted content"`
		Summary string `json:"summary" jsonschema:"Brief summary of what the page contains"`
		Content string `json:"content" jsonschema:"The main extracted text content from the page"`
		URL     string `json:"url" jsonschema:"The source URL"`
	}
	return copilot.DefineTool("save_result",
		"Save the extracted content from a browsed web page. Call this when you have finished extracting content from a URL.",
		func(p Params, inv copilot.ToolInvocation) (string, error) {
			fmt.Fprintf(os.Stderr, "💾 Saving result: %s\n", p.Title)
			extractedContent = fmt.Sprintf("# %s\n\nURL: %s\n\n## Summary\n%s\n\n## Content\n%s",
				p.Title, p.URL, p.Summary, p.Content)
			return fmt.Sprintf("Result saved: '%s' (%d chars)", p.Title, len(p.Content)), nil
		})
}

const systemPrompt = `You are an autonomous AI agent. Your job is to browse web URLs and extract their content.

You have these tools available:
- find_skill: Search the skills ecosystem for capabilities you need
- install_skill: Install a skill you found
- load_skill: Read an installed skill's full instructions (SKILL.md)
- run_command: Execute shell commands
- save_result: Save extracted content when done

MANDATORY WORKFLOW — follow these steps in order:
1. FIRST call find_skill("browser automation") to discover what browser skills are available.
2. From the results, pick the best match and call install_skill to install it.
3. Call load_skill with the skill name to read its full SKILL.md instructions.
4. READ the SKILL.md carefully — it contains the exact CLI commands and workflow to follow.
5. Use run_command to execute those CLI commands step by step, following the skill's Core Workflow.
6. After each command output, decide the next step autonomously.
7. When you have the page content, call save_result with structured data (title, summary, content, url).
8. Always close the browser session when done.

RULES:
- You MUST start with find_skill — do not skip the discovery phase.
- You MUST load_skill to read the instructions — do not guess commands.
- The SKILL.md tells you the exact command syntax. Follow it precisely.
- After opening a page, wait for it to load, then extract text content.
- Be thorough — get the full main body text, not just a snippet.
- Always call save_result at the end.
- Always close the browser when finished.`
