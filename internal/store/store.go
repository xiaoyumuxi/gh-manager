package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const dbTimeLayout = "2006-01-02T15:04:05.000000000Z"

var ErrNotFound = errors.New("记录不存在")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	u := url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("初始化数据库结构失败: %w", err)
	}
	now := dbTime(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, tag := range defaultTags {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO tags (name, color, created_at) VALUES (?, ?, ?)`,
			tag.Name, tag.Color, now,
		); err != nil {
			return fmt.Errorf("初始化默认标签失败: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '1')
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ApplyRepoSnapshot(ctx context.Context, repo string, items []GitHubItem, closeMissing bool, boundary time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	for _, item := range items {
		if err := upsertItem(ctx, tx, repo, item, now); err != nil {
			return 0, err
		}
	}
	if closeMissing {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items
			 SET state = 'closed', synced_at = ?
			 WHERE source = 'github' AND repo = ? AND state = 'open' AND last_seen_at < ?`,
			dbTime(now), repo, dbTime(boundary),
		); err != nil {
			return 0, fmt.Errorf("更新缺失条目状态失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(items), nil
}

func upsertItem(ctx context.Context, tx *sql.Tx, repo string, item GitHubItem, now time.Time) error {
	item.State = strings.ToLower(strings.TrimSpace(item.State))
	if item.State == "" {
		item.State = "open"
	}
	labelsJSON, _ := json.Marshal(item.Labels)
	assigneesJSON, _ := json.Marshal(item.Assignees)
	reviewersJSON, _ := json.Marshal(item.Reviewers)
	if item.Labels == nil {
		labelsJSON = []byte("[]")
	}
	if item.Assignees == nil {
		assigneesJSON = []byte("[]")
	}
	if item.Reviewers == nil {
		reviewersJSON = []byte("[]")
	}

	var existingID int64
	var oldState, oldDraft, oldReview, oldCI, oldMergeable, oldUpdated string
	err := tx.QueryRowContext(ctx,
		`SELECT id, state, is_draft, review_decision, ci_status, mergeable, COALESCE(updated_at, '')
		 FROM items WHERE source = 'github' AND repo = ? AND kind = ? AND number = ?`,
		repo, item.Kind, item.Number,
	).Scan(&existingID, &oldState, &oldDraft, &oldReview, &oldCI, &oldMergeable, &oldUpdated)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取条目失败 %s#%d: %w", repo, item.Number, err)
	}

	createdAt := nullableTime(item.CreatedAt)
	updatedAt := nullableTime(item.UpdatedAt)
	closedAt := nullableOptionalTime(item.ClosedAt)
	mergedAt := nullableOptionalTime(item.MergedAt)
	draft := 0
	if item.IsDraft {
		draft = 1
	}

	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO items (
				source, repo, number, kind, title, url, state, is_draft, author, body,
				labels_json, assignees_json, reviewers_json, milestone, review_decision,
				ci_status, mergeable, comments_count, created_at, updated_at, closed_at,
				merged_at, first_seen_at, last_seen_at, synced_at
			) VALUES ('github', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			repo, item.Number, item.Kind, item.Title, item.URL, item.State, draft, item.Author, item.Body,
			string(labelsJSON), string(assigneesJSON), string(reviewersJSON), item.Milestone,
			item.ReviewDecision, item.CIStatus, item.Mergeable, item.Comments, createdAt, updatedAt,
			closedAt, mergedAt, dbTime(now), dbTime(now), dbTime(now),
		)
		if err != nil {
			return fmt.Errorf("插入条目失败 %s#%d: %w", repo, item.Number, err)
		}
		return nil
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE items SET
			title = ?, url = ?, state = ?, is_draft = ?, author = ?, body = ?,
			labels_json = ?, assignees_json = ?, reviewers_json = ?, milestone = ?,
			review_decision = ?, ci_status = ?, mergeable = ?, comments_count = ?,
			created_at = ?, updated_at = ?, closed_at = ?, merged_at = ?,
			last_seen_at = ?, synced_at = ?
		 WHERE id = ?`,
		item.Title, item.URL, item.State, draft, item.Author, item.Body,
		string(labelsJSON), string(assigneesJSON), string(reviewersJSON), item.Milestone,
		item.ReviewDecision, item.CIStatus, item.Mergeable, item.Comments,
		createdAt, updatedAt, closedAt, mergedAt, dbTime(now), dbTime(now), existingID,
	)
	if err != nil {
		return fmt.Errorf("更新条目失败 %s#%d: %w", repo, item.Number, err)
	}

	if oldState != item.State || oldDraft != strconv.Itoa(draft) || oldReview != item.ReviewDecision ||
		oldCI != item.CIStatus || oldMergeable != item.Mergeable || oldUpdated != timeString(item.UpdatedAt) {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO item_snapshots (
				item_id, captured_at, state, is_draft, review_decision, ci_status,
				mergeable, github_updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			existingID, dbTime(now), item.State, draft, item.ReviewDecision,
			item.CIStatus, item.Mergeable, nullableTime(item.UpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("记录条目快照失败: %w", err)
		}
	}
	return nil
}

func (s *Store) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source, repo, number, kind, title, url, state, is_draft, author, body,
			labels_json, assignees_json, reviewers_json, milestone, review_decision,
			ci_status, mergeable, comments_count, created_at, updated_at, closed_at,
			merged_at, first_seen_at, last_seen_at
		 FROM items ORDER BY COALESCE(updated_at, '') DESC, repo, number DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Item, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		var item Item
		var draft int
		var labelsJSON, assigneesJSON, reviewersJSON string
		var createdAt, updatedAt, closedAt, mergedAt sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Source, &item.Repo, &item.Number, &item.Kind, &item.Title,
			&item.URL, &item.State, &draft, &item.Author, &item.Body, &labelsJSON,
			&assigneesJSON, &reviewersJSON, &item.Milestone, &item.ReviewDecision,
			&item.CIStatus, &item.Mergeable, &item.Comments, &createdAt, &updatedAt,
			&closedAt, &mergedAt, &item.FirstSeenAt, &item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		item.IsDraft = draft == 1
		item.CreatedAt = parseNullableTime(createdAt)
		item.UpdatedAt = parseNullableTime(updatedAt)
		item.ClosedAt = parseNullableTime(closedAt)
		item.MergedAt = parseNullableTime(mergedAt)
		_ = json.Unmarshal([]byte(labelsJSON), &item.Labels)
		_ = json.Unmarshal([]byte(assigneesJSON), &item.Assignees)
		_ = json.Unmarshal([]byte(reviewersJSON), &item.Reviewers)
		item.Tags = []Tag{}
		item.Fields = map[string]any{}
		items = append(items, item)
		ids = append(ids, item.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return items, nil
	}

	itemIndex := make(map[int64]int, len(items))
	for i, id := range ids {
		itemIndex[id] = i
	}
	tagRows, err := s.db.QueryContext(ctx,
		`SELECT it.item_id, t.id, t.name, t.color, t.created_at, 0
		 FROM item_tags it JOIN tags t ON t.id = it.tag_id
		 ORDER BY t.name`,
	)
	if err != nil {
		return nil, err
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var itemID int64
		var tag Tag
		if err := tagRows.Scan(&itemID, &tag.ID, &tag.Name, &tag.Color, &tag.CreatedAt, &tag.Count); err != nil {
			return nil, err
		}
		if index, ok := itemIndex[itemID]; ok {
			items[index].Tags = append(items[index].Tags, tag)
		}
	}
	if err := tagRows.Err(); err != nil {
		return nil, err
	}

	fieldRows, err := s.db.QueryContext(ctx,
		`SELECT item_id, field_id, value_json FROM item_field_values`,
	)
	if err != nil {
		return nil, err
	}
	defer fieldRows.Close()
	for fieldRows.Next() {
		var itemID, fieldID int64
		var valueJSON string
		if err := fieldRows.Scan(&itemID, &fieldID, &valueJSON); err != nil {
			return nil, err
		}
		if index, ok := itemIndex[itemID]; ok {
			var value any
			if json.Unmarshal([]byte(valueJSON), &value) == nil {
				items[index].Fields[strconv.FormatInt(fieldID, 10)] = value
			}
		}
	}
	return items, fieldRows.Err()
}

func (s *Store) ListTags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.name, t.color, t.created_at, COUNT(it.item_id) AS item_count
		 FROM tags t LEFT JOIN item_tags it ON it.tag_id = t.id
		 GROUP BY t.id, t.name, t.color, t.created_at
		 ORDER BY t.name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := make([]Tag, 0)
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Color, &tag.CreatedAt, &tag.Count); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *Store) CreateTag(ctx context.Context, name, color string) (Tag, error) {
	name = strings.TrimSpace(name)
	if err := validateTagName(name); err != nil {
		return Tag{}, err
	}
	if color == "" {
		color = DeriveColor(name)
	}
	if err := validateColor(color); err != nil {
		return Tag{}, err
	}
	now := dbTime(time.Now())
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO tags (name, color, created_at) VALUES (?, ?, ?)`, name, color, now,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Tag{}, fmt.Errorf("标签 %q 已存在", name)
		}
		return Tag{}, err
	}
	id, _ := result.LastInsertId()
	return Tag{ID: id, Name: name, Color: color, CreatedAt: now}, nil
}

func (s *Store) UpdateTag(ctx context.Context, id int64, name, color *string) (Tag, error) {
	tag, err := s.getTag(ctx, id)
	if err != nil {
		return Tag{}, err
	}
	nextName := tag.Name
	nextColor := tag.Color
	if name != nil {
		nextName = strings.TrimSpace(*name)
		if err := validateTagName(nextName); err != nil {
			return Tag{}, err
		}
	}
	if color != nil {
		nextColor = *color
		if err := validateColor(nextColor); err != nil {
			return Tag{}, err
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tags SET name = ?, color = ? WHERE id = ?`, nextName, nextColor, id,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Tag{}, fmt.Errorf("标签 %q 已存在", nextName)
		}
		return Tag{}, err
	}
	tag.Name = nextName
	tag.Color = nextColor
	return tag, nil
}

func (s *Store) DeleteTag(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) getTag(ctx context.Context, id int64) (Tag, error) {
	var tag Tag
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, color, created_at FROM tags WHERE id = ?`, id,
	).Scan(&tag.ID, &tag.Name, &tag.Color, &tag.CreatedAt)
	if err != nil {
		return Tag{}, normalizeNotFound(err)
	}
	return tag, nil
}

func (s *Store) SetItemTags(ctx context.Context, itemID int64, tagIDs []int64) ([]Tag, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE id = ?`, itemID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE item_id = ?`, itemID); err != nil {
		return nil, err
	}
	now := dbTime(time.Now())
	seen := map[int64]struct{}{}
	for _, tagID := range tagIDs {
		if _, ok := seen[tagID]; ok {
			continue
		}
		seen[tagID] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_tags (item_id, tag_id, created_at) VALUES (?, ?, ?)`,
			itemID, tagID, now,
		); err != nil {
			return nil, fmt.Errorf("标签不存在或写入失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.tagsForItem(ctx, itemID)
}

func (s *Store) tagsForItem(ctx context.Context, itemID int64) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.name, t.color, t.created_at, 0
		 FROM tags t JOIN item_tags it ON it.tag_id = t.id
		 WHERE it.item_id = ? ORDER BY t.name`,
		itemID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []Tag{}
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Color, &tag.CreatedAt, &tag.Count); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *Store) ListFields(ctx context.Context) ([]FieldDef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, type, options_json, position, created_at
		 FROM field_defs ORDER BY position, id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := make([]FieldDef, 0)
	for rows.Next() {
		var field FieldDef
		var optionsJSON string
		if err := rows.Scan(&field.ID, &field.Name, &field.Type, &optionsJSON, &field.Position, &field.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(optionsJSON), &field.Options)
		fields = append(fields, field)
	}
	return fields, rows.Err()
}

func (s *Store) CreateField(ctx context.Context, name, fieldType string, options []string) (FieldDef, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FieldDef{}, errors.New("字段名不能为空")
	}
	if err := validateFieldType(fieldType); err != nil {
		return FieldDef{}, err
	}
	options = normalizeOptions(options)
	if (fieldType == "select" || fieldType == "multi_select") && len(options) == 0 {
		return FieldDef{}, errors.New("单选或多选字段至少需要一个选项")
	}
	optionsJSON, _ := json.Marshal(options)
	var position int
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM field_defs`).Scan(&position)
	now := dbTime(time.Now())
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO field_defs (name, type, options_json, position, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		name, fieldType, string(optionsJSON), position, now,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return FieldDef{}, fmt.Errorf("字段 %q 已存在", name)
		}
		return FieldDef{}, err
	}
	id, _ := result.LastInsertId()
	return FieldDef{ID: id, Name: name, Type: fieldType, Options: options, Position: position, CreatedAt: now}, nil
}

func (s *Store) UpdateField(ctx context.Context, id int64, name *string, options *[]string) (FieldDef, error) {
	var field FieldDef
	var optionsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, options_json, position, created_at FROM field_defs WHERE id = ?`,
		id,
	).Scan(&field.ID, &field.Name, &field.Type, &optionsJSON, &field.Position, &field.CreatedAt)
	if err != nil {
		return FieldDef{}, normalizeNotFound(err)
	}
	_ = json.Unmarshal([]byte(optionsJSON), &field.Options)

	nextName := field.Name
	nextOptions := field.Options
	if name != nil {
		nextName = strings.TrimSpace(*name)
		if nextName == "" {
			return FieldDef{}, errors.New("字段名不能为空")
		}
	}
	if options != nil {
		nextOptions = normalizeOptions(*options)
		if (field.Type == "select" || field.Type == "multi_select") && len(nextOptions) == 0 {
			return FieldDef{}, errors.New("单选或多选字段至少需要一个选项")
		}
	}
	encoded, _ := json.Marshal(nextOptions)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE field_defs SET name = ?, options_json = ? WHERE id = ?`,
		nextName, string(encoded), id,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return FieldDef{}, fmt.Errorf("字段 %q 已存在", nextName)
		}
		return FieldDef{}, err
	}
	field.Name = nextName
	field.Options = nextOptions
	return field, nil
}

func (s *Store) DeleteField(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM field_defs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetItemField(ctx context.Context, itemID, fieldID int64, raw json.RawMessage) (any, error) {
	var field FieldDef
	var optionsJSON string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, options_json, position, created_at FROM field_defs WHERE id = ?`,
		fieldID,
	).Scan(&field.ID, &field.Name, &field.Type, &optionsJSON, &field.Position, &field.CreatedAt); err != nil {
		return nil, normalizeNotFound(err)
	}
	_ = json.Unmarshal([]byte(optionsJSON), &field.Options)
	normalized, value, err := normalizeFieldValue(field, raw)
	if err != nil {
		return nil, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE id = ?`, itemID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO item_field_values (item_id, field_id, value_json, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(item_id, field_id) DO UPDATE SET
			value_json = excluded.value_json, updated_at = excluded.updated_at`,
		itemID, fieldID, string(normalized), dbTime(time.Now()),
	); err != nil {
		return nil, err
	}
	return value, nil
}

func (s *Store) ClearItemField(ctx context.Context, itemID, fieldID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM item_field_values WHERE item_id = ? AND field_id = ?`,
		itemID, fieldID,
	)
	return err
}

func (s *Store) AcquireSyncLock(ctx context.Context, owner string) (bool, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_lock (id, owner, acquired_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET owner = excluded.owner, acquired_at = excluded.acquired_at
		 WHERE sync_lock.acquired_at < ?`,
		owner, dbTime(now), dbTime(now.Add(-30*time.Minute)),
	)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

func (s *Store) ReleaseSyncLock(ctx context.Context, owner string) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM sync_lock WHERE id = 1 AND owner = ?`, owner)
}

func (s *Store) StartSyncRun(ctx context.Context, repoCount int) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_runs (started_at, status, repo_count) VALUES (?, 'running', ?)`,
		dbTime(time.Now()), repoCount,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) FinishSyncRun(ctx context.Context, id int64, status, errorText string, itemCount int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sync_runs SET finished_at = ?, status = ?, error = ?, item_count = ? WHERE id = ?`,
		dbTime(time.Now()), status, errorText, itemCount, id,
	)
	return err
}

func (s *Store) LatestSyncRun(ctx context.Context) (*SyncRun, error) {
	var run SyncRun
	err := s.db.QueryRowContext(ctx,
		`SELECT id, started_at, finished_at, status, error, repo_count, item_count
		 FROM sync_runs ORDER BY id DESC LIMIT 1`,
	).Scan(&run.ID, &run.StartedAt, &run.FinishedAt, &run.Status, &run.Error, &run.RepoCount, &run.ItemCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Store) SetRepoResult(ctx context.Context, fullName, errorText string) error {
	now := dbTime(time.Now())
	lastSynced := now
	if errorText != "" {
		lastSynced = ""
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO repos (full_name, last_synced_at, last_error) VALUES (?, ?, ?)
		 ON CONFLICT(full_name) DO UPDATE SET
			last_synced_at = CASE WHEN excluded.last_error = '' THEN excluded.last_synced_at ELSE repos.last_synced_at END,
			last_error = excluded.last_error`,
		fullName, lastSynced, errorText,
	)
	return err
}

func (s *Store) ListRepoStatuses(ctx context.Context) ([]RepoStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT full_name, last_synced_at, last_error FROM repos ORDER BY full_name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repos := make([]RepoStatus, 0)
	for rows.Next() {
		var repo RepoStatus
		if err := rows.Scan(&repo.FullName, &repo.LastSynced, &repo.LastError); err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func DeriveColor(name string) string {
	palette := []string{
		"#D83931", "#F54A45", "#FF8800", "#7F3BF5", "#3370FF",
		"#1F8B4C", "#0E9F9F", "#C453C8", "#A45A2A", "#5B65F5",
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(name)))
	return palette[int(h.Sum32())%len(palette)]
}

func validateTagName(name string) error {
	if name == "" {
		return errors.New("标签名不能为空")
	}
	if len([]rune(name)) > 24 {
		return errors.New("标签名最多 24 个字符")
	}
	return nil
}

func validateColor(color string) error {
	if len(color) != 7 || color[0] != '#' {
		return errors.New("颜色必须是 #RRGGBB 格式")
	}
	for _, char := range color[1:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return errors.New("颜色必须是 #RRGGBB 格式")
		}
	}
	return nil
}

func validateFieldType(fieldType string) error {
	switch fieldType {
	case "text", "long_text", "number", "select", "multi_select", "date", "checkbox", "url":
		return nil
	default:
		return fmt.Errorf("不支持的字段类型: %s", fieldType)
	}
}

func normalizeOptions(options []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		key := strings.ToLower(option)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, option)
	}
	return result
}

func normalizeFieldValue(field FieldDef, raw json.RawMessage) ([]byte, any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, errors.New("字段值不能为空")
	}
	switch field.Type {
	case "text", "long_text":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, errors.New("文本字段值格式错误")
		}
		encoded, _ := json.Marshal(value)
		return encoded, value, nil
	case "url":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, errors.New("URL 字段值格式错误")
		}
		value = strings.TrimSpace(value)
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, nil, errors.New("请输入完整的 http(s) URL")
		}
		encoded, _ := json.Marshal(value)
		return encoded, value, nil
	case "number":
		var value json.Number
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, errors.New("数字字段值格式错误")
		}
		if _, err := value.Float64(); err != nil {
			return nil, nil, errors.New("数字字段值格式错误")
		}
		encoded, _ := json.Marshal(value)
		return encoded, value, nil
	case "checkbox":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, errors.New("复选框字段值格式错误")
		}
		encoded, _ := json.Marshal(value)
		return encoded, value, nil
	case "date":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, errors.New("日期字段值格式错误")
		}
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return nil, nil, errors.New("日期格式应为 YYYY-MM-DD")
		}
		encoded, _ := json.Marshal(value)
		return encoded, value, nil
	case "select":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, errors.New("单选字段值格式错误")
		}
		if !containsFold(field.Options, value) {
			return nil, nil, errors.New("单选值不在字段选项中")
		}
		encoded, _ := json.Marshal(value)
		return encoded, value, nil
	case "multi_select":
		var values []string
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, nil, errors.New("多选字段值格式错误")
		}
		values = normalizeOptions(values)
		for _, value := range values {
			if !containsFold(field.Options, value) {
				return nil, nil, fmt.Errorf("选项 %q 不在字段选项中", value)
			}
		}
		encoded, _ := json.Marshal(values)
		return encoded, values, nil
	default:
		return nil, nil, fmt.Errorf("不支持的字段类型: %s", field.Type)
	}
}

func containsFold(options []string, value string) bool {
	for _, option := range options {
		if strings.EqualFold(option, value) {
			return true
		}
	}
	return false
}

func normalizeNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func dbTime(t time.Time) string {
	return t.UTC().Format(dbTimeLayout)
}

func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return dbTime(t)
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return dbTime(t)
}

func nullableOptionalTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return dbTime(*t)
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	t, err := time.Parse(dbTimeLayout, value.String)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, value.String)
		if err != nil {
			return nil
		}
	}
	t = t.UTC()
	return &t
}
