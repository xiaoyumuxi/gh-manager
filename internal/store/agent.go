package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	validAgents    = map[string]bool{"claude": true, "codex": true}
	validModes     = map[string]bool{"read": true, "edit": true}
	validAppliesTo = map[string]bool{"all": true, "pr": true, "issue": true}
	activeStatuses = []string{"queued", "preparing", "running"}
)

func seedDefaultActions(ctx context.Context, tx *sql.Tx, now string) error {
	var seeded string
	err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'actions_seeded'`).Scan(&seeded)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for index, action := range defaultActions {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO actions (name, agent, mode, applies_to, prompt, position, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			action.Name, action.Agent, action.Mode, action.AppliesTo, action.Prompt, index, now, now,
		); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('actions_seeded', '1')`)
	return err
}

const actionColumns = `id, name, agent, mode, applies_to, skill, prompt, model, allowed_tools, position, created_at, updated_at`

func scanAction(row rowScanner) (Action, error) {
	var action Action
	err := row.Scan(&action.ID, &action.Name, &action.Agent, &action.Mode, &action.AppliesTo,
		&action.Skill, &action.Prompt, &action.Model, &action.AllowedTools, &action.Position,
		&action.CreatedAt, &action.UpdatedAt)
	return action, err
}

func (s *Store) ListActions(ctx context.Context) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+actionColumns+` FROM actions ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	actions := make([]Action, 0)
	for rows.Next() {
		action, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, rows.Err()
}

func (s *Store) GetAction(ctx context.Context, id int64) (Action, error) {
	action, err := scanAction(s.db.QueryRowContext(ctx, `SELECT `+actionColumns+` FROM actions WHERE id = ?`, id))
	return action, normalizeNotFound(err)
}

func (s *Store) CreateAction(ctx context.Context, input ActionInput) (Action, error) {
	action := Action{Agent: "claude", Mode: "read", AppliesTo: "all"}
	applyActionInput(&action, input)
	if input.Position == nil {
		var maxPosition sql.NullInt64
		_ = s.db.QueryRowContext(ctx, `SELECT MAX(position) FROM actions`).Scan(&maxPosition)
		action.Position = int(maxPosition.Int64) + 1
	}
	if err := validateAction(action); err != nil {
		return Action{}, err
	}
	now := dbTime(time.Now())
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO actions (name, agent, mode, applies_to, skill, prompt, model, allowed_tools, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.Name, action.Agent, action.Mode, action.AppliesTo, action.Skill, action.Prompt,
		action.Model, action.AllowedTools, action.Position, now, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Action{}, errors.New("操作名称已存在")
		}
		return Action{}, err
	}
	id, _ := result.LastInsertId()
	return s.GetAction(ctx, id)
}

func (s *Store) UpdateAction(ctx context.Context, id int64, input ActionInput) (Action, error) {
	action, err := s.GetAction(ctx, id)
	if err != nil {
		return Action{}, err
	}
	applyActionInput(&action, input)
	if err := validateAction(action); err != nil {
		return Action{}, err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE actions SET name = ?, agent = ?, mode = ?, applies_to = ?, skill = ?, prompt = ?,
			model = ?, allowed_tools = ?, position = ?, updated_at = ?
		 WHERE id = ?`,
		action.Name, action.Agent, action.Mode, action.AppliesTo, action.Skill, action.Prompt,
		action.Model, action.AllowedTools, action.Position, dbTime(time.Now()), id,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Action{}, errors.New("操作名称已存在")
		}
		return Action{}, err
	}
	return s.GetAction(ctx, id)
}

func (s *Store) DeleteAction(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM actions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func applyActionInput(action *Action, input ActionInput) {
	if input.Name != nil {
		action.Name = strings.TrimSpace(*input.Name)
	}
	if input.Agent != nil {
		action.Agent = strings.TrimSpace(*input.Agent)
	}
	if input.Mode != nil {
		action.Mode = strings.TrimSpace(*input.Mode)
	}
	if input.AppliesTo != nil {
		action.AppliesTo = strings.TrimSpace(*input.AppliesTo)
	}
	if input.Skill != nil {
		action.Skill = strings.TrimLeft(strings.TrimSpace(*input.Skill), "/$")
	}
	if input.Prompt != nil {
		action.Prompt = strings.TrimSpace(*input.Prompt)
	}
	if input.Model != nil {
		action.Model = strings.TrimSpace(*input.Model)
	}
	if input.AllowedTools != nil {
		action.AllowedTools = strings.TrimSpace(*input.AllowedTools)
	}
	if input.Position != nil {
		action.Position = *input.Position
	}
}

func validateAction(action Action) error {
	if action.Name == "" {
		return errors.New("操作名称不能为空")
	}
	if utf8.RuneCountInString(action.Name) > 40 {
		return errors.New("操作名称不能超过 40 个字符")
	}
	if !validAgents[action.Agent] {
		return fmt.Errorf("不支持的 Agent: %q", action.Agent)
	}
	if !validModes[action.Mode] {
		return fmt.Errorf("不支持的模式: %q", action.Mode)
	}
	if !validAppliesTo[action.AppliesTo] {
		return fmt.Errorf("不支持的适用范围: %q", action.AppliesTo)
	}
	if action.Prompt == "" && action.Skill == "" {
		return errors.New("提示词和 SKILL 至少填写一个")
	}
	if len(action.Prompt) > 20000 {
		return errors.New("提示词过长")
	}
	if strings.ContainsAny(action.Skill, " \t\r\n") {
		return errors.New("SKILL 名称不能包含空白")
	}
	if strings.ContainsAny(action.Model, " \t\r\n") || strings.HasPrefix(action.Model, "-") {
		return errors.New("模型名无效")
	}
	if strings.ContainsAny(action.AllowedTools, "\r\n") {
		return errors.New("允许的工具请写在一行内，用逗号分隔")
	}
	return nil
}

const runColumns = `id, item_id, repo, number, kind, title, action_id, action_name, agent, mode, skill, model,
	prompt, status, error, session_id, result, work_dir, base_sha, base_ref, head_ref, head_repo,
	diff_stat, has_changes, cost_usd, duration_ms, published_at, published_url, applied_at,
	applied_url, discarded, created_at, started_at, finished_at`

func scanRun(row rowScanner) (AgentRun, error) {
	var run AgentRun
	var hasChanges, discarded int
	err := row.Scan(&run.ID, &run.ItemID, &run.Repo, &run.Number, &run.Kind, &run.Title,
		&run.ActionID, &run.ActionName, &run.Agent, &run.Mode, &run.Skill, &run.Model,
		&run.Prompt, &run.Status, &run.Error, &run.SessionID, &run.Result, &run.WorkDir,
		&run.BaseSHA, &run.BaseRef, &run.HeadRef, &run.HeadRepo, &run.DiffStat, &hasChanges,
		&run.CostUSD, &run.DurationMS, &run.PublishedAt, &run.PublishedURL, &run.AppliedAt,
		&run.AppliedURL, &discarded, &run.CreatedAt, &run.StartedAt, &run.FinishedAt)
	run.HasChanges = hasChanges == 1
	run.Discarded = discarded == 1
	return run, err
}

// CreateRun 插入一条排队中的运行记录。同一条目同一操作已有进行中的任务时返回错误。
func (s *Store) CreateRun(ctx context.Context, run AgentRun) (AgentRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRun{}, err
	}
	defer tx.Rollback()
	var existing int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM agent_runs WHERE item_id = ? AND action_id = ? AND status IN ('queued', 'preparing', 'running') LIMIT 1`,
		run.ItemID, run.ActionID,
	).Scan(&existing)
	if err == nil {
		return AgentRun{}, fmt.Errorf("%s#%d 已有相同操作正在运行（任务 #%d）", run.Repo, run.Number, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AgentRun{}, err
	}
	now := dbTime(time.Now())
	result, err := tx.ExecContext(ctx,
		`INSERT INTO agent_runs (item_id, repo, number, kind, title, action_id, action_name, agent, mode,
			skill, model, prompt, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?)`,
		run.ItemID, run.Repo, run.Number, run.Kind, run.Title, run.ActionID, run.ActionName,
		run.Agent, run.Mode, run.Skill, run.Model, run.Prompt, now,
	)
	if err != nil {
		return AgentRun{}, err
	}
	id, _ := result.LastInsertId()
	if err := tx.Commit(); err != nil {
		return AgentRun{}, err
	}
	return s.GetRun(ctx, id)
}

func (s *Store) GetRun(ctx context.Context, id int64) (AgentRun, error) {
	run, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM agent_runs WHERE id = ?`, id))
	return run, normalizeNotFound(err)
}

// ListRuns 返回最近的运行记录；itemID > 0 时只返回该条目的记录。
func (s *Store) ListRuns(ctx context.Context, itemID int64, limit int) ([]AgentRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT ` + runColumns + ` FROM agent_runs`
	args := []any{}
	if itemID > 0 {
		query += ` WHERE item_id = ?`
		args = append(args, itemID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]AgentRun, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		// 列表中不返回完整提示词，减少传输量。
		run.Prompt = ""
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// LatestRunsByItem 返回每个条目最近一次运行的状态，用于表格中的 AI 列。
func (s *Store) LatestRunsByItem(ctx context.Context) (map[int64]AgentRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runColumns+` FROM agent_runs
		 WHERE id IN (SELECT MAX(id) FROM agent_runs GROUP BY item_id)`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := make(map[int64]AgentRun)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		latest[run.ItemID] = AgentRun{
			ID: run.ID, ItemID: run.ItemID, ActionName: run.ActionName, Agent: run.Agent,
			Mode: run.Mode, Status: run.Status, HasChanges: run.HasChanges,
			PublishedAt: run.PublishedAt, AppliedAt: run.AppliedAt, FinishedAt: run.FinishedAt,
			CreatedAt: run.CreatedAt,
		}
	}
	return latest, rows.Err()
}

func (s *Store) CountActiveRuns(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_runs WHERE status IN ('queued', 'preparing', 'running')`,
	).Scan(&count)
	return count, err
}

func (s *Store) UpdateRun(ctx context.Context, id int64, update RunUpdate) error {
	sets := make([]string, 0, 8)
	args := make([]any, 0, 8)
	addString := func(column string, value *string) {
		if value != nil {
			sets = append(sets, column+" = ?")
			args = append(args, *value)
		}
	}
	addBool := func(column string, value *bool) {
		if value != nil {
			sets = append(sets, column+" = ?")
			if *value {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		}
	}
	addString("prompt", update.Prompt)
	addString("status", update.Status)
	addString("error", update.Error)
	addString("session_id", update.SessionID)
	addString("result", update.Result)
	addString("work_dir", update.WorkDir)
	addString("base_sha", update.BaseSHA)
	addString("base_ref", update.BaseRef)
	addString("head_ref", update.HeadRef)
	addString("head_repo", update.HeadRepo)
	addString("diff_stat", update.DiffStat)
	addBool("has_changes", update.HasChanges)
	if update.CostUSD != nil {
		sets = append(sets, "cost_usd = ?")
		args = append(args, *update.CostUSD)
	}
	if update.DurationMS != nil {
		sets = append(sets, "duration_ms = ?")
		args = append(args, *update.DurationMS)
	}
	addString("published_at", update.PublishedAt)
	addString("published_url", update.PublishedURL)
	addString("applied_at", update.AppliedAt)
	addString("applied_url", update.AppliedURL)
	addBool("discarded", update.Discarded)
	addString("started_at", update.StartedAt)
	addString("finished_at", update.FinishedAt)
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRun(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_runs WHERE id = ? AND status NOT IN ('queued', 'preparing', 'running')`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("任务不存在或仍在运行")
	}
	return nil
}

// IsActiveStatus 判断任务是否仍在排队或执行。
func IsActiveStatus(status string) bool {
	for _, active := range activeStatuses {
		if status == active {
			return true
		}
	}
	return false
}

// Now 以数据库格式返回当前时间。
func Now() string {
	return dbTime(time.Now())
}
