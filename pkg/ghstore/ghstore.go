package ghstore

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultRepo = "toskill-store"
const defaultBranch = "main"

// GitHubStore writes artifacts to a GitHub repository via the REST API.
type GitHubStore struct {
	token  string
	owner  string
	repo   string
	branch string
	client *http.Client
}

// New creates a new GitHub store. repo can be "owner/repo" or just "repo".
func New(token, repo string) *GitHubStore {
	if repo == "" {
		repo = defaultRepo
	}
	s := &GitHubStore{
		token:  token,
		branch: defaultBranch,
		client: &http.Client{},
	}
	if strings.Contains(repo, "/") {
		parts := strings.SplitN(repo, "/", 2)
		s.owner = parts[0]
		s.repo = parts[1]
	} else {
		s.repo = repo
	}
	return s
}

// resolveOwner fetches the authenticated user's login if owner isn't set.
func (s *GitHubStore) resolveOwner() error {
	if s.owner != "" {
		return nil
	}
	req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	defer resp.Body.Close()

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return fmt.Errorf("failed to decode user: %w", err)
	}
	s.owner = user.Login
	return nil
}

// EnsureRepo creates the repo if it doesn't exist.
func (s *GitHubStore) EnsureRepo() (string, error) {
	if err := s.resolveOwner(); err != nil {
		return "", err
	}

	// Check if repo exists
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/%s", s.owner, s.repo), nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var repo struct {
			HTMLURL string `json:"html_url"`
		}
		json.NewDecoder(resp.Body).Decode(&repo)
		return repo.HTMLURL, nil
	}

	// Create repo
	body := fmt.Sprintf(`{"name":"%s","description":"Skill Builder artifact store","private":true,"auto_init":true}`, s.repo)
	req, _ = http.NewRequest("POST", "https://api.github.com/user/repos", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err = s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create repo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create repo failed (%d): %s", resp.StatusCode, string(b))
	}

	var created struct {
		HTMLURL string `json:"html_url"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	return created.HTMLURL, nil
}

// WriteFile creates or updates a file in the repo.
func (s *GitHubStore) WriteFile(path, content, message string) (string, error) {
	if err := s.resolveOwner(); err != nil {
		return "", err
	}
	if message == "" {
		message = "Update " + path
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", s.owner, s.repo, path)

	// Check if file exists (to get sha for update)
	var sha string
	req, _ := http.NewRequest("GET", url+"?ref="+s.branch, nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var existing struct {
				SHA string `json:"sha"`
			}
			json.NewDecoder(resp.Body).Decode(&existing)
			sha = existing.SHA
		}
	}

	// Create/update
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	bodyMap := map[string]string{
		"message": message,
		"content": encoded,
		"branch":  s.branch,
	}
	if sha != "" {
		bodyMap["sha"] = sha
	}
	bodyJSON, _ := json.Marshal(bodyMap)

	req, _ = http.NewRequest("PUT", url, strings.NewReader(string(bodyJSON)))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err = s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("write failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("write failed (%d): %s", resp.StatusCode, string(b))
	}

	var result struct {
		Content struct {
			HTMLURL string `json:"html_url"`
		} `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Content.HTMLURL, nil
}

// Owner returns the resolved owner.
func (s *GitHubStore) Owner() string { return s.owner }

// Repo returns the repo name.
func (s *GitHubStore) Repo() string { return s.repo }

// DeleteFile deletes a file from the repo.
func (s *GitHubStore) DeleteFile(path, message string) error {
	if err := s.resolveOwner(); err != nil {
		return err
	}
	if message == "" {
		message = "Delete " + path
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", s.owner, s.repo, path)

	// Get sha first
	req, _ := http.NewRequest("GET", url+"?ref="+s.branch, nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil // already gone
	}
	var existing struct {
		SHA string `json:"sha"`
	}
	json.NewDecoder(resp.Body).Decode(&existing)

	// Delete
	bodyJSON, _ := json.Marshal(map[string]string{
		"message": message,
		"sha":     existing.SHA,
		"branch":  s.branch,
	})
	req, _ = http.NewRequest("DELETE", url, strings.NewReader(string(bodyJSON)))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err = s.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed (%d): %s", resp.StatusCode, string(b))
	}
	return nil
}

// ListDir lists files in a directory of the repo.
func (s *GitHubStore) ListDir(path string) ([]string, error) {
	if err := s.resolveOwner(); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", s.owner, s.repo, path, s.branch)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}

	var items []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	json.NewDecoder(resp.Body).Decode(&items)

	var paths []string
	for _, item := range items {
		paths = append(paths, item.Path)
	}
	return paths, nil
}

// DeleteDir recursively deletes all files in a directory.
func (s *GitHubStore) DeleteDir(dirPath, message string) error {
	files, err := s.listAllFiles(dirPath)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := s.DeleteFile(f, message); err != nil {
			return fmt.Errorf("failed to delete %s: %w", f, err)
		}
	}
	return nil
}

// listAllFiles recursively lists all files under a path.
func (s *GitHubStore) listAllFiles(path string) ([]string, error) {
	if err := s.resolveOwner(); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", s.owner, s.repo, path, s.branch)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}

	var items []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	json.NewDecoder(resp.Body).Decode(&items)

	var files []string
	for _, item := range items {
		if item.Type == "file" {
			files = append(files, item.Path)
		} else if item.Type == "dir" {
			sub, err := s.listAllFiles(item.Path)
			if err != nil {
				return nil, err
			}
			files = append(files, sub...)
		}
	}
	return files, nil
}
