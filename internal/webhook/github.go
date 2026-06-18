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
	token, err := c.tokenCache.get(c)
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
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments", githubAPIBase, repo, prNumber)
	req, err := c.newRequest(ctx, http.MethodPost, url, map[string]string{"body": body})
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// GetPullRequest fetches a pull request and returns the head ref, base ref,
// and clone URL of the head repository.
func (c *Client) GetPullRequest(ctx context.Context, repo string, prNumber int) (headRef, baseRef, cloneURL string, err error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d", githubAPIBase, repo, prNumber)
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
	token, expiry, err := fetchInstallationToken(client)
	if err != nil {
		return "", err
	}
	c.token = token
	c.expiry = expiry
	return token, nil
}

// fetchInstallationToken signs a JWT and exchanges it for an installation token.
func fetchInstallationToken(client *Client) (string, time.Time, error) {
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
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+signed)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)

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
