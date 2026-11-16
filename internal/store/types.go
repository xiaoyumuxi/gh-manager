package store

import "time"

type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Tag struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Count     int    `json:"count"`
	CreatedAt string `json:"created_at"`
}

type FieldDef struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Options   []string `json:"options"`
	Position  int      `json:"position"`
	CreatedAt string   `json:"created_at"`
}

type GitHubItem struct {
	Repo           string
	Number         int
	Kind           string
	Title          string
	URL            string
	State          string
	IsDraft        bool
	Author         string
	Body           string
	Labels         []Label
	Assignees      []string
	Reviewers      []string
	Milestone      string
	ReviewDecision string
	CIStatus       string
	Mergeable      string
	Comments       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClosedAt       *time.Time
	MergedAt       *time.Time
}

type Item struct {
	ID             int64          `json:"id"`
	Source         string         `json:"source"`
	Repo           string         `json:"repo"`
	Number         int            `json:"number"`
	Kind           string         `json:"kind"`
	Title          string         `json:"title"`
	URL            string         `json:"url"`
	State          string         `json:"state"`
	IsDraft        bool           `json:"is_draft"`
	Author         string         `json:"author"`
	Body           string         `json:"body"`
	Labels         []Label        `json:"github_labels"`
	Assignees      []string       `json:"assignees"`
	Reviewers      []string       `json:"reviewers"`
	Milestone      string         `json:"milestone"`
	ReviewDecision string         `json:"review_decision"`
	CIStatus       string         `json:"ci_status"`
	Mergeable      string         `json:"mergeable"`
	Comments       int            `json:"comments"`
	CreatedAt      *time.Time     `json:"created_at"`
	UpdatedAt      *time.Time     `json:"updated_at"`
	ClosedAt       *time.Time     `json:"closed_at"`
	MergedAt       *time.Time     `json:"merged_at"`
	FirstSeenAt    string         `json:"first_seen_at"`
	LastSeenAt     string         `json:"last_seen_at"`
	Tags           []Tag          `json:"tags"`
	Fields         map[string]any `json:"fields"`
	StaleDays      int            `json:"stale_days"`
	Stale          bool           `json:"stale"`
	ReviewStale    bool           `json:"review_stale"`
}

type SyncRun struct {
	ID         int64  `json:"id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	RepoCount  int    `json:"repo_count"`
	ItemCount  int    `json:"item_count"`
}

type RepoStatus struct {
	FullName          string `json:"full_name"`
	TrackedBranch     string `json:"tracked_branch"`
	LastSynced        string `json:"last_synced_at"`
	LastError         string `json:"last_error"`
	CommitSHA         string `json:"commit_sha"`
	CommitMessage     string `json:"commit_message"`
	CommitCommittedAt string `json:"commit_committed_at"`
}

type RepoResult struct {
	FullName          string
	TrackedBranch     string
	LastError         string
	CommitSHA         string
	CommitMessage     string
	CommitCommittedAt string
	Synced            bool
	UpdateHead        bool
}

type SyncSummary struct {
	Repos int `json:"repos"`
	Items int `json:"items"`
}
