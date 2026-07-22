package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/webhook"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type DefinitionProposalFileInput struct {
	Path    string `json:"path" jsonschema:"Path inside the definitions repository"`
	Content string `json:"content" jsonschema:"Complete replacement file content"`
}

type CreateDefinitionProposalInput struct {
	TaskID        string                        `json:"task_id,omitempty" jsonschema:"Chetter task ID from CHETTER_TASK_ID; required for task artifact tracking"`
	SourceID      string                        `json:"source_id,omitempty" jsonschema:"Definition source ID; defaults to the configured default source"`
	Title         string                        `json:"title" jsonschema:"Pull request title"`
	Body          string                        `json:"body,omitempty" jsonschema:"Pull request body with rationale and evidence"`
	Branch        string                        `json:"branch,omitempty" jsonschema:"Proposal branch name; generated if omitted"`
	BaseBranch    string                        `json:"base_branch,omitempty" jsonschema:"Base branch; defaults to the source branch"`
	CommitMessage string                        `json:"commit_message,omitempty" jsonschema:"Commit message for definition file updates"`
	Files         []DefinitionProposalFileInput `json:"files" jsonschema:"Files to create or replace in the definitions repository"`
	Draft         bool                          `json:"draft,omitempty" jsonschema:"Create a draft pull request"`
}

type CreateDefinitionProposalOutput struct {
	Proposal DefinitionProposalToolRecord `json:"proposal"`
}

type ListDefinitionProposalsInput struct {
	SourceID string `json:"source_id,omitempty" jsonschema:"Optional definition source ID filter"`
	Status   string `json:"status,omitempty" jsonschema:"Optional stored proposal status filter"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum proposals to return, capped at 100"`
}

type ListDefinitionProposalsOutput struct {
	Proposals []DefinitionProposalToolRecord `json:"proposals"`
}

type GetDefinitionProposalInput struct {
	ProposalID string `json:"proposal_id,omitempty" jsonschema:"Definition proposal ID"`
	Repo       string `json:"repo,omitempty" jsonschema:"Repository, e.g. flatout-works/chetter"`
	PRNumber   int    `json:"pr_number,omitempty" jsonschema:"Pull request number; use with repo as an alternative to proposal_id"`
}

type GetDefinitionProposalOutput struct {
	Proposal DefinitionProposalToolRecord `json:"proposal"`
}

type DefinitionProposalToolRecord struct {
	ID         string                    `json:"id"`
	SourceID   string                    `json:"source_id"`
	TaskID     string                    `json:"task_id,omitempty"`
	Repo       string                    `json:"repo"`
	Branch     string                    `json:"branch"`
	BaseBranch string                    `json:"base_branch"`
	PRNumber   int                       `json:"pr_number"`
	PRURL      string                    `json:"pr_url"`
	Title      string                    `json:"title"`
	Body       string                    `json:"body,omitempty"`
	Files      []DefinitionProposalFile  `json:"files"`
	Status     string                    `json:"status"`
	CreatedAt  time.Time                 `json:"created_at"`
	UpdatedAt  time.Time                 `json:"updated_at"`
	LiveStatus *DefinitionProposalStatus `json:"live_status,omitempty"`
}

type DefinitionProposalFile struct {
	Path string `json:"path"`
}

type DefinitionProposalStatus struct {
	State        string                  `json:"state"`
	Merged       bool                    `json:"merged"`
	HeadSHA      string                  `json:"head_sha,omitempty"`
	ChangedFiles []string                `json:"changed_files,omitempty"`
	Checks       webhook.CheckRunSummary `json:"checks"`
}

const definitionProposalStatusOpen = "open"

var safeBranchSegment = regexp.MustCompile(`[^a-zA-Z0-9._/-]+`)

func (s *Service) createDefinitionProposalTool(ctx context.Context, _ *mcp.CallToolRequest, in CreateDefinitionProposalInput) (*mcp.CallToolResult, CreateDefinitionProposalOutput, error) {
	if s.githubClient() == nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("GitHub App client is not configured")
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("title is required")
	}
	if len(in.Files) == 0 {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("at least one file is required")
	}
	sourceID := in.SourceID
	if sourceID == "" {
		sourceID = defaultDefinitionSourceID
	}
	source, err := s.repo.GetDefinitionSource(ctx, sourceID)
	if err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("get definition source: %w", err)
	}
	repo, err := githubRepoFromURL(source.RepoUrl)
	if err != nil {
		return nil, CreateDefinitionProposalOutput{}, err
	}
	baseBranch := nonEmpty(in.BaseBranch, source.Branch)
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		branch = generatedDefinitionProposalBranch(in.Title, time.Now().UTC())
	}
	commitMessage := nonEmpty(in.CommitMessage, "chore: propose definition updates")
	baseSHA, err := s.githubClient().GetBranchSHA(ctx, repo, baseBranch)
	if err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("get base branch sha: %w", err)
	}
	if err := s.githubClient().CreateBranch(ctx, repo, branch, baseSHA); err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("create proposal branch: %w", err)
	}
	files := make([]DefinitionProposalFile, 0, len(in.Files))
	for _, file := range in.Files {
		filePath := strings.TrimSpace(file.Path)
		if filePath == "" {
			return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("file path is required")
		}
		if strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "..") {
			return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("file path %q must be relative and must not contain '..'", filePath)
		}
		fullPath := definitionSourceFilePath(source.Path, filePath)
		if err := s.githubClient().UpsertFile(ctx, repo, branch, fullPath, file.Content, commitMessage); err != nil {
			return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("upsert proposal file %s: %w", fullPath, err)
		}
		files = append(files, DefinitionProposalFile{Path: fullPath})
	}
	body := in.Body
	var task repository.ChetterTask
	var userPrompt repository.ChetterUserPrompt
	if strings.TrimSpace(in.TaskID) != "" {
		task, userPrompt, err = s.githubToolTaskContext(ctx, in.TaskID)
		if err != nil {
			return nil, CreateDefinitionProposalOutput{}, err
		}
		body = appendChetterSignature(body, s.githubToolSignature(ctx, task, userPrompt))
	}
	created, err := s.githubClient().CreatePullRequest(ctx, repo, in.Title, body, branch, baseBranch, in.Draft)
	if err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("create definition proposal PR: %w", err)
	}
	proposalID, err := randomID("dprop")
	if err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("generate proposal id: %w", err)
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("marshal files: %w", err)
	}
	now := time.Now().UTC()
	params := repository.InsertDefinitionChangeProposalParams{
		ID:         proposalID,
		SourceID:   source.ID,
		TaskID:     nullString(in.TaskID),
		Repo:       repo,
		Branch:     branch,
		BaseBranch: baseBranch,
		PrNumber:   int32(created.Number),
		PrUrl:      created.URL,
		Title:      in.Title,
		Body:       body,
		Files:      filesJSON,
		Status:     definitionProposalStatusOpen,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repo.InsertDefinitionChangeProposal(ctx, params); err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("store definition proposal: %w", err)
	}
	row, err := s.repo.GetDefinitionChangeProposal(ctx, proposalID)
	if err != nil {
		return nil, CreateDefinitionProposalOutput{}, fmt.Errorf("get stored definition proposal: %w", err)
	}
	if strings.TrimSpace(in.TaskID) != "" {
		if _, _, err := s.recordGitHubToolArtifact(ctx, task, userPrompt, "definition_proposal", repo, created.Number, created.URL, branch, body, map[string]any{
			"title":     in.Title,
			"source_id": source.ID,
			"files":     files,
		}); err != nil {
			return nil, CreateDefinitionProposalOutput{}, err
		}
	}
	return nil, CreateDefinitionProposalOutput{Proposal: definitionProposalToolRecord(row, nil)}, nil
}

func (s *Service) listDefinitionProposalsTool(ctx context.Context, _ *mcp.CallToolRequest, in ListDefinitionProposalsInput) (*mcp.CallToolResult, ListDefinitionProposalsOutput, error) {
	limit := clampListLimit(in.Limit)
	rows, err := s.repo.ListDefinitionChangeProposals(ctx, repository.ListDefinitionChangeProposalsParams{
		Column1:  in.SourceID,
		SourceID: in.SourceID,
		Column3:  in.Status,
		Status:   in.Status,
		Limit:    limit,
	})
	if err != nil {
		return nil, ListDefinitionProposalsOutput{}, fmt.Errorf("list definition proposals: %w", err)
	}
	out := make([]DefinitionProposalToolRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, definitionProposalToolRecord(row, nil))
	}
	return nil, ListDefinitionProposalsOutput{Proposals: out}, nil
}

func (s *Service) getDefinitionProposalTool(ctx context.Context, _ *mcp.CallToolRequest, in GetDefinitionProposalInput) (*mcp.CallToolResult, GetDefinitionProposalOutput, error) {
	row, err := s.definitionProposalByInput(ctx, in)
	if err != nil {
		return nil, GetDefinitionProposalOutput{}, err
	}
	live := s.liveDefinitionProposalStatus(ctx, row)
	if live != nil {
		status := live.State
		if live.Merged {
			status = "merged"
		}
		if status != "" && status != row.Status {
			if err := s.repo.UpdateDefinitionChangeProposalStatus(ctx, repository.UpdateDefinitionChangeProposalStatusParams{Status: status, UpdatedAt: time.Now().UTC(), ID: row.ID}); err == nil {
				row.Status = status
			}
		}
	}
	return nil, GetDefinitionProposalOutput{Proposal: definitionProposalToolRecord(row, live)}, nil
}

func (s *Service) definitionProposalByInput(ctx context.Context, in GetDefinitionProposalInput) (repository.DefinitionChangeProposal, error) {
	if strings.TrimSpace(in.ProposalID) != "" {
		row, err := s.repo.GetDefinitionChangeProposal(ctx, in.ProposalID)
		if err != nil {
			return repository.DefinitionChangeProposal{}, fmt.Errorf("get definition proposal: %w", err)
		}
		return row, nil
	}
	if strings.TrimSpace(in.Repo) == "" || in.PRNumber <= 0 {
		return repository.DefinitionChangeProposal{}, fmt.Errorf("proposal_id or repo+pr_number is required")
	}
	row, err := s.repo.GetDefinitionChangeProposalByPR(ctx, repository.GetDefinitionChangeProposalByPRParams{Repo: in.Repo, PrNumber: int32(in.PRNumber)})
	if err != nil {
		return repository.DefinitionChangeProposal{}, fmt.Errorf("get definition proposal by PR: %w", err)
	}
	return row, nil
}

func (s *Service) liveDefinitionProposalStatus(ctx context.Context, row repository.DefinitionChangeProposal) *DefinitionProposalStatus {
	if s.githubClient() == nil {
		return nil
	}
	details, err := s.githubClient().GetPullRequestDetails(ctx, row.Repo, int(row.PrNumber))
	if err != nil {
		return nil
	}
	files, _ := s.githubClient().ListPRFiles(ctx, row.Repo, int(row.PrNumber))
	checks, _ := s.githubClient().ListCheckRunsForRef(ctx, row.Repo, details.HeadSHA)
	return &DefinitionProposalStatus{State: details.State, Merged: details.Merged, HeadSHA: details.HeadSHA, ChangedFiles: files, Checks: checks}
}

func definitionProposalToolRecord(row repository.DefinitionChangeProposal, live *DefinitionProposalStatus) DefinitionProposalToolRecord {
	files := parseJSON[[]DefinitionProposalFile](row.Files, "proposal:"+row.ID+" files")
	return DefinitionProposalToolRecord{
		ID:         row.ID,
		SourceID:   row.SourceID,
		TaskID:     row.TaskID.String,
		Repo:       row.Repo,
		Branch:     row.Branch,
		BaseBranch: row.BaseBranch,
		PRNumber:   int(row.PrNumber),
		PRURL:      row.PrUrl,
		Title:      row.Title,
		Body:       row.Body,
		Files:      files,
		Status:     row.Status,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
		LiveStatus: live,
	}
}

func githubRepoFromURL(repoURL string) (string, error) {
	trimmed := strings.TrimSpace(repoURL)
	if trimmed == "" {
		return "", fmt.Errorf("definition source repo_url is empty")
	}
	if strings.Count(trimmed, "/") == 1 && !strings.Contains(trimmed, ":") {
		return trimmed, nil
	}
	if strings.HasPrefix(trimmed, "git@github.com:") {
		repo := strings.TrimSuffix(strings.TrimPrefix(trimmed, "git@github.com:"), ".git")
		if strings.Count(repo, "/") == 1 {
			return repo, nil
		}
	}
	u, err := url.Parse(trimmed)
	if err == nil && strings.EqualFold(u.Host, "github.com") {
		repo := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
		if strings.Count(repo, "/") == 1 {
			return repo, nil
		}
	}
	return "", fmt.Errorf("definition source repo_url %q is not a GitHub repository URL", repoURL)
}

func generatedDefinitionProposalBranch(title string, now time.Time) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = safeBranchSegment.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-./")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-./")
	}
	if slug == "" {
		slug = "definition-update"
	}
	return "automation/definition-proposal-" + now.Format("20060102-150405") + "-" + slug
}

func definitionSourceFilePath(sourcePath, filePath string) string {
	sourcePath = strings.Trim(strings.TrimSpace(sourcePath), "/")
	filePath = strings.Trim(strings.TrimSpace(filePath), "/")
	if sourcePath == "" {
		return filePath
	}
	return path.Join(sourcePath, filePath)
}
