// Package webhook handles GitHub webhook events for the Chetter service.
package webhook

// Event payload structs matching the GitHub webhook JSON schema.
// Field tags use the GitHub field names exactly so json.Unmarshal works
// against the raw webhook body. We only model the fields we use.

const (
	// Action values for the pull_request event.
	PullRequestActionOpened      = "opened"
	PullRequestActionSynchronize = "synchronize"
	PullRequestActionReopened    = "reopened"
	PullRequestActionLabeled     = "labeled"

	// EventType values for the X-GitHub-Event header.
	EventTypePullRequest              = "pull_request"
	EventTypeIssueComment             = "issue_comment"
	EventTypeIssues                   = "issues"
	EventTypePullRequestReview        = "pull_request_review"
	EventTypePullRequestReviewComment = "pull_request_review_comment"

	// ChetterReviewLabel is the label we add to PRs that should be reviewed.
	ChetterReviewLabel = "chetter-review"

	// ReviewTrigger comment that users post to request a review.
	ReviewTriggerCommand = "/chetter-review"

	// Trigger event values for trigger_config's "event" field.
	TriggerEventOpened      = "opened"
	TriggerEventLabeled     = "labeled"
	TriggerEventComment     = "comment"
	TriggerEventFork        = "fork"
	TriggerEventCreated     = "created" // issue created
	TriggerEventSynchronize = "synchronize"
)

// PullRequestEvent is the top-level payload for a pull_request webhook event.
type PullRequestEvent struct {
	Action      string      `json:"action"`
	Number      int         `json:"number"`
	PullRequest PullRequest `json:"pull_request"`
	Label       *Label      `json:"label,omitempty"`
	Repository  Repository  `json:"repository"`
}

// PullRequestReviewEvent is the top-level payload for pull_request_review.
type PullRequestReviewEvent struct {
	Action      string      `json:"action"`
	PullRequest PullRequest `json:"pull_request"`
	Review      Review      `json:"review"`
	Repository  Repository  `json:"repository"`
}

// PullRequestReviewCommentEvent is the top-level payload for pull_request_review_comment.
type PullRequestReviewCommentEvent struct {
	Action      string      `json:"action"`
	PullRequest PullRequest `json:"pull_request"`
	Comment     Comment     `json:"comment"`
	Repository  Repository  `json:"repository"`
}

// Review is the relevant subset of a pull request review object.
type Review struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// PullRequest is the relevant subset of the pull_request object.
type PullRequest struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`

	Head PRBranch `json:"head"`
	Base PRBranch `json:"base"`

	User struct {
		Login string `json:"login"`
	} `json:"user"`

	Labels []Label `json:"labels"`
}

// PRBranch is the head or base ref of a pull request.
type PRBranch struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repo"`
}

// Label is a PR or issue label.
type Label struct {
	Name string `json:"name"`
}

// Repository is the relevant subset of the repository object.
type Repository struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// IssueCommentEvent is the top-level payload for an issue_comment webhook event.
// For PR comments, the Issue object includes a `pull_request` field which we
// use to determine that this is a PR comment (vs an issue comment).
type IssueCommentEvent struct {
	Action     string     `json:"action"`
	Comment    Comment    `json:"comment"`
	Issue      Issue      `json:"issue"`
	Repository Repository `json:"repository"`
}

// Comment is the issue/PR comment object.
type Comment struct {
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Issue is the issue/PR object (PRs come through the issues API).
type Issue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request,omitempty"`
	Labels []Label `json:"labels"`
}

// IsPullRequest returns true if the issue is actually a pull request.
func (e *IssueCommentEvent) IsPullRequest() bool {
	return e.Issue.PullRequest != nil
}

// IssueEvent is the top-level payload for an issues webhook event.
type IssueEvent struct {
	Action     string     `json:"action"`
	Issue      IssueData  `json:"issue"`
	Label      *Label     `json:"label,omitempty"`
	Repository Repository `json:"repository"`
}

// IssueData is the relevant subset of the issue object.
type IssueData struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []Label `json:"labels"`
}
