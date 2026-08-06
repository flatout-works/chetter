// Package webhook handles GitHub webhook events for the Chetter service.
// It verifies webhook signatures, parses events, and submits review tasks
// to the chetter service.
package webhook

import (
	"bytes"
	"container/list"
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

	"github.com/flatout-works/chetter/internal/githubrepo"
	"github.com/golang-jwt/jwt/v5"
)

const (
	githubAPIBase             = "https://api.github.com"
	githubAPIVersion          = "2022-11-28"
	gitHubRequestTimeout      = 30 * time.Second
	defaultTokenCacheMax      = 256
	defaultCredentialCacheMax = 1024
	defaultRepoCacheMax       = 1024
	defaultRepoCacheTTL       = 10 * time.Minute
)

// Manager owns GitHub App credentials and installation-specific caches.
type Manager struct {
	appID      int64
	privateKey *rsa.PrivateKey
	httpClient *http.Client
	apiBase    string

	legacyInstallationID int64

	mu            sync.Mutex
	tokens        map[int64]*tokenCacheItem
	tokenLRU      *list.List
	credentials   map[credentialCacheKey]*credentialCacheItem
	credentialLRU *list.List
	repositories  map[string]*repoCacheItem
	repoLRU       *list.List

	appLoginMu sync.Mutex
	appLogin   string
}

// PermissionProfile identifies the least-privilege permission set requested
// for a repository-restricted installation credential.
type PermissionProfile string

const (
	PermissionProfileTaskGit PermissionProfile = "task-git"
)

// Credential is a short-lived GitHub installation credential. Callers must
// not persist or log Token.
type Credential struct {
	Token     string
	ExpiresAt time.Time
}

type credentialCacheKey struct {
	installationID int64
	repo           string
	profile        PermissionProfile
}

type credentialCacheItem struct {
	key     credentialCacheKey
	mu      sync.Mutex
	token   string
	expiry  time.Time
	element *list.Element
}

type tokenCacheItem struct {
	installationID int64
	cache          tokenCache
	element        *list.Element
}

type repoCacheItem struct {
	repo           string
	mu             sync.Mutex
	installationID int64
	expiresAt      time.Time
	element        *list.Element
}

// ManagerOption customizes a GitHub App manager.
type ManagerOption func(*Manager) error

// WithAPIBaseURL overrides the GitHub API base URL. It is intended for tests
// and GitHub Enterprise-compatible API endpoints.
func WithAPIBaseURL(baseURL string) ManagerOption {
	return func(m *Manager) error {
		parsed, err := url.Parse(strings.TrimSpace(baseURL))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid GitHub API base URL %q", baseURL)
		}
		m.apiBase = strings.TrimRight(parsed.String(), "/")
		return nil
	}
}

// WithHTTPClient overrides the HTTP client used by the manager.
func WithHTTPClient(client *http.Client) ManagerOption {
	return func(m *Manager) error {
		if client == nil {
			return fmt.Errorf("GitHub HTTP client is required")
		}
		m.httpClient = client
		return nil
	}
}

// WithLegacyInstallationID configures the optional installation used by
// repository-less legacy callers until they are migrated to ClientForRepo.
func WithLegacyInstallationID(installationID int64) ManagerOption {
	return func(m *Manager) error {
		if installationID < 0 {
			return fmt.Errorf("legacy installation ID must be positive")
		}
		m.legacyInstallationID = installationID
		return nil
	}
}

// NewManager parses GitHub App credentials and creates a process-wide manager.
func NewManager(appID int64, privateKeyPEMBase64 string, opts ...ManagerOption) (*Manager, error) {
	if appID <= 0 {
		return nil, fmt.Errorf("appID is required")
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
	m := &Manager{
		appID:         appID,
		privateKey:    key,
		httpClient:    &http.Client{Timeout: gitHubRequestTimeout},
		apiBase:       githubAPIBase,
		tokens:        make(map[int64]*tokenCacheItem),
		tokenLRU:      list.New(),
		credentials:   make(map[credentialCacheKey]*credentialCacheItem),
		credentialLRU: list.New(),
		repositories:  make(map[string]*repoCacheItem),
		repoLRU:       list.New(),
	}
	for _, opt := range opts {
		if err := opt(m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// CredentialForRepo discovers the repository installation and returns a
// repository-restricted credential for profile.
func (m *Manager) CredentialForRepo(ctx context.Context, repo string, profile PermissionProfile) (Credential, error) {
	client, err := m.ClientForRepo(ctx, repo)
	if err != nil {
		return Credential{}, err
	}
	return client.CredentialForRepo(ctx, repo, profile)
}

// CredentialForRepo returns a repository-restricted credential from this
// client's immutable installation.
func (c *Client) CredentialForRepo(ctx context.Context, repo string, profile PermissionProfile) (Credential, error) {
	parsed, err := githubrepo.Parse(repo)
	if err != nil {
		return Credential{}, fmt.Errorf("issue GitHub credential: %w", err)
	}
	if profile != PermissionProfileTaskGit {
		return Credential{}, fmt.Errorf("unsupported GitHub credential permission profile %q", profile)
	}
	key := credentialCacheKey{installationID: c.InstallationID, repo: parsed.Normalized(), profile: profile}

	c.manager.mu.Lock()
	item := c.manager.credentials[key]
	if item == nil {
		item = &credentialCacheItem{key: key}
		item.element = c.manager.credentialLRU.PushFront(item)
		c.manager.credentials[key] = item
		if c.manager.credentialLRU.Len() > defaultCredentialCacheMax {
			oldest := c.manager.credentialLRU.Back()
			oldItem := oldest.Value.(*credentialCacheItem)
			delete(c.manager.credentials, oldItem.key)
			c.manager.credentialLRU.Remove(oldest)
		}
	} else {
		c.manager.credentialLRU.MoveToFront(item.element)
	}
	c.manager.mu.Unlock()

	item.mu.Lock()
	defer item.mu.Unlock()
	if item.token != "" && time.Until(item.expiry) > 5*time.Minute {
		return Credential{Token: item.token, ExpiresAt: item.expiry}, nil
	}
	credential, err := c.manager.fetchRepositoryCredential(ctx, c.InstallationID, parsed, profile)
	if err != nil {
		return Credential{}, err
	}
	item.token = credential.Token
	item.expiry = credential.ExpiresAt
	return credential, nil
}

// Client wraps the GitHub API for one immutable installation.
type Client struct {
	InstallationID int64
	manager        *Manager
	token          *tokenCache
}

// NewClient is a compatibility wrapper for legacy single-installation callers.
func NewClient(appID int64, installationID int64, privateKeyPEMBase64 string) (*Client, error) {
	m, err := NewManager(appID, privateKeyPEMBase64, WithLegacyInstallationID(installationID))
	if err != nil {
		return nil, err
	}
	return m.ClientForInstallation(context.Background(), installationID)
}

// ClientForInstallation returns an immutable client whose token cache is
// isolated from every other installation.
func (m *Manager) ClientForInstallation(ctx context.Context, installationID int64) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if installationID <= 0 {
		return nil, fmt.Errorf("GitHub installation ID is required")
	}

	m.mu.Lock()
	item := m.tokens[installationID]
	if item == nil {
		item = &tokenCacheItem{installationID: installationID}
		item.element = m.tokenLRU.PushFront(item)
		m.tokens[installationID] = item
		if m.tokenLRU.Len() > defaultTokenCacheMax {
			oldest := m.tokenLRU.Back()
			oldItem := oldest.Value.(*tokenCacheItem)
			delete(m.tokens, oldItem.installationID)
			m.tokenLRU.Remove(oldest)
		}
	} else {
		m.tokenLRU.MoveToFront(item.element)
	}
	m.mu.Unlock()

	return &Client{InstallationID: installationID, manager: m, token: &item.cache}, nil
}

// LegacyClient returns the optional fallback installation client.
func (m *Manager) LegacyClient() *Client {
	if m == nil || m.legacyInstallationID <= 0 {
		return nil
	}
	client, _ := m.ClientForInstallation(context.Background(), m.legacyInstallationID)
	return client
}

// ClientForRepo discovers the App installation authorized for repo using an
// App JWT and caches the mapping for a bounded TTL.
func (m *Manager) ClientForRepo(ctx context.Context, repo string) (*Client, error) {
	parsed, err := githubrepo.Parse(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub repository installation: %w", err)
	}
	cacheKey := parsed.Normalized()

	m.mu.Lock()
	item := m.repositories[cacheKey]
	if item == nil {
		item = &repoCacheItem{repo: cacheKey}
		item.element = m.repoLRU.PushFront(item)
		m.repositories[cacheKey] = item
		if m.repoLRU.Len() > defaultRepoCacheMax {
			oldest := m.repoLRU.Back()
			oldItem := oldest.Value.(*repoCacheItem)
			delete(m.repositories, oldItem.repo)
			m.repoLRU.Remove(oldest)
		}
	} else {
		m.repoLRU.MoveToFront(item.element)
	}
	m.mu.Unlock()

	item.mu.Lock()
	defer item.mu.Unlock()
	if item.installationID > 0 && time.Now().Before(item.expiresAt) {
		return m.ClientForInstallation(ctx, item.installationID)
	}
	installationID, err := m.fetchRepoInstallation(ctx, parsed.FullName())
	if err != nil {
		return nil, err
	}
	item.installationID = installationID
	item.expiresAt = time.Now().Add(defaultRepoCacheTTL)
	return m.ClientForInstallation(ctx, installationID)
}

func (m *Manager) fetchRepoInstallation(ctx context.Context, repo string) (int64, error) {
	signed, err := m.appJWT()
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiURL("/repos/"+repo+"/installation"), nil)
	if err != nil {
		return 0, err
	}
	setGitHubHeaders(req, signed)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("get repository installation: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("get repository installation for %s: %d: %s", repo, resp.StatusCode, string(body))
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decode repository installation: %w", err)
	}
	if body.ID <= 0 {
		return 0, fmt.Errorf("repository installation for %s has invalid ID", repo)
	}
	return body.ID, nil
}

// newRequest builds an authenticated GitHub API request.
func (c *Client) newRequest(ctx context.Context, method, url string, body any) (*http.Request, error) {
	token, err := c.token.get(ctx, c.manager, c.InstallationID)
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

func (c *Client) do(req *http.Request, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.manager.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("github request: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			c.token.invalidate()
			token, err := c.token.get(req.Context(), c.manager, c.InstallationID)
			if err != nil {
				return fmt.Errorf("refresh installation token: %w", err)
			}
			retry := req.Clone(req.Context())
			if req.GetBody != nil {
				retry.Body, err = req.GetBody()
				if err != nil {
					return fmt.Errorf("recreate GitHub request body: %w", err)
				}
			}
			retry.Header.Set("Authorization", "Bearer "+token)
			req = retry
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return &githubAPIError{method: req.Method, path: req.URL.Path, status: resp.StatusCode, body: string(body)}
		}
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return fmt.Errorf("github request failed after token refresh")
}

type githubAPIError struct {
	method string
	path   string
	status int
	body   string
}

func (e *githubAPIError) Error() string {
	return fmt.Sprintf("github %s %s: %d: %s", e.method, e.path, e.status, e.body)
}

func (m *Manager) apiURL(path string) string {
	return m.apiBase + "/" + strings.TrimLeft(path, "/")
}

func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
}

// ListPRFiles returns the list of filenames changed in a pull request.
func (c *Client) ListPRFiles(ctx context.Context, repo string, prNumber int) ([]string, error) {
	var all []string
	page := 1
	for {
		url := c.manager.apiURL(fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=100&page=%d", repo, prNumber, page))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/issues/%d/labels", repo, prNumber))
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

// IssueDetails is the authoritative issue metadata used by the manual trigger
// test flow. It is fetched from GitHub so label matching and the default
// prompt never trust editable client-supplied fields.
type IssueDetails struct {
	Number  int
	State   string
	Title   string
	Body    string
	HTMLURL string
	Labels  []string
}

// GetIssueDetails fetches the authoritative metadata for a GitHub issue.
func (c *Client) GetIssueDetails(ctx context.Context, repo string, issueNumber int) (IssueDetails, error) {
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/issues/%d", repo, issueNumber))
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return IssueDetails{}, err
	}
	var resp struct {
		Number  int    `json:"number"`
		State   string `json:"state"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := c.do(req, &resp); err != nil {
		return IssueDetails{}, err
	}
	details := IssueDetails{
		Number:  resp.Number,
		State:   resp.State,
		Title:   resp.Title,
		Body:    resp.Body,
		HTMLURL: resp.HTMLURL,
	}
	for _, lbl := range resp.Labels {
		details.Labels = append(details.Labels, lbl.Name)
	}
	return details, nil
}

func (c *Client) GetBranchSHA(ctx context.Context, repo, branch string) (string, error) {
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/git/ref/heads/%s", repo, escapeGitHubPath(branch)))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/git/refs", repo))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/contents/%s", repo, escapeGitHubPath(path)))
	req, err := c.newRequest(ctx, http.MethodPut, url, payload)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) fileSHA(ctx context.Context, repo, branch, path string) (string, error) {
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/contents/%s?ref=%s", repo, escapeGitHubPath(path), url.QueryEscape(branch)))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/issues", repo))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/issues/%d/comments", repo, issueNumber))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/pulls", repo))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/pulls/%d", repo, prNumber))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/commits/%s/check-runs", repo, url.PathEscape(ref)))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, prNumber))
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
// and clone URL of the head repository.
func (c *Client) GetPullRequest(ctx context.Context, repo string, prNumber int) (headRef, baseRef, cloneURL string, err error) {
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/pulls/%d", repo, prNumber))
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", "", err
	}
	var resp struct {
		Head struct {
			Ref  string `json:"ref"`
			Repo struct {
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := c.do(req, &resp); err != nil {
		return "", "", "", err
	}
	return resp.Head.Ref, resp.Base.Ref, resp.Head.Repo.CloneURL, nil
}

// HasLabel reports whether the label is already on the PR.
func (c *Client) HasLabel(ctx context.Context, repo string, prNumber int, label string) (bool, error) {
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/issues/%d/labels", repo, prNumber))
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
	url := c.manager.apiURL(fmt.Sprintf("/repos/%s/collaborators/%s/permission", repo, username))
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

// AppLogin returns the App bot login (for example, "chetter[bot]").
func (m *Manager) AppLogin(ctx context.Context) (string, error) {
	m.appLoginMu.Lock()
	defer m.appLoginMu.Unlock()
	if m.appLogin != "" {
		return m.appLogin, nil
	}

	appToken, err := m.appJWT()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiURL("/app"), nil)
	if err != nil {
		return "", err
	}
	setGitHubHeaders(req, appToken)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get app: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("get app: %d: %s", resp.StatusCode, string(body))
	}
	var body struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode app: %w", err)
	}
	if body.Slug == "" {
		return "", fmt.Errorf("get app: empty slug")
	}

	m.appLogin = body.Slug + "[bot]"
	return m.appLogin, nil
}

// GetAppLogin is retained for compatibility; App login is manager-scoped.
func (c *Client) GetAppLogin(ctx context.Context) (string, error) {
	return c.manager.AppLogin(ctx)
}

func escapeGitHubPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// tokenCache holds one installation token and refreshes before expiry.
type tokenCache struct {
	mu     sync.Mutex
	token  string
	expiry time.Time
}

// get returns a valid token, refreshing if within 5 minutes of expiry.
func (c *tokenCache) get(ctx context.Context, manager *Manager, installationID int64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.expiry) > 5*time.Minute {
		return c.token, nil
	}
	token, expiry, err := manager.fetchInstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}
	c.token = token
	c.expiry = expiry
	return token, nil
}

func (c *tokenCache) invalidate() {
	c.mu.Lock()
	c.token = ""
	c.expiry = time.Time{}
	c.mu.Unlock()
}

// fetchInstallationToken signs a JWT and exchanges it for an installation token.
func (m *Manager) fetchInstallationToken(ctx context.Context, installationID int64) (string, time.Time, error) {
	signed, err := m.appJWT()
	if err != nil {
		return "", time.Time{}, err
	}

	url := m.apiURL(fmt.Sprintf("/app/installations/%d/access_tokens", installationID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	setGitHubHeaders(req, signed)

	resp, err := m.httpClient.Do(req)
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

func (m *Manager) fetchRepositoryCredential(ctx context.Context, installationID int64, repo githubrepo.Repository, profile PermissionProfile) (Credential, error) {
	permissions := map[string]string{}
	switch profile {
	case PermissionProfileTaskGit:
		permissions["contents"] = "write"
		permissions["issues"] = "read"
		permissions["pull_requests"] = "read"
	default:
		return Credential{}, fmt.Errorf("unsupported GitHub credential permission profile %q", profile)
	}
	payload, err := json.Marshal(map[string]any{
		"repositories": []string{repo.Name},
		"permissions":  permissions,
	})
	if err != nil {
		return Credential{}, fmt.Errorf("marshal repository credential request: %w", err)
	}
	signed, err := m.appJWT()
	if err != nil {
		return Credential{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.apiURL(fmt.Sprintf("/app/installations/%d/access_tokens", installationID)), bytes.NewReader(payload))
	if err != nil {
		return Credential{}, err
	}
	setGitHubHeaders(req, signed)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return Credential{}, fmt.Errorf("request repository credential: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Credential{}, fmt.Errorf("get repository credential for %s with profile %s: %d: %s", repo.FullName(), profile, resp.StatusCode, string(body))
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Credential{}, fmt.Errorf("decode repository credential response: %w", err)
	}
	if body.Token == "" || body.ExpiresAt.IsZero() {
		return Credential{}, fmt.Errorf("repository credential response is incomplete")
	}
	return Credential{Token: body.Token, ExpiresAt: body.ExpiresAt}, nil
}

func (m *Manager) appJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(m.appID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(m.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}

// CommentReviewFailed is posted on a PR when Chetter fails to start a review.
const CommentReviewFailed = "🤖 Chetter review could not start. Please check the chetter service logs."
