package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiaoyumuxi/gh-manager/internal/agent"
	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/githubsync"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
	"github.com/xiaoyumuxi/gh-manager/internal/web"
)

type Options struct {
	Store   *store.Store
	Config  *config.Config
	Paths   config.Paths
	Version string
}

type Server struct {
	store          *store.Store
	config         *config.Config
	paths          config.Paths
	version        string
	sessionToken   string
	allowedOrigins map[string]struct{}
	allowedHosts   map[string]struct{}
	configMu       sync.RWMutex
	syncMu         sync.Mutex
	syncState      SyncStatus
	agents         *agent.Manager
	streamsDone    chan struct{}
	stopOnce       sync.Once
	detailMu       sync.Mutex
	detailCache    map[int64]cachedDetail
}

type SyncStatus struct {
	Running    bool   `json:"running"`
	Message    string `json:"message"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Error      string `json:"error"`
}

type Stats struct {
	Total       int `json:"total"`
	Open        int `json:"open"`
	Closed      int `json:"closed"`
	Issues      int `json:"issues"`
	PRs         int `json:"prs"`
	Stale       int `json:"stale"`
	ReviewStale int `json:"review_stale"`
}

type StateResponse struct {
	Repositories []config.RepoConfig       `json:"repositories"`
	Items        []store.Item              `json:"items"`
	Tags         []store.Tag               `json:"tags"`
	Fields       []store.FieldDef          `json:"fields"`
	Stats        Stats                     `json:"stats"`
	LastSync     *store.SyncRun            `json:"last_sync"`
	RepoStatuses []store.RepoStatus        `json:"repo_statuses"`
	Syncing      bool                      `json:"syncing"`
	SyncStatus   SyncStatus                `json:"sync_status"`
	Workspace    Workspace                 `json:"workspace"`
	Actions      []store.Action            `json:"actions"`
	LatestRuns   map[string]store.AgentRun `json:"latest_runs"`
	ActiveRuns   int                       `json:"active_runs"`
}

type Workspace struct {
	ConfigPath      string `json:"config_path"`
	DBPath          string `json:"db_path"`
	RepoCount       int    `json:"repo_count"`
	StaleDays       int    `json:"stale_days"`
	ReviewStaleDays int    `json:"review_stale_days"`
	Version         string `json:"version"`
}

func New(options Options) (*Server, error) {
	token := randomToken()
	port := options.Config.Server.Port
	server := &Server{
		store:          options.Store,
		config:         options.Config,
		paths:          options.Paths,
		version:        options.Version,
		sessionToken:   token,
		allowedOrigins: map[string]struct{}{},
		allowedHosts:   map[string]struct{}{},
		syncState:      SyncStatus{Message: "尚未同步"},
		streamsDone:    make(chan struct{}),
		detailCache:    map[int64]cachedDetail{},
	}
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		address := fmt.Sprintf("%s:%d", host, port)
		server.allowedHosts[address] = struct{}{}
		server.allowedOrigins["http://"+address] = struct{}{}
	}
	manager, err := agent.NewManager(options.Store, options.Paths.ResolveWorkspaceDir(options.Config), options.Paths.RunsDir, server.agentSettings)
	if err != nil {
		return nil, err
	}
	server.agents = manager
	return server, nil
}

// StopStreams 结束所有长连接（SSE），让 HTTP 服务可以及时关闭。
func (s *Server) StopStreams() {
	s.stopOnce.Do(func() { close(s.streamsDone) })
}

// Close 取消运行中的 Agent 任务。
func (s *Server) Close() {
	s.StopStreams()
	s.agents.Close()
}

func (s *Server) agentSettings() agent.Settings {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	cfg := s.config.Agent
	return agent.Settings{
		ClaudeCommand: cfg.ClaudeCommand,
		CodexCommand:  cfg.CodexCommand,
		ClaudeModel:   cfg.ClaudeModel,
		CodexModel:    cfg.CodexModel,
		MaxConcurrent: cfg.MaxConcurrent,
		Timeout:       time.Duration(cfg.TimeoutMinutes) * time.Minute,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/sync", s.handleStartSync)
	mux.HandleFunc("GET /api/sync/status", s.handleSyncStatus)
	mux.HandleFunc("POST /api/repos", s.handleAddRepo)
	mux.HandleFunc("PATCH /api/repos", s.handleUpdateRepo)
	mux.HandleFunc("DELETE /api/repos", s.handleDeleteRepo)
	mux.HandleFunc("POST /api/tags", s.handleCreateTag)
	mux.HandleFunc("PATCH /api/tags/{id}", s.handleUpdateTag)
	mux.HandleFunc("DELETE /api/tags/{id}", s.handleDeleteTag)
	mux.HandleFunc("PUT /api/items/{id}/tags", s.handleSetItemTags)
	mux.HandleFunc("POST /api/fields", s.handleCreateField)
	mux.HandleFunc("PATCH /api/fields/{id}", s.handleUpdateField)
	mux.HandleFunc("DELETE /api/fields/{id}", s.handleDeleteField)
	mux.HandleFunc("PUT /api/items/{id}/fields/{fieldID}", s.handleSetItemField)
	s.registerAgentRoutes(mux)

	staticFS, err := fs.Sub(web.Assets, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))
	return s.browserGuard(mux)
}

func (s *Server) Addr() string {
	s.configMu.RLock()
	port := s.config.Server.Port
	s.configMu.RUnlock()
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func (s *Server) browserGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只接受本机地址作为 Host，防止 DNS 重绑定攻击借用浏览器访问本服务。
		if _, ok := s.allowedHosts[strings.ToLower(r.Host)]; !ok {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		cookie, err := r.Cookie("gh_manager_session")
		validCookie := err == nil && cookie.Value == s.sessionToken
		if !validCookie {
			http.SetCookie(w, &http.Cookie{
				Name:     "gh_manager_session",
				Value:    s.sessionToken,
				Path:     "/",
				MaxAge:   24 * 60 * 60,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
		}
		if !isUnsafeMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/api/") && !validCookie {
			writeError(w, http.StatusForbidden, "会话已失效，请刷新页面")
			return
		}
		if isUnsafeMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/api/") {
			if !validCookie {
				writeError(w, http.StatusForbidden, "会话已失效，请刷新页面")
				return
			}
			origin := r.Header.Get("Origin")
			if _, ok := s.allowedOrigins[origin]; !ok {
				writeError(w, http.StatusForbidden, "请求来源不受信任")
				return
			}
			if r.Method != http.MethodDelete && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "请求必须使用 application/json")
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

const contentSecurityPolicy = "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	repos := append([]config.RepoConfig(nil), s.config.Repos...)
	staleDays := s.config.Sync.StaleDays
	reviewStaleDays := s.config.Sync.ReviewStaleDays
	s.syncMu.Lock()
	syncState := s.syncState
	s.syncMu.Unlock()
	s.configMu.RUnlock()

	items, err := s.store.ListItems(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tags, err := s.store.ListTags(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fields, err := s.store.ListFields(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lastSync, err := s.store.LatestSyncRun(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	repoStatuses, err := s.store.ListRepoStatuses(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items = filterConfiguredItems(items, repos)
	repoStatuses = mergeRepoStatuses(repos, repoStatuses)
	actions, err := s.store.ListActions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	latest, err := s.store.LatestRunsByItem(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	latestRuns := make(map[string]store.AgentRun, len(latest))
	for itemID, run := range latest {
		latestRuns[strconv.FormatInt(itemID, 10)] = run
	}
	activeRuns, _ := s.store.CountActiveRuns(r.Context())

	now := time.Now().UTC()
	stats := Stats{Total: len(items)}
	for index := range items {
		item := &items[index]
		if item.Kind == "issue" {
			stats.Issues++
		} else {
			stats.PRs++
		}
		if item.State == "open" {
			stats.Open++
		} else {
			stats.Closed++
		}
		reference := item.UpdatedAt
		if reference == nil {
			reference = item.CreatedAt
		}
		if reference != nil && item.State == "open" {
			item.StaleDays = int(now.Sub(*reference).Hours() / 24)
			if item.StaleDays < 0 {
				item.StaleDays = 0
			}
			item.Stale = item.StaleDays >= staleDays
			item.ReviewStale = item.Kind == "pr" &&
				(item.ReviewDecision == "REVIEW_REQUIRED" || len(item.Reviewers) > 0) &&
				item.StaleDays >= reviewStaleDays
			if item.Stale {
				stats.Stale++
			}
			if item.ReviewStale {
				stats.ReviewStale++
			}
		}
	}

	writeJSON(w, http.StatusOK, StateResponse{
		Repositories: repos,
		Items:        items,
		Tags:         tags,
		Fields:       fields,
		Stats:        stats,
		LastSync:     lastSync,
		RepoStatuses: repoStatuses,
		Syncing:      syncState.Running,
		SyncStatus:   syncState,
		Workspace: Workspace{
			ConfigPath:      s.paths.ConfigFile,
			DBPath:          s.paths.DBFile,
			RepoCount:       len(repos),
			StaleDays:       staleDays,
			ReviewStaleDays: reviewStaleDays,
			Version:         s.version,
		},
		Actions:    actions,
		LatestRuns: latestRuns,
		ActiveRuns: activeRuns,
	})
}

func (s *Server) handleStartSync(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	repos := append([]config.RepoConfig(nil), s.config.Repos...)
	pageSize := s.config.Sync.RequestPageSize
	closedWindowDays := s.config.Sync.ClosedWindowDays
	s.syncMu.Lock()
	if s.syncState.Running {
		s.syncMu.Unlock()
		s.configMu.RUnlock()
		writeError(w, http.StatusConflict, "同步正在进行中")
		return
	}
	s.syncState = SyncStatus{
		Running:   true,
		Message:   "正在准备同步",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.syncMu.Unlock()
	s.configMu.RUnlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		_, err := githubsync.Run(ctx, githubsync.SyncOptions{
			Store:            s.store,
			Repos:            repos,
			PageSize:         pageSize,
			ClosedWindowDays: closedWindowDays,
			Progress: func(message string) {
				s.syncMu.Lock()
				s.syncState.Message = message
				s.syncMu.Unlock()
			},
		})
		s.syncMu.Lock()
		s.syncState.Running = false
		s.syncState.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			s.syncState.Error = err.Error()
			s.syncState.Message = "同步未完全成功"
		} else {
			s.syncState.Message = "同步完成"
		}
		s.syncMu.Unlock()
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (s *Server) handleAddRepo(w http.ResponseWriter, r *http.Request) {
	s.handleSaveRepo(w, r, false)
}

func (s *Server) handleUpdateRepo(w http.ResponseWriter, r *http.Request) {
	s.handleSaveRepo(w, r, true)
}

func (s *Server) handleSaveRepo(w http.ResponseWriter, r *http.Request, update bool) {
	var payload struct {
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}

	repoName := config.NormalizeRepoName(payload.Repo)
	branch := config.NormalizeBranchName(payload.Branch)
	if _, _, err := config.SplitRepo(repoName); err != nil {
		writeError(w, http.StatusBadRequest, "请输入 owner/repo 格式的仓库名")
		return
	}
	if err := config.ValidateBranchName(branch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.configMu.RLock()
	existingIndex := findRepoIndex(s.config.Repos, repoName)
	s.configMu.RUnlock()
	if update && existingIndex < 0 {
		writeError(w, http.StatusNotFound, "仓库不在管理列表中")
		return
	}
	if !update && existingIndex >= 0 {
		writeError(w, http.StatusConflict, "该仓库已在管理列表中")
		return
	}
	if s.syncIsRunning() {
		writeError(w, http.StatusConflict, "同步进行中，暂时不能修改仓库")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	head, err := githubsync.GetRepositoryHead(ctx, repoName, branch)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "验证仓库超时，请稍后重试")
			return
		}
		writeError(w, http.StatusBadRequest, "无法验证仓库或分支："+err.Error())
		return
	}

	s.configMu.Lock()
	s.syncMu.Lock()
	if s.syncState.Running {
		s.syncMu.Unlock()
		s.configMu.Unlock()
		writeError(w, http.StatusConflict, "同步进行中，暂时不能修改仓库")
		return
	}

	existingIndex = findRepoIndex(s.config.Repos, repoName)
	if update && existingIndex < 0 {
		s.syncMu.Unlock()
		s.configMu.Unlock()
		writeError(w, http.StatusNotFound, "仓库不在管理列表中")
		return
	}
	if !update && existingIndex >= 0 {
		s.syncMu.Unlock()
		s.configMu.Unlock()
		writeError(w, http.StatusConflict, "该仓库已在管理列表中")
		return
	}

	next := cloneConfig(s.config)
	repoConfig := config.RepoConfig{Name: repoName, Branch: branch}
	if update {
		next.Repos[existingIndex] = repoConfig
	} else {
		next.Repos = append(next.Repos, repoConfig)
	}
	if err := config.Save(s.paths.ConfigFile, next); err != nil {
		s.syncMu.Unlock()
		s.configMu.Unlock()
		writeError(w, http.StatusInternalServerError, "保存仓库配置失败："+err.Error())
		return
	}
	s.config = next
	s.syncMu.Unlock()
	s.configMu.Unlock()

	status := store.RepoResult{
		FullName:          repoName,
		TrackedBranch:     head.Branch,
		CommitSHA:         head.SHA,
		CommitMessage:     head.Message,
		CommitCommittedAt: head.CommittedAt.UTC().Format(time.RFC3339),
		UpdateHead:        true,
	}
	if err := s.store.SaveRepoResult(r.Context(), status); err != nil {
		writeError(w, http.StatusInternalServerError, "仓库已保存，但写入仓库状态失败："+err.Error())
		return
	}

	code := http.StatusCreated
	if update {
		code = http.StatusOK
	}
	writeJSON(w, code, repoConfig)
}

func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	repoName := config.NormalizeRepoName(r.URL.Query().Get("repo"))
	if _, _, err := config.SplitRepo(repoName); err != nil {
		writeError(w, http.StatusBadRequest, "缺少有效的仓库名")
		return
	}

	s.configMu.Lock()
	s.syncMu.Lock()
	if s.syncState.Running {
		s.syncMu.Unlock()
		s.configMu.Unlock()
		writeError(w, http.StatusConflict, "同步进行中，暂时不能修改仓库")
		return
	}
	index := findRepoIndex(s.config.Repos, repoName)
	if index < 0 {
		s.syncMu.Unlock()
		s.configMu.Unlock()
		writeError(w, http.StatusNotFound, "仓库不在管理列表中")
		return
	}
	next := cloneConfig(s.config)
	next.Repos = append(next.Repos[:index], next.Repos[index+1:]...)
	if err := config.Save(s.paths.ConfigFile, next); err != nil {
		s.syncMu.Unlock()
		s.configMu.Unlock()
		writeError(w, http.StatusInternalServerError, "保存仓库配置失败："+err.Error())
		return
	}
	s.config = next
	s.syncMu.Unlock()
	s.configMu.Unlock()

	if err := s.store.DeleteRepoData(r.Context(), repoName); err != nil {
		writeError(w, http.StatusInternalServerError, "仓库已从配置移除，但清理本地数据失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) syncIsRunning() bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncState.Running
}

func cloneConfig(source *config.Config) *config.Config {
	next := *source
	next.Repos = append([]config.RepoConfig(nil), source.Repos...)
	return &next
}

func findRepoIndex(repos []config.RepoConfig, fullName string) int {
	for index, repo := range repos {
		if strings.EqualFold(repo.Name, fullName) {
			return index
		}
	}
	return -1
}

func filterConfiguredItems(items []store.Item, repos []config.RepoConfig) []store.Item {
	configured := make(map[string]struct{}, len(repos))
	for _, repo := range repos {
		configured[strings.ToLower(repo.Name)] = struct{}{}
	}
	filtered := make([]store.Item, 0, len(items))
	for _, item := range items {
		if item.Source != "github" {
			filtered = append(filtered, item)
			continue
		}
		if _, ok := configured[strings.ToLower(item.Repo)]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func mergeRepoStatuses(repos []config.RepoConfig, statuses []store.RepoStatus) []store.RepoStatus {
	byName := make(map[string]store.RepoStatus, len(statuses))
	for _, status := range statuses {
		byName[strings.ToLower(status.FullName)] = status
	}
	merged := make([]store.RepoStatus, 0, len(repos))
	for _, repo := range repos {
		status, ok := byName[strings.ToLower(repo.Name)]
		if !ok {
			status = store.RepoStatus{FullName: repo.Name}
		}
		merged = append(merged, status)
	}
	return merged
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	s.syncMu.Lock()
	status := s.syncState
	s.syncMu.Unlock()
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	tag, err := s.store.CreateTag(r.Context(), payload.Name, payload.Color)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, tag)
}

func (s *Server) handleUpdateTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var payload struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	tag, err := s.store.UpdateTag(r.Context(), id, payload.Name, payload.Color)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tag)
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.store.DeleteTag(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSetItemTags(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var payload struct {
		TagIDs []int64 `json:"tag_ids"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	tags, err := s.store.SetItemTags(r.Context(), itemID, payload.TagIDs)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) handleCreateField(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		Options []string `json:"options"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	field, err := s.store.CreateField(r.Context(), payload.Name, payload.Type, payload.Options)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, field)
}

func (s *Server) handleUpdateField(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var payload struct {
		Name    *string   `json:"name"`
		Options *[]string `json:"options"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	field, err := s.store.UpdateField(r.Context(), id, payload.Name, payload.Options)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, field)
}

func (s *Server) handleDeleteField(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.store.DeleteField(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSetItemField(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	fieldID, ok := pathID(w, r, "fieldID")
	if !ok {
		return
	}
	var payload struct {
		Value json.RawMessage `json:"value"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	trimmed := strings.TrimSpace(string(payload.Value))
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "缺少字段值")
		return
	}
	if trimmed == "null" {
		if err := s.store.ClearItemField(r.Context(), itemID, fieldID); err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"value": nil})
		return
	}
	value, err := s.store.SetItemField(r.Context(), itemID, fieldID, payload.Value)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": value})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "请求内容不是有效 JSON")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的资源 ID")
		return 0, false
	}
	return id, true
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "记录不存在")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func randomToken() string {
	data := make([]byte, 24)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}
