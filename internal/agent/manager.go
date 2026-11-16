package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xiaoyumuxi/gh-manager/internal/githubsync"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
)

// Settings 是 Agent 运行时配置，每次使用时通过回调读取，网页中修改后立即生效。
type Settings struct {
	ClaudeCommand string
	CodexCommand  string
	ClaudeModel   string
	CodexModel    string
	MaxConcurrent int
	Timeout       time.Duration
}

const (
	maxBacklog   = 4000
	maxDiffBytes = 600 << 10
)

// Manager 负责排队、执行、记录 Agent 任务，并在用户确认后提交改动。
type Manager struct {
	store     *store.Store
	settings  func() Settings
	workspace *Workspace
	runsDir   string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	active   int
	changed  chan struct{}
	live     map[int64]*liveRun
	watchers map[chan RunChange]struct{}
	applying map[int64]bool

	skillsMu    sync.Mutex
	skillsCache map[string]cachedSkills
}

type cachedSkills struct {
	skills []Skill
	err    error
	at     time.Time
}

// RunChange 在任务状态变化时广播，用于刷新列表。
type RunChange struct {
	RunID  int64  `json:"run_id"`
	ItemID int64  `json:"item_id"`
	Status string `json:"status"`
}

type liveRun struct {
	mu     sync.Mutex
	seq    int
	events []Event
	subs   map[chan Event]struct{}
	done   bool
	cancel context.CancelFunc
	file   *os.File
}

// SubmitRequest 描述一次批量提交。
type SubmitRequest struct {
	ItemIDs     []int64
	ActionID    int64
	ExtraPrompt string
	Skill       *string
}

// SubmitResult 是单个条目的提交结果。
type SubmitResult struct {
	ItemID int64           `json:"item_id"`
	Run    *store.AgentRun `json:"run,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func NewManager(st *store.Store, workspaceDir, runsDir string, settings func() Settings) (*Manager, error) {
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建运行日志目录失败: %w", err)
	}
	workspace, err := NewWorkspace(workspaceDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		store:       st,
		settings:    settings,
		workspace:   workspace,
		runsDir:     runsDir,
		ctx:         ctx,
		cancel:      cancel,
		changed:     make(chan struct{}),
		live:        map[int64]*liveRun{},
		watchers:    map[chan RunChange]struct{}{},
		applying:    map[int64]bool{},
		skillsCache: map[string]cachedSkills{},
	}, nil
}

// Close 取消所有任务并等待退出。
func (m *Manager) Close() {
	m.cancel()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
	}
}

func (m *Manager) WorkspaceRoot() string { return m.workspace.Root() }

// SettingsChanged 唤醒排队中的任务，使新的并发上限立即生效。
func (m *Manager) SettingsChanged() {
	m.mu.Lock()
	close(m.changed)
	m.changed = make(chan struct{})
	m.mu.Unlock()
}

func (m *Manager) acquire(ctx context.Context) error {
	for {
		m.mu.Lock()
		limit := m.settings().MaxConcurrent
		if limit <= 0 {
			limit = 1
		}
		if m.active < limit {
			m.active++
			m.mu.Unlock()
			return nil
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *Manager) release() {
	m.mu.Lock()
	m.active--
	close(m.changed)
	m.changed = make(chan struct{})
	m.mu.Unlock()
}

// Watch 订阅所有任务的状态变化。
func (m *Manager) Watch() (<-chan RunChange, func()) {
	ch := make(chan RunChange, 64)
	m.mu.Lock()
	m.watchers[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		if _, ok := m.watchers[ch]; ok {
			delete(m.watchers, ch)
			close(ch)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) broadcast(change RunChange) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.watchers {
		select {
		case ch <- change:
		default:
		}
	}
}

func (m *Manager) runDir(id int64) string {
	return filepath.Join(m.runsDir, fmt.Sprint(id))
}

// CommandFor 返回 Agent 对应的命令路径。
func (m *Manager) CommandFor(agentName string) (string, error) {
	settings := m.settings()
	switch agentName {
	case "claude":
		path, err := ResolveCommand(settings.ClaudeCommand, "claude")
		if err != nil {
			return "", errors.New("未找到 Claude Code CLI（claude），请安装或在设置中填写路径")
		}
		return path, nil
	case "codex":
		path, err := ResolveCommand(settings.CodexCommand, "codex")
		if err != nil {
			return "", errors.New("未找到 Codex CLI（codex），请安装或在设置中填写路径")
		}
		return path, nil
	}
	return "", fmt.Errorf("不支持的 Agent: %s", agentName)
}

// Submit 为每个条目创建任务并在后台执行。
func (m *Manager) Submit(ctx context.Context, request SubmitRequest) ([]SubmitResult, error) {
	if len(request.ItemIDs) == 0 {
		return nil, errors.New("请选择至少一个条目")
	}
	if len(request.ItemIDs) > 50 {
		return nil, errors.New("一次最多提交 50 个条目")
	}
	if len(request.ExtraPrompt) > 20000 {
		return nil, errors.New("补充要求过长")
	}
	action, err := m.store.GetAction(ctx, request.ActionID)
	if err != nil {
		return nil, err
	}
	command, err := m.CommandFor(action.Agent)
	if err != nil {
		return nil, err
	}
	skill := action.Skill
	if request.Skill != nil {
		skill = strings.TrimLeft(strings.TrimSpace(*request.Skill), "/$")
		if strings.ContainsAny(skill, " \t\r\n") {
			return nil, errors.New("SKILL 名称不能包含空白")
		}
	}
	model := action.Model
	if model == "" {
		settings := m.settings()
		if action.Agent == "claude" {
			model = settings.ClaudeModel
		} else {
			model = settings.CodexModel
		}
	}

	results := make([]SubmitResult, 0, len(request.ItemIDs))
	for _, itemID := range request.ItemIDs {
		result := SubmitResult{ItemID: itemID}
		item, err := m.store.GetItem(ctx, itemID)
		if err != nil {
			result.Error = "条目不存在"
			results = append(results, result)
			continue
		}
		if action.AppliesTo != "all" && action.AppliesTo != item.Kind {
			result.Error = fmt.Sprintf("%s#%d：该操作只适用于 %s", item.Repo, item.Number, kindLabel(action.AppliesTo))
			results = append(results, result)
			continue
		}
		run, err := m.store.CreateRun(ctx, store.AgentRun{
			ItemID: item.ID, Repo: item.Repo, Number: item.Number, Kind: item.Kind, Title: item.Title,
			ActionID: action.ID, ActionName: action.Name, Agent: action.Agent, Mode: action.Mode,
			Skill: skill, Model: model,
		})
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		live, err := m.startLive(run.ID)
		if err != nil {
			status, message := "failed", err.Error()
			_ = m.store.UpdateRun(ctx, run.ID, store.RunUpdate{Status: &status, Error: &message})
			result.Error = message
			results = append(results, result)
			continue
		}
		result.Run = &run
		results = append(results, result)
		m.broadcast(RunChange{RunID: run.ID, ItemID: run.ItemID, Status: run.Status})
		m.wg.Add(1)
		go m.execute(live, run, item, action, command, request.ExtraPrompt)
	}
	return results, nil
}

func (m *Manager) startLive(id int64) (*liveRun, error) {
	dir := m.runDir(id)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	live := &liveRun{subs: map[chan Event]struct{}{}, file: file}
	m.mu.Lock()
	m.live[id] = live
	m.mu.Unlock()
	return live, nil
}

func (live *liveRun) emit(event Event) {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.done {
		return
	}
	live.seq++
	event.Seq = live.seq
	event.Time = time.Now().UnixMilli()
	live.events = append(live.events, event)
	if len(live.events) > maxBacklog {
		live.events = live.events[len(live.events)-maxBacklog:]
	}
	if data, err := json.Marshal(event); err == nil {
		_, _ = live.file.Write(append(data, '\n'))
	}
	for ch := range live.subs {
		select {
		case ch <- event:
		default:
			// 订阅方处理不过来时断开，前端会自动重连并按 seq 去重。
			delete(live.subs, ch)
			close(ch)
		}
	}
}

func (live *liveRun) finish() {
	live.mu.Lock()
	defer live.mu.Unlock()
	live.done = true
	for ch := range live.subs {
		close(ch)
	}
	live.subs = map[chan Event]struct{}{}
	_ = live.file.Close()
}

// Subscribe 返回已有事件和后续事件通道。任务已结束时从日志文件读取，通道为 nil。
func (m *Manager) Subscribe(id int64) ([]Event, <-chan Event, func()) {
	m.mu.Lock()
	live := m.live[id]
	m.mu.Unlock()
	if live != nil {
		live.mu.Lock()
		if !live.done {
			backlog := append([]Event(nil), live.events...)
			ch := make(chan Event, 512)
			live.subs[ch] = struct{}{}
			live.mu.Unlock()
			return backlog, ch, func() {
				live.mu.Lock()
				if _, ok := live.subs[ch]; ok {
					delete(live.subs, ch)
					close(ch)
				}
				live.mu.Unlock()
			}
		}
		live.mu.Unlock()
	}
	return m.readEvents(id), nil, func() {}
}

func (m *Manager) readEvents(id int64) []Event {
	file, err := os.Open(filepath.Join(m.runDir(id), "events.jsonl"))
	if err != nil {
		return nil
	}
	defer file.Close()
	events := make([]Event, 0, 256)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
		}
	}
	if len(events) > maxBacklog {
		events = events[len(events)-maxBacklog:]
	}
	return events
}

func (m *Manager) setStatus(run *store.AgentRun, live *liveRun, status string, update store.RunUpdate) {
	update.Status = &status
	if err := m.store.UpdateRun(context.Background(), run.ID, update); err != nil {
		live.emit(Event{Type: "error", Text: "保存任务状态失败: " + err.Error()})
	}
	run.Status = status
	live.emit(Event{Type: "state", Text: status})
	m.broadcast(RunChange{RunID: run.ID, ItemID: run.ItemID, Status: status})
}

func (m *Manager) execute(live *liveRun, run store.AgentRun, item store.Item, action store.Action, command, extraPrompt string) {
	defer m.wg.Done()
	ctx, cancel := context.WithCancel(m.ctx)
	live.mu.Lock()
	live.cancel = cancel
	live.mu.Unlock()
	defer cancel()

	finalStatus, finalUpdate := m.runSteps(ctx, live, &run, item, action, command, extraPrompt)
	finished := store.Now()
	finalUpdate.FinishedAt = &finished
	m.setStatus(&run, live, finalStatus, finalUpdate)
	live.emit(Event{Type: "end", Text: finalStatus})
	live.finish()
	m.mu.Lock()
	delete(m.live, run.ID)
	m.mu.Unlock()
}

func failure(err error) (string, store.RunUpdate) {
	if errors.Is(err, context.Canceled) {
		message := "已取消"
		return "canceled", store.RunUpdate{Error: &message}
	}
	message := err.Error()
	return "failed", store.RunUpdate{Error: &message}
}

func (m *Manager) runSteps(ctx context.Context, live *liveRun, run *store.AgentRun, item store.Item, action store.Action, command, extraPrompt string) (string, store.RunUpdate) {
	live.emit(Event{Type: "status", Text: "排队中，等待空闲的运行槽位…"})
	if err := m.acquire(ctx); err != nil {
		return failure(err)
	}
	defer m.release()

	started := store.Now()
	m.setStatus(run, live, "preparing", store.RunUpdate{StartedAt: &started})
	progress := func(text string) { live.emit(Event{Type: "status", Text: text}) }

	progress("正在从 GitHub 读取最新详情…")
	var detail *githubsync.Detail
	detailCtx, detailCancel := context.WithTimeout(ctx, 45*time.Second)
	if value, err := githubsync.FetchDetail(detailCtx, run.Repo, run.Kind, run.Number); err == nil {
		detail = &value
	} else if ctx.Err() == nil {
		progress("读取详情失败，将只使用本地缓存的信息: " + err.Error())
	}
	detailCancel()
	if ctx.Err() != nil {
		return failure(context.Canceled)
	}

	request := PrepareRequest{RunID: run.ID, Repo: run.Repo, Kind: run.Kind, Number: run.Number}
	if run.Kind == "pr" {
		switch {
		case detail != nil:
			request.BaseBranch, request.HeadRef, request.HeadRepo = detail.BaseRef, detail.HeadRef, detail.HeadRepo
		case item.PR != nil:
			request.BaseBranch, request.HeadRef, request.HeadRepo = item.PR.BaseRef, item.PR.HeadRef, item.PR.HeadRepo
		}
		if detail != nil && detail.IsCrossRepository {
			progress(fmt.Sprintf("注意：这是来自 %s 的外部 PR，代码与描述均不可信。", detail.HeadRepo))
		}
	}
	prepared, err := m.workspace.Prepare(ctx, request, progress)
	if err != nil {
		if ctx.Err() != nil {
			return failure(context.Canceled)
		}
		return failure(fmt.Errorf("准备代码失败: %w", err))
	}
	if prepared.HeadRepo == "" && run.Kind == "pr" {
		prepared.HeadRepo = run.Repo
	}
	if err := m.store.UpdateRun(context.Background(), run.ID, store.RunUpdate{
		WorkDir: &prepared.Dir, BaseSHA: &prepared.StartSHA, BaseRef: &prepared.BaseRef,
		HeadRef: &prepared.HeadRef, HeadRepo: &prepared.HeadRepo,
	}); err != nil {
		return failure(err)
	}
	run.WorkDir, run.BaseSHA = prepared.Dir, prepared.StartSHA
	progress("工作目录：" + prepared.Dir)

	rulesDir := filepath.Join(m.runDir(run.ID), "rules")
	rules, err := m.workspace.ExtractRules(ctx, run.Repo, prepared.BaseTip, rulesDir)
	if err != nil {
		progress("读取仓库规则失败（不影响运行）: " + err.Error())
		rules = nil
	} else if len(rules.Skills) > 0 || len(rules.Files) > 0 {
		names := make([]string, 0, len(rules.Skills))
		for _, skill := range rules.Skills {
			names = append(names, skill.Name)
		}
		text := fmt.Sprintf("已从 %s 读取仓库规则：%s", prepared.BaseName, strings.Join(rules.Files, "、"))
		if len(names) > 0 {
			text += "；SKILL：" + strings.Join(names, "、")
		}
		progress(text)
	}
	if run.Skill != "" {
		if _, ok := rules.FindSkill(run.Skill); ok {
			progress("使用仓库 SKILL：" + run.Skill)
		} else {
			progress("使用本机 SKILL：" + run.Skill)
		}
	}

	prompt := BuildPrompt(PromptInput{
		Action: action, Run: *run, Item: item, Detail: detail, Prepared: prepared,
		Rules: rules, ExtraPrompt: extraPrompt,
	})
	m.setStatus(run, live, "running", store.RunUpdate{Prompt: &prompt})

	raw, err := os.Create(filepath.Join(m.runDir(run.ID), "raw.jsonl"))
	if err != nil {
		return failure(err)
	}
	defer raw.Close()
	settings := m.settings()
	invocation := Invocation{
		Agent: run.Agent, Mode: run.Mode, Command: command, Model: run.Model, Prompt: prompt,
		Dir: prepared.Dir, AllowedTools: action.AllowedTools,
		LastMessage: filepath.Join(m.runDir(run.ID), "last.md"), Timeout: settings.Timeout,
	}
	if rules != nil && (len(rules.Files) > 0 || len(rules.Skills) > 0) {
		invocation.AddDirs = []string{rulesDir}
	}
	progress(fmt.Sprintf("正在运行 %s…", agentLabel(run.Agent)))
	outcome, runErr := execute(ctx, invocation, raw, live.emit, func(sessionID string) {
		_ = m.store.UpdateRun(context.Background(), run.ID, store.RunUpdate{SessionID: &sessionID})
	})

	update := store.RunUpdate{}
	if outcome.SessionID != "" {
		update.SessionID = &outcome.SessionID
	}
	if outcome.Result != "" {
		update.Result = &outcome.Result
	}
	if outcome.CostUSD > 0 {
		update.CostUSD = &outcome.CostUSD
	}
	if outcome.DurationMS > 0 {
		update.DurationMS = &outcome.DurationMS
	}
	if run.Mode == "edit" {
		diffCtx, diffCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		stat, _, _, diffErr := m.workspace.Diff(diffCtx, prepared.Dir, prepared.StartSHA, 1)
		diffCancel()
		if diffErr == nil {
			hasChanges := strings.TrimSpace(stat) != ""
			update.DiffStat = &stat
			update.HasChanges = &hasChanges
			if hasChanges {
				live.emit(Event{Type: "status", Text: "检测到代码改动：\n" + stat})
			} else {
				live.emit(Event{Type: "status", Text: "没有产生代码改动。"})
			}
		} else {
			live.emit(Event{Type: "error", Text: "统计改动失败: " + diffErr.Error()})
		}
	}
	if runErr != nil {
		status, failed := failure(runErr)
		failed.SessionID, failed.Result, failed.CostUSD, failed.DurationMS = update.SessionID, update.Result, update.CostUSD, update.DurationMS
		failed.DiffStat, failed.HasChanges = update.DiffStat, update.HasChanges
		return status, failed
	}
	empty := ""
	update.Error = &empty
	return "succeeded", update
}

func agentLabel(agentName string) string {
	if agentName == "codex" {
		return "Codex"
	}
	return "Claude Code"
}

// Cancel 取消排队中或运行中的任务。
func (m *Manager) Cancel(id int64) error {
	m.mu.Lock()
	live := m.live[id]
	m.mu.Unlock()
	if live == nil {
		return errors.New("任务未在运行")
	}
	live.mu.Lock()
	cancel := live.cancel
	live.mu.Unlock()
	if cancel == nil {
		return errors.New("任务正在启动，请稍后重试")
	}
	live.emit(Event{Type: "status", Text: "正在取消…"})
	cancel()
	return nil
}

// IsLive 判断任务是否仍由当前进程执行。
func (m *Manager) IsLive(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.live[id]
	return ok
}

// Diff 返回编辑类任务产生的改动。
func (m *Manager) Diff(ctx context.Context, id int64) (map[string]any, error) {
	run, err := m.store.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if run.WorkDir == "" || run.BaseSHA == "" {
		return nil, errors.New("任务没有工作目录")
	}
	if run.Discarded {
		return nil, errors.New("改动已丢弃")
	}
	if store.IsActiveStatus(run.Status) {
		return nil, errors.New("任务仍在运行")
	}
	stat, patch, truncated, err := m.workspace.Diff(ctx, run.WorkDir, run.BaseSHA, maxDiffBytes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"stat": stat, "patch": patch, "truncated": truncated}, nil
}

// Discard 删除任务的工作目录。
func (m *Manager) Discard(ctx context.Context, id int64) error {
	run, err := m.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if store.IsActiveStatus(run.Status) {
		return errors.New("任务仍在运行，请先取消")
	}
	if err := m.workspace.Remove(ctx, run.Repo, run.WorkDir); err != nil {
		return err
	}
	discarded := true
	return m.store.UpdateRun(ctx, id, store.RunUpdate{Discarded: &discarded})
}

// Delete 删除任务记录、日志和工作目录。
func (m *Manager) Delete(ctx context.Context, id int64) error {
	run, err := m.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if store.IsActiveStatus(run.Status) {
		return errors.New("任务仍在运行，请先取消")
	}
	if !run.Discarded && run.WorkDir != "" {
		_ = m.workspace.Remove(ctx, run.Repo, run.WorkDir)
	}
	if err := m.store.DeleteRun(ctx, id); err != nil {
		return err
	}
	_ = os.RemoveAll(m.runDir(id))
	m.broadcast(RunChange{RunID: id, ItemID: run.ItemID, Status: "deleted"})
	return nil
}

// ApplyRequest 是用户在确认对话框中填写的提交信息。
type ApplyRequest struct {
	CommitMessage string
	PRTitle       string
	PRBody        string
	Draft         bool
}

// ApplyResult 是提交结果；Warning 非空表示部分成功。
type ApplyResult struct {
	URL     string `json:"url"`
	Warning string `json:"warning,omitempty"`
}

// Apply 把编辑类任务的改动提交并推送到 GitHub。调用方必须已经取得用户确认。
func (m *Manager) Apply(ctx context.Context, id int64, request ApplyRequest) (ApplyResult, error) {
	url, warning, err := m.apply(ctx, id, request)
	return ApplyResult{URL: url, Warning: warning}, err
}

func (m *Manager) apply(ctx context.Context, id int64, request ApplyRequest) (string, string, error) {
	m.mu.Lock()
	if m.applying[id] {
		m.mu.Unlock()
		return "", "", errors.New("正在提交，请勿重复操作")
	}
	m.applying[id] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.applying, id)
		m.mu.Unlock()
	}()

	run, err := m.store.GetRun(ctx, id)
	if err != nil {
		return "", "", err
	}
	switch {
	case run.Mode != "edit":
		return "", "", errors.New("只读任务没有可提交的改动")
	case store.IsActiveStatus(run.Status):
		return "", "", errors.New("任务仍在运行")
	case run.AppliedAt != "":
		return "", "", errors.New("改动已经提交过")
	case run.Discarded:
		return "", "", errors.New("改动已丢弃")
	case run.WorkDir == "" || run.BaseSHA == "":
		return "", "", errors.New("任务没有工作目录")
	}
	message := strings.TrimSpace(request.CommitMessage)
	if message == "" {
		return "", "", errors.New("请填写提交说明")
	}
	viewer, err := githubsync.GetViewer(ctx)
	if err != nil {
		return "", "", fmt.Errorf("读取 GitHub 账号失败: %w", err)
	}
	email := fmt.Sprintf("%d+%s@users.noreply.github.com", viewer.DatabaseID, viewer.Login)
	name := viewer.Name
	if name == "" {
		name = viewer.Login
	}

	var url, warning string
	if run.Kind == "pr" {
		detail, err := githubsync.FetchDetail(ctx, run.Repo, "pr", run.Number)
		if err != nil {
			return "", "", fmt.Errorf("读取 PR 最新状态失败: %w", err)
		}
		switch {
		case !strings.EqualFold(detail.State, "open"):
			return "", "", errors.New("PR 已关闭或已合并，不能推送")
		case detail.HeadOID != run.BaseSHA:
			return "", "", errors.New("PR 分支在 Agent 运行后有新的提交，为避免覆盖已拒绝推送。请重新运行操作")
		case detail.IsCrossRepository && !detail.MaintainerCanModify:
			return "", "", errors.New("这是外部仓库的 PR，且作者未允许维护者修改，无法推送")
		case !detail.IsCrossRepository && !canWrite(detail.ViewerPermission):
			return "", "", errors.New("你对该仓库没有写权限，无法推送")
		}
		sha, err := m.workspace.CommitAll(ctx, run.WorkDir, run.BaseSHA, message, name, email)
		if err != nil {
			return "", "", err
		}
		target := detail.HeadRepo
		if target == "" {
			target = run.Repo
		}
		if err := m.workspace.Push(ctx, run.WorkDir, target, detail.HeadRef); err != nil {
			m.rollbackCommit(run)
			return "", "", err
		}
		url = fmt.Sprintf("https://github.com/%s/pull/%d/commits/%s", run.Repo, run.Number, sha)
	} else {
		title := strings.TrimSpace(request.PRTitle)
		if title == "" {
			return "", "", errors.New("请填写 Pull Request 标题")
		}
		branch := fmt.Sprintf("gh-manager/issue-%d-run%d", run.Number, run.ID)
		if _, err := m.workspace.CommitAll(ctx, run.WorkDir, run.BaseSHA, message, name, email); err != nil {
			return "", "", err
		}
		if err := m.workspace.Push(ctx, run.WorkDir, run.Repo, branch); err != nil {
			m.rollbackCommit(run)
			return "", "", err
		}
		url, err = githubsync.CreatePullRequest(ctx, run.Repo, branch, run.BaseRef, title, request.PRBody, request.Draft)
		if err != nil {
			// 分支已经推送成功，记录为已提交，避免重试时重复推送；用户可以在 GitHub 上手动创建 PR。
			warning = fmt.Sprintf("分支 %s 已推送，但创建 Pull Request 失败：%v。可以打开链接手动创建。", branch, err)
			url = fmt.Sprintf("https://github.com/%s/compare/%s...%s?expand=1", run.Repo, run.BaseRef, branch)
		}
	}
	now := store.Now()
	if err := m.store.UpdateRun(ctx, id, store.RunUpdate{AppliedAt: &now, AppliedURL: &url}); err != nil {
		return url, warning, err
	}
	m.broadcast(RunChange{RunID: id, ItemID: run.ItemID, Status: run.Status})
	return url, warning, nil
}

// rollbackCommit 推送失败时撤销本地提交，保留改动以便重试。
func (m *Manager) rollbackCommit(run store.AgentRun) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = m.workspace.git(ctx, run.WorkDir, "reset", "--soft", run.BaseSHA)
}

func canWrite(permission string) bool {
	switch permission {
	case "ADMIN", "MAINTAIN", "WRITE":
		return true
	}
	return false
}

// Skills 返回本机 SKILL，以及指定仓库（默认分支）中的 SKILL。
func (m *Manager) Skills(ctx context.Context, repo string, refresh bool) ([]Skill, error) {
	skills := DiscoverSkills()
	if repo == "" {
		return skills, nil
	}
	key := strings.ToLower(repo)
	m.skillsMu.Lock()
	cached, ok := m.skillsCache[key]
	m.skillsMu.Unlock()
	if !ok || refresh || time.Since(cached.at) > 5*time.Minute {
		repoSkills, err := m.workspace.ListRepoSkills(ctx, repo)
		cached = cachedSkills{skills: repoSkills, err: err, at: time.Now()}
		m.skillsMu.Lock()
		m.skillsCache[key] = cached
		m.skillsMu.Unlock()
	}
	if cached.err != nil {
		return skills, cached.err
	}
	return append(cached.skills, skills...), nil
}
