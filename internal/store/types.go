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
	PR             *PRMeta
}

// PRMeta 记录 Pull Request 的分支信息，Agent 检出代码与回推修改时使用。
type PRMeta struct {
	HeadRef             string `json:"head_ref"`
	HeadOID             string `json:"head_oid"`
	HeadRepo            string `json:"head_repo"`
	BaseRef             string `json:"base_ref"`
	BaseOID             string `json:"base_oid"`
	IsCrossRepository   bool   `json:"is_cross_repository"`
	MaintainerCanModify bool   `json:"maintainer_can_modify"`
	Additions           int    `json:"additions"`
	Deletions           int    `json:"deletions"`
	ChangedFiles        int    `json:"changed_files"`
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
	PR             *PRMeta        `json:"pr,omitempty"`
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

// Action 是一个可复用的 Agent 操作模板（例如“Claude 代码审查”）。
type Action struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Agent        string `json:"agent"`
	Mode         string `json:"mode"`
	AppliesTo    string `json:"applies_to"`
	Skill        string `json:"skill"`
	Prompt       string `json:"prompt"`
	Model        string `json:"model"`
	AllowedTools string `json:"allowed_tools"`
	Position     int    `json:"position"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type ActionInput struct {
	Name         *string `json:"name"`
	Agent        *string `json:"agent"`
	Mode         *string `json:"mode"`
	AppliesTo    *string `json:"applies_to"`
	Skill        *string `json:"skill"`
	Prompt       *string `json:"prompt"`
	Model        *string `json:"model"`
	AllowedTools *string `json:"allowed_tools"`
	Position     *int    `json:"position"`
}

// AgentRun 记录一次本机 Claude Code / Codex 调用。
type AgentRun struct {
	ID           int64   `json:"id"`
	ItemID       int64   `json:"item_id"`
	Repo         string  `json:"repo"`
	Number       int     `json:"number"`
	Kind         string  `json:"kind"`
	Title        string  `json:"title"`
	ActionID     int64   `json:"action_id"`
	ActionName   string  `json:"action_name"`
	Agent        string  `json:"agent"`
	Mode         string  `json:"mode"`
	Skill        string  `json:"skill"`
	Model        string  `json:"model"`
	Prompt       string  `json:"prompt"`
	Status       string  `json:"status"`
	Error        string  `json:"error"`
	SessionID    string  `json:"session_id"`
	Result       string  `json:"result"`
	WorkDir      string  `json:"work_dir"`
	BaseSHA      string  `json:"base_sha"`
	BaseRef      string  `json:"base_ref"`
	HeadRef      string  `json:"head_ref"`
	HeadRepo     string  `json:"head_repo"`
	DiffStat     string  `json:"diff_stat"`
	HasChanges   bool    `json:"has_changes"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	PublishedAt  string  `json:"published_at"`
	PublishedURL string  `json:"published_url"`
	AppliedAt    string  `json:"applied_at"`
	AppliedURL   string  `json:"applied_url"`
	Discarded    bool    `json:"discarded"`
	CreatedAt    string  `json:"created_at"`
	StartedAt    string  `json:"started_at"`
	FinishedAt   string  `json:"finished_at"`
}

// RunUpdate 描述对运行记录的部分更新，nil 字段保持不变。
type RunUpdate struct {
	Prompt       *string
	Status       *string
	Error        *string
	SessionID    *string
	Result       *string
	WorkDir      *string
	BaseSHA      *string
	BaseRef      *string
	HeadRef      *string
	HeadRepo     *string
	DiffStat     *string
	HasChanges   *bool
	CostUSD      *float64
	DurationMS   *int64
	PublishedAt  *string
	PublishedURL *string
	AppliedAt    *string
	AppliedURL   *string
	Discarded    *bool
	StartedAt    *string
	FinishedAt   *string
}
