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
	last_synced_at TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT ''
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
