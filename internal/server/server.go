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
	syncMu         sync.Mutex
	syncState      SyncStatus
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
	Items        []store.Item       `json:"items"`
	Tags         []store.Tag        `json:"tags"`
	Fields       []store.FieldDef   `json:"fields"`
	Stats        Stats              `json:"stats"`
	LastSync     *store.SyncRun     `json:"last_sync"`
	RepoStatuses []store.RepoStatus `json:"repo_statuses"`
	Syncing      bool               `json:"syncing"`
	SyncStatus   SyncStatus         `json:"sync_status"`
	Workspace    Workspace          `json:"workspace"`
}

type Workspace struct {
	ConfigPath      string `json:"config_path"`
	DBPath          string `json:"db_path"`
	RepoCount       int    `json:"repo_count"`
	StaleDays       int    `json:"stale_days"`
	ReviewStaleDays int    `json:"review_stale_days"`
	Version         string `json:"version"`
}

func New(options Options) *Server {
	token := randomToken()
	port := options.Config.Server.Port
	return &Server{
		store:        options.Store,
		config:       options.Config,
		paths:        options.Paths,
		version:      options.Version,
		sessionToken: token,
		allowedOrigins: map[string]struct{}{
			fmt.Sprintf("http://127.0.0.1:%d", port): {},
			fmt.Sprintf("http://localhost:%d", port): {},
			fmt.Sprintf("http://[::1]:%d", port):     {},
		},
		syncState: SyncStatus{Message: "尚未同步"},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/sync", s.handleStartSync)
	mux.HandleFunc("GET /api/sync/status", s.handleSyncStatus)
	mux.HandleFunc("POST /api/tags", s.handleCreateTag)
	mux.HandleFunc("PATCH /api/tags/{id}", s.handleUpdateTag)
	mux.HandleFunc("DELETE /api/tags/{id}", s.handleDeleteTag)
	mux.HandleFunc("PUT /api/items/{id}/tags", s.handleSetItemTags)
	mux.HandleFunc("POST /api/fields", s.handleCreateField)
	mux.HandleFunc("PATCH /api/fields/{id}", s.handleUpdateField)
	mux.HandleFunc("DELETE /api/fields/{id}", s.handleDeleteField)
	mux.HandleFunc("PUT /api/items/{id}/fields/{fieldID}", s.handleSetItemField)

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
	return fmt.Sprintf("127.0.0.1:%d", s.config.Server.Port)
}

func (s *Server) browserGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
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
			item.Stale = item.StaleDays >= s.config.Sync.StaleDays
			item.ReviewStale = item.Kind == "pr" &&
				(item.ReviewDecision == "REVIEW_REQUIRED" || len(item.Reviewers) > 0) &&
				item.StaleDays >= s.config.Sync.ReviewStaleDays
			if item.Stale {
				stats.Stale++
			}
			if item.ReviewStale {
				stats.ReviewStale++
			}
		}
	}

	s.syncMu.Lock()
	syncState := s.syncState
	s.syncMu.Unlock()
	writeJSON(w, http.StatusOK, StateResponse{
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
			RepoCount:       len(s.config.Repos),
			StaleDays:       s.config.Sync.StaleDays,
			ReviewStaleDays: s.config.Sync.ReviewStaleDays,
			Version:         s.version,
		},
	})
}

func (s *Server) handleStartSync(w http.ResponseWriter, r *http.Request) {
	s.syncMu.Lock()
	if s.syncState.Running {
		s.syncMu.Unlock()
		writeError(w, http.StatusConflict, "同步正在进行中")
		return
	}
	s.syncState = SyncStatus{
		Running:   true,
		Message:   "正在准备同步",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.syncMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		_, err := githubsync.Run(ctx, githubsync.SyncOptions{
			Store:            s.store,
			Repos:            s.config.Repos,
			PageSize:         s.config.Sync.RequestPageSize,
			ClosedWindowDays: s.config.Sync.ClosedWindowDays,
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
