package ghauth

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Status represents the current GitHub auth state.
type Status struct {
	LoggedIn bool
	Username string
	Token    string
}

// Check returns the current gh CLI auth status.
func Check() Status {
	// Try gh auth token first (fastest)
	token, err := run("gh", "auth", "token")
	if err != nil || token == "" {
		return Status{}
	}

	username, _ := run("gh", "api", "user", "--jq", ".login")
	return Status{
		LoggedIn: true,
		Username: strings.TrimSpace(username),
		Token:    strings.TrimSpace(token),
	}
}

// Login triggers browser-based GitHub OAuth via gh CLI.
// Returns the auth status after login attempt.
func Login() (Status, error) {
	fmt.Fprintf(os.Stderr, "🔐 Opening browser for GitHub login...\n")
	cmd := exec.Command("gh", "auth", "login", "--web", "-p", "https")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return Status{}, fmt.Errorf("GitHub login failed: %w", err)
	}
	return Check(), nil
}

// ListRepos returns the user's repos (up to limit).
func ListRepos(limit int) ([]string, error) {
	out, err := run("gh", "repo", "list", "--limit", fmt.Sprintf("%d", limit),
		"--json", "nameWithOwner", "--jq", ".[].nameWithOwner")
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			repos = append(repos, line)
		}
	}
	return repos, nil
}

// CreateRepo creates a new private repo and returns its full name.
func CreateRepo(name string) (string, error) {
	out, err := run("gh", "repo", "create", name, "--private", "--confirm")
	if err != nil {
		return "", fmt.Errorf("failed to create repo: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// GetToken returns the gh CLI token, or falls back to GITHUB_TOKEN env var.
func GetToken() string {
	token, _ := run("gh", "auth", "token")
	token = strings.TrimSpace(token)
	if token != "" {
		return token
	}
	return os.Getenv("GITHUB_TOKEN")
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return string(out), err
}
