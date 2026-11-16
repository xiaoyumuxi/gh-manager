package store

const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	source TEXT NOT NULL DEFAULT 'github' CHECK (source IN ('github', 'manual')),
	repo TEXT NOT NULL COLLATE NOCASE,
	number INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('issue', 'pr')),
	title TEXT NOT NULL,
	url TEXT NOT NULL,
	state TEXT NOT NULL,
	is_draft INTEGER NOT NULL DEFAULT 0,
	author TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	labels_json TEXT NOT NULL DEFAULT '[]',
	assignees_json TEXT NOT NULL DEFAULT '[]',
	reviewers_json TEXT NOT NULL DEFAULT '[]',
	milestone TEXT NOT NULL DEFAULT '',
	review_decision TEXT NOT NULL DEFAULT '',
	ci_status TEXT NOT NULL DEFAULT '',
	mergeable TEXT NOT NULL DEFAULT '',
	comments_count INTEGER NOT NULL DEFAULT 0,
	created_at TEXT,
	updated_at TEXT,
	closed_at TEXT,
	merged_at TEXT,
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	synced_at TEXT NOT NULL,
	metadata TEXT NOT NULL DEFAULT '{}',
	UNIQUE (source, repo, kind, number)
);

CREATE INDEX IF NOT EXISTS items_repo_state_idx ON items (repo, state);
CREATE INDEX IF NOT EXISTS items_updated_idx ON items (updated_at DESC);

CREATE TABLE IF NOT EXISTS tags (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	color TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS item_tags (
	item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
	tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (item_id, tag_id)
);

CREATE INDEX IF NOT EXISTS item_tags_tag_idx ON item_tags (tag_id);

CREATE TABLE IF NOT EXISTS field_defs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	type TEXT NOT NULL CHECK (type IN ('text', 'long_text', 'number', 'select', 'multi_select', 'date', 'checkbox', 'url')),
	options_json TEXT NOT NULL DEFAULT '[]',
	position INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS item_field_values (
	item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
	field_id INTEGER NOT NULL REFERENCES field_defs(id) ON DELETE CASCADE,
	value_json TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (item_id, field_id)
);

CREATE TABLE IF NOT EXISTS item_snapshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
	captured_at TEXT NOT NULL,
	state TEXT NOT NULL,
	is_draft INTEGER NOT NULL,
	review_decision TEXT NOT NULL,
	ci_status TEXT NOT NULL,
	mergeable TEXT NOT NULL,
	github_updated_at TEXT
);

CREATE INDEX IF NOT EXISTS item_snapshots_item_idx ON item_snapshots (item_id, captured_at DESC);

CREATE TABLE IF NOT EXISTS repos (
	full_name TEXT PRIMARY KEY COLLATE NOCASE,
	tracked_branch TEXT NOT NULL DEFAULT '',
	last_synced_at TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	commit_sha TEXT NOT NULL DEFAULT '',
	commit_message TEXT NOT NULL DEFAULT '',
	commit_committed_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sync_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	repo_count INTEGER NOT NULL DEFAULT 0,
	item_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS actions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	agent TEXT NOT NULL CHECK (agent IN ('claude', 'codex')),
	mode TEXT NOT NULL CHECK (mode IN ('read', 'edit')),
	applies_to TEXT NOT NULL DEFAULT 'all' CHECK (applies_to IN ('all', 'pr', 'issue')),
	skill TEXT NOT NULL DEFAULT '',
	prompt TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	allowed_tools TEXT NOT NULL DEFAULT '',
	position INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	item_id INTEGER NOT NULL,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	kind TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	action_id INTEGER NOT NULL DEFAULT 0,
	action_name TEXT NOT NULL DEFAULT '',
	agent TEXT NOT NULL,
	mode TEXT NOT NULL,
	skill TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	prompt TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL DEFAULT '',
	work_dir TEXT NOT NULL DEFAULT '',
	base_sha TEXT NOT NULL DEFAULT '',
	base_ref TEXT NOT NULL DEFAULT '',
	head_ref TEXT NOT NULL DEFAULT '',
	head_repo TEXT NOT NULL DEFAULT '',
	diff_stat TEXT NOT NULL DEFAULT '',
	has_changes INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	published_at TEXT NOT NULL DEFAULT '',
	published_url TEXT NOT NULL DEFAULT '',
	applied_at TEXT NOT NULL DEFAULT '',
	applied_url TEXT NOT NULL DEFAULT '',
	discarded INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	started_at TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS agent_runs_item_idx ON agent_runs (item_id, id DESC);
CREATE INDEX IF NOT EXISTS agent_runs_status_idx ON agent_runs (status);

CREATE TABLE IF NOT EXISTS sync_lock (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	owner TEXT NOT NULL,
	acquired_at TEXT NOT NULL
);
`

var defaultTags = []struct {
	Name  string
	Color string
}{
	{Name: "重点", Color: "#D83931"},
	{Name: "待处理", Color: "#F54A45"},
	{Name: "进行中", Color: "#3370FF"},
	{Name: "待跟进", Color: "#7F3BF5"},
}

type defaultAction struct {
	Name      string
	Agent     string
	Mode      string
	AppliesTo string
	Prompt    string
}

var defaultActions = []defaultAction{
	{
		Name:      "Claude 代码审查",
		Agent:     "claude",
		Mode:      "read",
		AppliesTo: "pr",
		Prompt:    defaultReviewPrompt,
	},
	{
		Name:      "Codex 代码审查",
		Agent:     "codex",
		Mode:      "read",
		AppliesTo: "pr",
		Prompt:    defaultReviewPrompt,
	},
	{
		Name:      "Issue 分析",
		Agent:     "claude",
		Mode:      "read",
		AppliesTo: "issue",
		Prompt: `请分析这个 Issue：结合当前目录中的代码定位相关实现，判断问题是否成立、可能的根因，并给出修复方案（涉及的文件和大致改法）。
如果「仓库规则」中有相关的开发规范或 SKILL，结论要符合这些约定。

输出中文 Markdown，可作为 Issue 回复草稿。`,
	},
	{
		Name:      "Claude 修复 Issue",
		Agent:     "claude",
		Mode:      "edit",
		AppliesTo: "issue",
		Prompt: `请在当前目录中直接修改代码来解决这个 Issue。保持改动最小、聚焦，必要时补充测试。
如果「仓库规则」中有开发规范相关的 SKILL（例如 develop-*、write-*-tests），先阅读并遵循。
不要执行 git commit / git push，改动会由用户在网页中确认后提交。

完成后用中文 Markdown 输出：改动摘要、涉及文件、验证方式。这段内容会作为 Pull Request 描述草稿。`,
	},
	{
		Name:      "按评审意见修改 PR",
		Agent:     "claude",
		Mode:      "edit",
		AppliesTo: "pr",
		Prompt: `请根据上下文中列出的评审意见与评论，在当前目录中修改这个 Pull Request 的代码。只处理合理且明确的意见，对不认同的意见说明理由。
如果「仓库规则」中有开发规范相关的 SKILL，修改要符合这些约定。
不要执行 git commit / git push，改动会由用户在网页中确认后推送。

完成后用中文 Markdown 输出：逐条说明每个意见的处理情况。`,
	},
}

const defaultReviewPrompt = `请审查这个 Pull Request 的全部改动（使用上下文中给出的 git diff 命令查看），并沿调用链检查直接受影响的代码、测试和文档。

如果「仓库规则」中有代码审查相关的 SKILL（例如 *-code-review），按该 SKILL 的流程、严重度规则和输出格式完成审查，它的要求优先于下面的默认格式。审查范围就是上面给出的「目标分支 ← PR 分支」。

没有这类 SKILL 时，输出中文 Markdown（会作为评审评论草稿）：
1. **结论**：✅ 未发现明确问题 / ⚠️ 建议修改 / ⛔ 存在阻塞问题，并用一句话概括这个 PR；
2. **Findings**：按严重度 P0–P3 排序，每条写明 ` + "`文件:行号`" + `、触发条件、实际影响和修复方向；只报告有把握、可复现的问题，不报告风格偏好；
3. 没有实质问题时明确写出“未发现需要阻塞合并的问题。”，并列出仍未验证的风险。`
