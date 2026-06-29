// Package webhook handles GitHub webhook events for the Chetter service.
// It verifies webhook signatures, parses events, and submits review tasks
// to the chetter service.
package webhook

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	githubAPIBase        = "https://api.github.com"
	githubAPIVersion     = "2022-11-28"
	gitHubRequestTimeout = 30 * time.Second
)

// Client wraps the GitHub API surface we need for PR reviews.
type Client struct {
	AppID          int64
	InstallationID int64
	PrivateKey     *rsa.PrivateKey
	HTTPClient     *http.Client
	tokenCache     *tokenCache
	appLoginOnce   sync.Once
	appLogin       string
}

// NewClient creates a Client from the given configuration. The private key
// is expected to be PEM encoded (newlines preserved) and base64-wrapped.
func NewClient(appID int64, installationID int64, privateKeyPEMBase64 string) (*Client, error) {
	if appID == 0 || installationID == 0 {
		return nil, fmt.Errorf("appID and installationID are required")
	}
	if privateKeyPEMBase64 == "" {
		return nil, fmt.Errorf("private key is required")
	}
	pem, err := base64.StdEncoding.DecodeString(privateKeyPEMBase64)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("parse RSA private key: %w", err)
	}
	return &Client{
		AppID:          appID,
		InstallationID: installationID,
		PrivateKey:     key,
		HTTPClient:     &http.Client{Timeout: gitHubRequestTimeout},
		tokenCache:     newTokenCache(),
	}, nil
}

// newRequest builds an authenticated GitHub API request.
func (c *Client) newRequest(ctx context.Context, method, url string, body any) (*http.Request, error) {
	token, err := c.InstallationToken()
	if err != nil {
		return nil, fmt.Errorf("get installation token: %w", err)
	}
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// InstallationToken returns a current GitHub App installation token.
func (c *Client) InstallationToken() (string, error) {
	if c == nil {
		return "", fmt.Errorf("GitHub client is not configured")
	}
	return c.tokenCache.get(c)
}

// InstallationTokenForRepository returns an uncached installation token scoped
// to a single repository name within the configured installation.
func (c *Client) InstallationTokenForRepository(repo string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("GitHub client is not configured")
	}
	repoName, err := githubRepositoryName(repo)
	if err != nil {
		return "", err
	}
	token, _, err := fetchInstallationToken(c, map[string]any{
		"repositories": []string{repoName},
	})
	return token, err
}

// InstallationReadTokenForRepository returns an uncached installation token
// scoped to one repository and narrowed to read-only repository inspection.
func (c *Client) InstallationReadTokenForRepository(repo string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("GitHub client is not configured")
	}
	repoName, err := githubRepositoryName(repo)
	if err != nil {
		return "", err
	}
	token, _, err := fetchInstallationToken(c, repositoryReadTokenRequest(repoName))
	return token, err
}

func repositoryReadTokenRequest(repoName string) map[string]any {
	return map[string]any{
		"repositories": []string{repoName},
		"permissions": map[string]string{
			"contents":      "read",
			"issues":        "read",
			"pull_requests": "read",
		},
	}
}

func githubRepositoryName(repo string) (string, error) {
	value := strings.TrimSpace(repo)
	if value == "" {
		return "", fmt.Errorf("repo is required")
	}
	value = strings.TrimSuffix(value, ".git")
	if strings.HasPrefix(value, "git@") {
		if _, path, ok := strings.Cut(value, ":"); ok {
			return githubRepositoryName(path)
		}
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		return githubRepositoryName(parsed.Path)
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	name := strings.TrimSpace(parts[len(parts)-1])
	if name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("repo must be owner/name or repository name")
	}
	return strings.TrimSuffix(name, ".git"), nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github %s %s: %d: %s", req.Method, req.URL.Path, resp.StatusCode, string(body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ListPRFiles returns the list of filenames changed in a pull request.
func (c *Client) ListPRFiles(ctx context.Context, repo string, prNumber int) ([]string, error) {
	var all []string
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=100&page=%d", githubAPIBase, repo, prNumber, page)
		req, err := c.newRequest(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var pageFiles []struct {
			Filename string `json:"filename"`
		}
		if err := c.do(req, &pageFiles); err != nil {
			return nil, err
		}
		for _, f := range pageFiles {
			all = append(all, f.Filename)
		}
		if len(pageFiles) < 100 {
			break
		}
		page++
	}
	return all, nil
}

// AddIssueLabel adds a label to a PR (issues and PRs share the labels API).
func (c *Client) AddIssueLabel(ctx context.Context, repo string, prNumber int, label string) error {
	url := fmt.Sprintf("%s/repos/%s/issues/%d/labels", githubAPIBase, repo, prNumber)
	body := map[string][]string{"labels": {label}}
	req, err := c.newRequest(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// CreateIssueComment posts a comment on a PR.
func (c *Client) CreateIssueComment(ctx context.Context, repo string, prNumber int, body string) error {
	_, err := c.CreateIssueCommentWithResponse(ctx, repo, prNumber, body)
	return err
}

type CreatedGitHubArtifact struct {
	Number  int
	URL     string
	ID      int64
	HTMLURL string
}

type PullRequestDetails struct {
	Number  int
	State   string
	Merged  bool
	URL     string
	HeadRef string
	HeadSHA string
	BaseRef string
}

type CheckRunSummary struct {
	Total      int
	Completed  int
	Successful int
	Failed     int
	Pending    int
}

func (c *Client) GetBranchSHA(ctx context.Context, repo, branch string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/git/ref/heads/%s", githubAPIBase, repo, escapeGitHubPath(branch))
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.do(req, &resp); err != nil {
		return "", err
	}
	if resp.Object.SHA == "" {
		return "", fmt.Errorf("github branch %q has empty sha", branch)
	}
	return resp.Object.SHA, nil
}

func (c *Client) CreateBranch(ctx context.Context, repo, branch, sha string) error {
	url := fmt.Sprintf("%s/repos/%s/git/refs", githubAPIBase, repo)
	req, err := c.newRequest(ctx, http.MethodPost, url, map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": sha,
	})
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) UpsertFile(ctx context.Context, repo, branch, path, content, message string) error {
	sha, err := c.fileSHA(ctx, repo, branch, path)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	}
	if sha != "" {
		payload["sha"] = sha
	}
	url := fmt.Sprintf("%s/repos/%s/contents/%s", githubAPIBase, repo, escapeGitHubPath(path))
	req, err := c.newRequest(ctx, http.MethodPut, url, payload)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) fileSHA(ctx context.Context, repo, branch, path string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", githubAPIBase, repo, escapeGitHubPath(path), url.QueryEscape(branch))
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		SHA string `json:"sha"`
	}
	if err := c.do(req, &resp); err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", nil
		}
		return "", err
	}
	return resp.SHA, nil
}

func (c *Client) CreateIssue(ctx context.Context, repo, title, body string, labels []string) (CreatedGitHubArtifact, error) {
	url := fmt.Sprintf("%s/repos/%s/issues", githubAPIBase, repo)
	payload := map[string]any{
		"title": title,
		"body":  body,
	}
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	req, err := c.newRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return CreatedGitHubArtifact{}, err
	}
	var resp struct {
		ID      int64  `json:"id"`
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.do(req, &resp); err != nil {
		return CreatedGitHubArtifact{}, err
	}
	return CreatedGitHubArtifact{ID: resp.ID, Number: resp.Number, URL: resp.HTMLURL, HTMLURL: resp.HTMLURL}, nil
}

func (c *Client) CreateIssueCommentWithResponse(ctx context.Context, repo string, issueNumber int, body string) (CreatedGitHubArtifact, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments", githubAPIBase, repo, issueNumber)
	req, err := c.newRequest(ctx, http.MethodPost, url, map[string]string{"body": body})
	if err != nil {
		return CreatedGitHubArtifact{}, err
	}
	var resp struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.do(req, &resp); err != nil {
		return CreatedGitHubArtifact{}, err
	}
	return CreatedGitHubArtifact{ID: resp.ID, Number: issueNumber, URL: resp.HTMLURL, HTMLURL: resp.HTMLURL}, nil
}

func (c *Client) CreatePullRequest(ctx context.Context, repo, title, body, head, base string, draft bool) (CreatedGitHubArtifact, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls", githubAPIBase, repo)
	req, err := c.newRequest(ctx, http.MethodPost, url, map[string]any{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
		"draft": draft,
	})
	if err != nil {
		return CreatedGitHubArtifact{}, err
	}
	var resp struct {
		ID      int64  `json:"id"`
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := c.do(req, &resp); err != nil {
		return CreatedGitHubArtifact{}, err
	}
	return CreatedGitHubArtifact{ID: resp.ID, Number: resp.Number, URL: resp.HTMLURL, HTMLURL: resp.HTMLURL}, nil
}

func (c *Client) GetPullRequestDetails(ctx context.Context, repo string, prNumber int) (PullRequestDetails, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d", githubAPIBase, repo, prNumber)
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return PullRequestDetails{}, err
	}
	var resp struct {
		Number  int    `json:"number"`
		State   string `json:"state"`
		Merged  bool   `json:"merged"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := c.do(req, &resp); err != nil {
		return PullRequestDetails{}, err
	}
	return PullRequestDetails{
		Number:  resp.Number,
		State:   resp.State,
		Merged:  resp.Merged,
		URL:     resp.HTMLURL,
		HeadRef: resp.Head.Ref,
		HeadSHA: resp.Head.SHA,
		BaseRef: resp.Base.Ref,
	}, nil
}

func (c *Client) ListCheckRunsForRef(ctx context.Context, repo, ref string) (CheckRunSummary, error) {
	url := fmt.Sprintf("%s/repos/%s/commits/%s/check-runs", githubAPIBase, repo, url.PathEscape(ref))
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CheckRunSummary{}, err
	}
	var resp struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := c.do(req, &resp); err != nil {
		return CheckRunSummary{}, err
	}
	summary := CheckRunSummary{Total: resp.TotalCount}
	for _, run := range resp.CheckRuns {
		if run.Status == "completed" {
			summary.Completed++
			switch run.Conclusion {
			case "success", "neutral", "skipped":
				summary.Successful++
			default:
				summary.Failed++
			}
		} else {
			summary.Pending++
		}
	}
	return summary, nil
}

func (c *Client) CreatePullRequestReview(ctx context.Context, repo string, prNumber int, event, body string) (CreatedGitHubArtifact, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", githubAPIBase, repo, prNumber)
	req, err := c.newRequest(ctx, http.MethodPost, url, map[string]string{
		"event": event,
		"body":  body,
	})
	if err != nil {
		return CreatedGitHubArtifact{}, err
	}
	var resp struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.do(req, &resp); err != nil {
		return CreatedGitHubArtifact{}, err
	}
	return CreatedGitHubArtifact{ID: resp.ID, Number: prNumber, URL: resp.HTMLURL, HTMLURL: resp.HTMLURL}, nil
}

// GetPullRequest fetches a pull request and returns the head ref, base ref,
// clone URL, head SHA, and web URL.
func (c *Client) GetPullRequest(ctx context.Context, repo string, prNumber int) (headRef, baseRef, cloneURL, headSHA, prURL string, err error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d", githubAPIBase, repo, prNumber)
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", "", "", "", err
	}
	var resp struct {
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := c.do(req, &resp); err != nil {
		return "", "", "", "", "", err
	}
	return resp.Head.Ref, resp.Base.Ref, resp.Head.Repo.CloneURL, resp.Head.SHA, resp.HTMLURL, nil
}

// HasLabel reports whether the label is already on the PR.
func (c *Client) HasLabel(ctx context.Context, repo string, prNumber int, label string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d/labels", githubAPIBase, repo, prNumber)
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	var labels []struct {
		Name string `json:"name"`
	}
	if err := c.do(req, &labels); err != nil {
		return false, err
	}
	for _, l := range labels {
		if l.Name == label {
			return true, nil
		}
	}
	return false, nil
}

// CheckUserHasWriteAccess returns true if the given user has write or admin
// permission on the repo. Used to gate the /chetter-review comment trigger.
func (c *Client) CheckUserHasWriteAccess(ctx context.Context, repo, username string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/collaborators/%s/permission", githubAPIBase, repo, username)
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	var resp struct {
		Permission string `json:"permission"`
		User       struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := c.do(req, &resp); err != nil {
		// 404 means user is not a collaborator
		if strings.Contains(err.Error(), "404") {
			return false, nil
		}
		return false, err
	}
	switch resp.Permission {
	case "admin", "write", "maintain":
		return true, nil
	}
	return false, nil
}

// GetAppLogin returns the GitHub App's bot login (e.g. "chetter[bot]").
// The result is cached on first call.
func (c *Client) GetAppLogin(ctx context.Context) (string, error) {
	c.appLoginOnce.Do(func() {
		url := fmt.Sprintf("%s/app", githubAPIBase)
		req, err := c.newRequest(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		var resp struct {
			Slug string `json:"slug"`
		}
		if err := c.do(req, &resp); err != nil {
			return
		}
		c.appLogin = resp.Slug + "[bot]"
	})
	if c.appLogin == "" {
		return "", fmt.Errorf("could not determine app login")
	}
	return c.appLogin, nil
}

func escapeGitHubPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// tokenCache holds the installation token with TTL, refreshes before expiry.
type tokenCache struct {
	mu     sync.Mutex
	token  string
	expiry time.Time
}

func newTokenCache() *tokenCache {
	return &tokenCache{}
}

// get returns a valid token, refreshing if within 5 minutes of expiry.
func (c *tokenCache) get(client *Client) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.expiry) > 5*time.Minute {
		return c.token, nil
	}
	token, expiry, err := fetchInstallationToken(client, nil)
	if err != nil {
		return "", err
	}
	c.token = token
	c.expiry = expiry
	return token, nil
}

// fetchInstallationToken signs a JWT and exchanges it for an installation token.
func fetchInstallationToken(client *Client, requestBody any) (string, time.Time, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(client.AppID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(client.PrivateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPIBase, client.InstallationID)
	var bodyReader io.Reader
	if requestBody != nil {
		data, err := json.Marshal(requestBody)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("marshal installation token request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPost, url, bodyReader)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+signed)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitHubRequestTimeout)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("request installation token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", time.Time{}, fmt.Errorf("get installation token: %d: %s", resp.StatusCode, string(body))
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", time.Time{}, fmt.Errorf("decode response: %w", err)
	}
	if body.Token == "" {
		return "", time.Time{}, fmt.Errorf("empty token in response")
	}
	return body.Token, body.ExpiresAt, nil
}

// CommentReviewFailed is posted on a PR when Chetter fails to start a review.
const CommentReviewFailed = "🤖 Chetter review could not start. Please check the chetter service logs."
