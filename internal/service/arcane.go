package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ArcaneClient wraps calls to the Arcane server API for vulnerability scans.
type ArcaneClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewArcaneClient creates an Arcane API client.
func NewArcaneClient(baseURL, apiKey string) *ArcaneClient {
	if baseURL == "" {
		baseURL = "https://arcane.devfleet.works"
	}
	return &ArcaneClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// IsConfigured reports whether the client has a valid base URL.
func (c *ArcaneClient) IsConfigured() bool {
	return c.baseURL != "" && c.apiKey != ""
}

// ScannerStatus represents the Trivy scanner availability.
type ScannerStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
}

// SeveritySummary counts vulnerabilities by severity.
type SeveritySummary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
	Total    int `json:"total"`
}

// ScanSummary is the per-image vulnerability summary.
type ScanSummary struct {
	ImageID   string          `json:"imageId"`
	ScanTime  time.Time       `json:"scanTime"`
	Status    string          `json:"status"`
	Summary   SeveritySummary `json:"summary"`
}

// EnvironmentVulnerabilitySummary aggregates vulnerability counts across all images.
type EnvironmentVulnerabilitySummary struct {
	TotalImages    int             `json:"totalImages"`
	ScannedImages  int             `json:"scannedImages"`
	Summary        SeveritySummary `json:"summary"`
}

// Vulnerability is a single CVE entry.
type Vulnerability struct {
	VulnerabilityID  string     `json:"vulnerabilityId"`
	PkgName          string     `json:"pkgName"`
	InstalledVersion string     `json:"installedVersion"`
	FixedVersion     string     `json:"fixedVersion,omitempty"`
	Severity         string     `json:"severity"`
	Title            string     `json:"title,omitempty"`
	Description      string     `json:"description,omitempty"`
	References       []string   `json:"references,omitempty"`
	CVSS             *CVSSInfo  `json:"cvss,omitempty"`
	PublishedDate    *time.Time `json:"publishedDate,omitempty"`
	LastModifiedDate *time.Time `json:"lastModifiedDate,omitempty"`
}

// CVSSInfo holds CVSS scores.
type CVSSInfo struct {
	V2Score float64 `json:"v2Score,omitempty"`
	V3Score float64 `json:"v3Score,omitempty"`
	V2Vector string `json:"v2Vector,omitempty"`
	V3Vector string `json:"v3Vector,omitempty"`
}

// VulnerabilityWithImage includes the source image context.
type VulnerabilityWithImage struct {
	Vulnerability
	ImageID   string `json:"imageId"`
	ImageName string `json:"imageName"`
}

// apiResponse is a generic Arcane API response wrapper.
type apiResponse[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

func (c *ArcaneClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("arcane API %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *ArcaneClient) post(ctx context.Context, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("arcane API %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// GetScannerStatus checks if Trivy is available.
func (c *ArcaneClient) GetScannerStatus(ctx context.Context, envID string) (*ScannerStatus, error) {
	body, err := c.get(ctx, "/api/environments/"+envID+"/vulnerabilities/scanner-status")
	if err != nil {
		return nil, err
	}
	var resp apiResponse[ScannerStatus]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse scanner status: %w", err)
	}
	return &resp.Data, nil
}

// GetEnvironmentSummary returns aggregated vulnerability counts.
func (c *ArcaneClient) GetEnvironmentSummary(ctx context.Context, envID string) (*EnvironmentVulnerabilitySummary, error) {
	body, err := c.get(ctx, "/api/environments/"+envID+"/vulnerabilities/summary")
	if err != nil {
		return nil, err
	}
	var resp apiResponse[EnvironmentVulnerabilitySummary]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse environment summary: %w", err)
	}
	return &resp.Data, nil
}

// GetImageScanSummary returns vulnerability summary for a specific image.
func (c *ArcaneClient) GetImageScanSummary(ctx context.Context, envID, imageID string) (*ScanSummary, error) {
	body, err := c.get(ctx, "/api/environments/"+envID+"/images/"+imageID+"/vulnerabilities/summary")
	if err != nil {
		return nil, err
	}
	var resp apiResponse[ScanSummary]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse image scan summary: %w", err)
	}
	return &resp.Data, nil
}

// GetBatchScanSummaries returns summaries for multiple images.
func (c *ArcaneClient) GetBatchScanSummaries(ctx context.Context, envID string, imageIDs []string) (map[string]*ScanSummary, error) {
	payload, err := json.Marshal(map[string]any{"imageIds": imageIDs})
	if err != nil {
		return nil, err
	}
	body, err := c.post(ctx, "/api/environments/"+envID+"/images/vulnerabilities/summaries", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	var resp apiResponse[struct {
		Summaries map[string]*ScanSummary `json:"summaries"`
	}]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse batch summaries: %w", err)
	}
	return resp.Data.Summaries, nil
}

// ImageSummaryItem is a simplified image record from the list endpoint.
type ImageSummaryItem struct {
	ID        string   `json:"id"`
	RepoTags  []string `json:"repoTags"`
	Repo      string   `json:"repo"`
	Tag       string   `json:"tag"`
	InUse     bool     `json:"inUse"`
}

// ListEnvironmentImages returns all Docker images in an environment.
func (c *ArcaneClient) ListEnvironmentImages(ctx context.Context, envID string) ([]ImageSummaryItem, error) {
	body, err := c.get(ctx, "/api/environments/"+envID+"/images?limit=100")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Success bool                `json:"success"`
		Data    []ImageSummaryItem  `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse environment images: %w", err)
	}
	return resp.Data, nil
}

// ListVulnerabilities returns paginated vulnerabilities for an image.
func (c *ArcaneClient) ListVulnerabilities(ctx context.Context, envID, imageID string, severity string, page, limit int) ([]Vulnerability, int, error) {
	path := "/api/environments/" + envID + "/images/" + imageID + "/vulnerabilities/list?page=" + fmt.Sprint(page) + "&limit=" + fmt.Sprint(limit)
	if severity != "" {
		path += "&severity=" + severity
	}
	body, err := c.get(ctx, path)
	if err != nil {
		return nil, 0, err
	}
	var resp struct {
		Success    bool              `json:"success"`
		Data       []Vulnerability   `json:"data"`
		Pagination struct {
			TotalItems int `json:"totalItems"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("parse vulnerabilities: %w", err)
	}
	return resp.Data, resp.Pagination.TotalItems, nil
}
