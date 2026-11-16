package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xiaoyumuxi/gh-manager/internal/agent"
	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/githubsync"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
)

type cachedDetail struct {
	detail githubsync.Detail
	at     time.Time
}

const maxGitHubBody = 65000

func (s *Server) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/environment", s.handleEnvironment)
	mux.HandleFunc("GET /api/agent/skills", s.handleSkills)
	mux.HandleFunc("GET /api/actions", s.handleListActions)
	mux.HandleFunc("POST /api/actions", s.handleCreateAction)
	mux.HandleFunc("PATCH /api/actions/{id}", s.handleUpdateAction)
	mux.HandleFunc("DELETE /api/actions/{id}", s.handleDeleteAction)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("POST /api/runs", s.handleSubmitRuns)
	mux.HandleFunc("GET /api/runs/stream", s.handleRunStream)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/runs/{id}/events", s.handleRunEvents)
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.handleCancelRun)
	mux.HandleFunc("GET /api/runs/{id}/diff", s.handleRunDiff)
	mux.HandleFunc("POST /api/runs/{id}/discard", s.handleDiscardRun)
	mux.HandleFunc("POST /api/runs/{id}/apply", s.handleApplyRun)
	mux.HandleFunc("DELETE /api/runs/{id}", s.handleDeleteRun)
	mux.HandleFunc("GET /api/items/{id}/detail", s.handleItemDetail)
	mux.HandleFunc("POST /api/items/{id}/publish", s.handlePublish)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("GET /api/github/repos", s.handleListGitHubRepos)
	mux.HandleFunc("POST /api/repos/batch", s.handleBatchAddRepos)
}

func (s *Server) handleEnvironment(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1"
	clis := agent.DetectCLIs(r.Context(), s.agentSettings(), refresh)
	response := map[string]any{
		"clis":          clis,
		"workspace_dir": s.agents.WorkspaceRoot(),
		"runs_dir":      s.paths.RunsDir,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if viewer, err := githubsync.GetViewer(ctx); err == nil {
		response["viewer"] = viewer
	} else {
		response["viewer_error"] = "无法读取 GitHub 账号，请先在终端执行 gh auth login：" + err.Error()
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	repo := config.NormalizeRepoName(r.URL.Query().Get("repo"))
	if repo != "" {
		if _, _, err := config.SplitRepo(repo); err != nil {
			writeError(w, http.StatusBadRequest, "无效仓库名")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	skills, err := s.agents.Skills(ctx, repo, r.URL.Query().Get("refresh") == "1")
	response := map[string]any{"skills": skills}
	if err != nil {
		response["repo_error"] = "读取仓库 SKILL 失败：" + err.Error()
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleListActions(w http.ResponseWriter, r *http.Request) {
	actions, err := s.store.ListActions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, actions)
}

func (s *Server) handleCreateAction(w http.ResponseWriter, r *http.Request) {
	var input store.ActionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	action, err := s.store.CreateAction(r.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, action)
}

func (s *Server) handleUpdateAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var input store.ActionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	action, err := s.store.UpdateAction(r.Context(), id, input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, action)
}

func (s *Server) handleDeleteAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.store.DeleteAction(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	var itemID int64
	if value := r.URL.Query().Get("item_id"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "无效的条目 ID")
			return
		}
		itemID = parsed
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.store.ListRuns(r.Context(), itemID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleSubmitRuns(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ItemIDs     []int64 `json:"item_ids"`
		ActionID    int64   `json:"action_id"`
		ExtraPrompt string  `json:"extra_prompt"`
		Skill       *string `json:"skill"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	results, err := s.agents.Submit(r.Context(), agent.SubmitRequest{
		ItemIDs: payload.ItemIDs, ActionID: payload.ActionID, ExtraPrompt: payload.ExtraPrompt, Skill: payload.Skill,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"results": results})
}

type runResponse struct {
	store.AgentRun
	ResumeCommand string `json:"resume_command"`
	Live          bool   `json:"live"`
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	response := runResponse{AgentRun: run, Live: s.agents.IsLive(id)}
	if !run.Discarded {
		response.ResumeCommand = agent.ResumeCommand(run.Agent, run.WorkDir, run.SessionID)
	}
	writeJSON(w, http.StatusOK, response)
}

func startSSE(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前环境不支持实时推送")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return flusher, true
}

func writeSSE(w http.ResponseWriter, event string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.store.GetRun(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	backlog, events, unsubscribe := s.agents.Subscribe(id)
	defer unsubscribe()
	flusher, ok := startSSE(w)
	if !ok {
		return
	}
	for _, event := range backlog {
		if writeSSE(w, "", event) != nil {
			return
		}
	}
	flusher.Flush()
	if events == nil {
		_ = writeSSE(w, "done", map[string]bool{"done": true})
		flusher.Flush()
		return
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, open := <-events:
			if !open {
				// 任务结束或订阅被断开；前端收到 done 后停止重连，否则自动重连补齐。
				if !s.agents.IsLive(id) {
					_ = writeSSE(w, "done", map[string]bool{"done": true})
				}
				flusher.Flush()
				return
			}
			if writeSSE(w, "", event) != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.streamsDone:
			return
		}
	}
}

func (s *Server) handleRunStream(w http.ResponseWriter, r *http.Request) {
	changes, unsubscribe := s.agents.Watch()
	defer unsubscribe()
	flusher, ok := startSSE(w)
	if !ok {
		return
	}
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case change, open := <-changes:
			if !open {
				return
			}
			if writeSSE(w, "", change) != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.streamsDone:
			return
		}
	}
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.agents.Cancel(id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRunDiff(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	diff, err := s.agents.Diff(ctx, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) handleDiscardRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.agents.Discard(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.agents.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleApplyRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var payload struct {
		CommitMessage string `json:"commit_message"`
		PRTitle       string `json:"pr_title"`
		PRBody        string `json:"pr_body"`
		Draft         bool   `json:"draft"`
		Confirm       bool   `json:"confirm"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	if !payload.Confirm {
		writeError(w, http.StatusBadRequest, "推送改动前需要在网页中确认")
		return
	}
	if len(payload.CommitMessage) > 10000 || len(payload.PRTitle) > 300 || len(payload.PRBody) > maxGitHubBody {
		writeError(w, http.StatusBadRequest, "提交说明或 PR 内容过长")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	result, err := s.agents.Apply(ctx, id, agent.ApplyRequest{
		CommitMessage: payload.CommitMessage, PRTitle: payload.PRTitle, PRBody: payload.PRBody, Draft: payload.Draft,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if run, err := s.store.GetRun(r.Context(), id); err == nil {
		s.forgetDetail(run.ItemID)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) forgetDetail(itemID int64) {
	s.detailMu.Lock()
	delete(s.detailCache, itemID)
	s.detailMu.Unlock()
}

func (s *Server) handleItemDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	item, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if r.URL.Query().Get("refresh") != "1" {
		s.detailMu.Lock()
		cached, ok := s.detailCache[id]
		s.detailMu.Unlock()
		if ok && time.Since(cached.at) < 60*time.Second {
			writeJSON(w, http.StatusOK, cached.detail)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	detail, err := githubsync.FetchDetail(ctx, item.Repo, item.Kind, item.Number)
	if err != nil {
		writeError(w, http.StatusBadGateway, "从 GitHub 读取详情失败："+err.Error())
		return
	}
	s.detailMu.Lock()
	if len(s.detailCache) > 200 {
		s.detailCache = map[int64]cachedDetail{}
	}
	s.detailCache[id] = cachedDetail{detail: detail, at: time.Now()}
	s.detailMu.Unlock()
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var payload struct {
		Mode    string `json:"mode"`
		Event   string `json:"event"`
		Body    string `json:"body"`
		RunID   int64  `json:"run_id"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	if !payload.Confirm {
		writeError(w, http.StatusBadRequest, "发布前需要在网页中确认")
		return
	}
	body := strings.TrimSpace(payload.Body)
	if body == "" && payload.Event != "APPROVE" {
		writeError(w, http.StatusBadRequest, "内容不能为空")
		return
	}
	if utf8.RuneCountInString(body) > maxGitHubBody {
		writeError(w, http.StatusBadRequest, "内容超过 GitHub 长度限制")
		return
	}
	item, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if payload.RunID > 0 {
		run, err := s.store.GetRun(r.Context(), payload.RunID)
		if err != nil || run.ItemID != item.ID {
			writeError(w, http.StatusBadRequest, "任务与条目不匹配")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	var url string
	switch payload.Mode {
	case "comment", "":
		url, err = githubsync.PostComment(ctx, item.Repo, item.Number, body)
	case "review":
		if item.Kind != "pr" {
			writeError(w, http.StatusBadRequest, "只有 Pull Request 可以提交评审")
			return
		}
		event := strings.ToUpper(strings.TrimSpace(payload.Event))
		if event == "" {
			event = "COMMENT"
		}
		if event != "COMMENT" && event != "APPROVE" && event != "REQUEST_CHANGES" {
			writeError(w, http.StatusBadRequest, "不支持的评审类型")
			return
		}
		url, err = githubsync.PostReview(ctx, item.Repo, item.Number, body, event, "")
	default:
		writeError(w, http.StatusBadRequest, "不支持的发布方式")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "发布到 GitHub 失败："+err.Error())
		return
	}
	if payload.RunID > 0 {
		now := store.Now()
		_ = s.store.UpdateRun(r.Context(), payload.RunID, store.RunUpdate{PublishedAt: &now, PublishedURL: &url})
	}
	s.forgetDetail(item.ID)
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

type settingsPayload struct {
	Sync         config.SyncConfig  `json:"sync"`
	Agent        config.AgentConfig `json:"agent"`
	Port         int                `json:"port"`
	ConfigPath   string             `json:"config_path"`
	WorkspaceDir string             `json:"effective_workspace_dir"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	payload := settingsPayload{
		Sync: s.config.Sync, Agent: s.config.Agent, Port: s.config.Server.Port,
		ConfigPath: s.paths.ConfigFile, WorkspaceDir: s.agents.WorkspaceRoot(),
	}
	s.configMu.RUnlock()
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Sync  *config.SyncConfig  `json:"sync"`
		Agent *config.AgentConfig `json:"agent"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	for _, command := range []*config.AgentConfig{payload.Agent} {
		if command == nil {
			continue
		}
		for _, value := range []string{command.ClaudeCommand, command.CodexCommand} {
			if strings.ContainsAny(value, "\r\n") {
				writeError(w, http.StatusBadRequest, "命令路径不能包含换行")
				return
			}
		}
	}
	s.configMu.Lock()
	next := cloneConfig(s.config)
	if payload.Sync != nil {
		next.Sync = *payload.Sync
	}
	if payload.Agent != nil {
		next.Agent = *payload.Agent
	}
	if err := config.Save(s.paths.ConfigFile, next); err != nil {
		s.configMu.Unlock()
		writeError(w, http.StatusBadRequest, "保存设置失败："+err.Error())
		return
	}
	s.config = next
	s.configMu.Unlock()
	s.agents.SettingsChanged()
	s.handleGetSettings(w, r)
}

func (s *Server) handleListGitHubRepos(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	if owner == "" || strings.ContainsAny(owner, "/ \t") {
		writeError(w, http.StatusBadRequest, "请输入组织或用户名")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	repos, err := githubsync.ListRepositories(ctx, owner)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取仓库列表失败："+err.Error())
		return
	}
	s.configMu.RLock()
	managed := make([]string, 0, len(s.config.Repos))
	for _, repo := range s.config.Repos {
		managed = append(managed, repo.Name)
	}
	s.configMu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos, "managed": managed})
}

func (s *Server) handleBatchAddRepos(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Repos []string `json:"repos"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	if len(payload.Repos) == 0 || len(payload.Repos) > 500 {
		writeError(w, http.StatusBadRequest, "请选择 1-500 个仓库")
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.syncIsRunning() {
		writeError(w, http.StatusConflict, "同步进行中，暂时不能修改仓库")
		return
	}
	next := cloneConfig(s.config)
	added := 0
	for _, value := range payload.Repos {
		name := config.NormalizeRepoName(value)
		if _, _, err := config.SplitRepo(name); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("无效仓库名: %q", value))
			return
		}
		if findRepoIndex(next.Repos, name) >= 0 {
			continue
		}
		next.Repos = append(next.Repos, config.RepoConfig{Name: name})
		added++
	}
	if added > 0 {
		if err := config.Save(s.paths.ConfigFile, next); err != nil {
			writeError(w, http.StatusInternalServerError, "保存仓库配置失败："+err.Error())
			return
		}
		s.config = next
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": added})
}
