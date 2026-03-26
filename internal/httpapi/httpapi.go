package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/observability"
	"github.com/beyond5959/ngent/internal/runtime"
	"github.com/beyond5959/ngent/internal/sse"
	"github.com/beyond5959/ngent/internal/storage"
)

// AgentInfo describes one supported agent entry returned by /v1/agents.
type AgentInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ThreadStore is the storage contract required by HTTP APIs.
type ThreadStore interface {
	UpsertClient(ctx context.Context, clientID string) error
	CreateThread(ctx context.Context, params storage.CreateThreadParams) (storage.Thread, error)
	GetThread(ctx context.Context, threadID string) (storage.Thread, error)
	DeleteThread(ctx context.Context, threadID string) error
	UpdateThreadTitle(ctx context.Context, threadID, title string) error
	UpdateThreadSummary(ctx context.Context, threadID, summary string) error
	UpdateThreadAgentOptions(ctx context.Context, threadID, agentOptionsJSON string) error
	UpsertAgentConfigCatalog(ctx context.Context, params storage.UpsertAgentConfigCatalogParams) error
	GetAgentConfigCatalog(ctx context.Context, agentID, modelID string) (storage.AgentConfigCatalog, error)
	ListAgentConfigCatalogsByAgent(ctx context.Context, agentID string) ([]storage.AgentConfigCatalog, error)
	GetAgentSlashCommands(ctx context.Context, agentID string) (storage.AgentSlashCommands, error)
	UpsertAgentSlashCommands(ctx context.Context, params storage.UpsertAgentSlashCommandsParams) error
	GetSessionTranscriptCache(ctx context.Context, agentID, cwd, sessionID string) (storage.SessionTranscriptCache, error)
	UpsertSessionTranscriptCache(ctx context.Context, params storage.UpsertSessionTranscriptCacheParams) error
	GetSessionConfigCache(ctx context.Context, agentID, cwd, sessionID string) (storage.SessionConfigCache, error)
	UpsertSessionConfigCache(ctx context.Context, params storage.UpsertSessionConfigCacheParams) error
	ListThreadsByClient(ctx context.Context, clientID string) ([]storage.Thread, error)
	CreateTurn(ctx context.Context, params storage.CreateTurnParams) (storage.Turn, error)
	CreateTurnAttachments(ctx context.Context, params []storage.CreateTurnAttachmentParams) error
	GetTurnAttachment(ctx context.Context, attachmentID string) (storage.TurnAttachment, error)
	GetTurn(ctx context.Context, turnID string) (storage.Turn, error)
	ListTurnsByThread(ctx context.Context, threadID string) ([]storage.Turn, error)
	AppendEvent(ctx context.Context, turnID, eventType, dataJSON string) (storage.Event, error)
	ListEventsByTurn(ctx context.Context, turnID string) ([]storage.Event, error)
	FinalizeTurn(ctx context.Context, params storage.FinalizeTurnParams) error
	ListRecentDirectories(ctx context.Context, clientID string, limit int) ([]string, error)
}

// TurnAgentFactory resolves a per-turn agent provider from thread metadata.
type TurnAgentFactory func(thread storage.Thread) (agents.Streamer, error)

// AgentModelsFactory resolves selectable model options for one agent.
type AgentModelsFactory func(ctx context.Context, agentID string) ([]agents.ModelOption, error)

// Config controls HTTP API behavior.
type Config struct {
	AuthToken          string
	DataDir            string
	Agents             []AgentInfo
	AllowedAgentIDs    []string
	AllowedRoots       []string
	Store              ThreadStore
	TurnController     *runtime.TurnController
	Agent              agents.Streamer
	TurnAgentFactory   TurnAgentFactory
	AgentModelsFactory AgentModelsFactory
	AgentIdleTTL       time.Duration
	Logger             *observability.Logger
	ContextRecentTurns int
	ContextMaxChars    int
	CompactMaxChars    int
	PermissionTimeout  time.Duration
	// FrontendHandler, if non-nil, is served for any request that does not
	// match /healthz or /v1/*. Intended for the embedded web UI.
	FrontendHandler http.Handler
}

// Server serves the HTTP API.
type Server struct {
	authToken          string
	dataDir            string
	agents             []AgentInfo
	allowedRoots       []string
	store              ThreadStore
	allowedAgent       map[string]struct{}
	turns              *runtime.TurnController
	turnAgentFactory   TurnAgentFactory
	agentModelsFactory AgentModelsFactory
	agentIdleTTL       time.Duration
	logger             *observability.Logger
	contextRecentTurns int
	contextMaxChars    int
	compactMaxChars    int
	permissionTimeout  time.Duration
	frontendHandler    http.Handler

	permissionsMu sync.Mutex
	permissions   map[string]*pendingPermission
	permissionSeq uint64

	agentMu       sync.Mutex
	agentsByScope map[string]*managedAgent
	janitorStop   chan struct{}
	janitorDone   chan struct{}
}

const (
	defaultContextRecentTurns = 10
	defaultContextMaxChars    = 20000
	defaultCompactMaxChars    = 4000
	defaultAgentIdleTTL       = 5 * time.Minute
	defaultPermissionTimeout  = 2 * time.Hour

	threadAgentOptionFreshSessionKey = "_ngentFreshSession"
	eventTypeUserPrompt              = "user_prompt"
	eventTypeMessageContent          = "message_content"
	eventTypeReasoningDelta          = "reasoning_delta"
	eventTypeSessionInfoUpdate       = "session_info_update"
	eventTypeToolCall                = "tool_call"
	eventTypeToolCallUpdate          = "tool_call_update"
)

const (
	codeInvalidArgument     = "INVALID_ARGUMENT"
	codeUnauthorized        = "UNAUTHORIZED"
	codeForbidden           = "FORBIDDEN"
	codeNotFound            = "NOT_FOUND"
	codeConflict            = "CONFLICT"
	codeTimeout             = "TIMEOUT"
	codeInternal            = "INTERNAL"
	codeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"
)

var errThreadConfigOptionsUnavailable = errors.New("thread config options are not available yet")

const maxTurnMultipartMemory = 32 << 20

type turnCreateRequest struct {
	Prompt  agents.Prompt
	Stream  bool
	Uploads []storedTurnAttachment
}

type storedTurnAttachment struct {
	PromptContent agents.PromptContent
	FilePath      string
}

// New creates a new API server.
func New(cfg Config) *Server {
	agentsList := make([]AgentInfo, len(cfg.Agents))
	copy(agentsList, cfg.Agents)

	roots := make([]string, 0, len(cfg.AllowedRoots))
	for _, root := range cfg.AllowedRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		roots = append(roots, filepath.Clean(root))
	}

	allowedAgent := make(map[string]struct{}, len(cfg.AllowedAgentIDs))
	for _, agentID := range cfg.AllowedAgentIDs {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			continue
		}
		allowedAgent[agentID] = struct{}{}
	}

	turnController := cfg.TurnController
	if turnController == nil {
		turnController = runtime.NewTurnController()
	}

	turnAgentFactory := cfg.TurnAgentFactory
	if turnAgentFactory == nil {
		agent := cfg.Agent
		if agent == nil {
			agent = agents.NewFakeAgent()
		}
		turnAgentFactory = func(thread storage.Thread) (agents.Streamer, error) {
			_ = thread
			return agent, nil
		}
	}

	permissionTimeout := cfg.PermissionTimeout
	if permissionTimeout <= 0 {
		permissionTimeout = defaultPermissionTimeout
	}

	contextRecentTurns := cfg.ContextRecentTurns
	if contextRecentTurns <= 0 {
		contextRecentTurns = defaultContextRecentTurns
	}

	contextMaxChars := cfg.ContextMaxChars
	if contextMaxChars <= 0 {
		contextMaxChars = defaultContextMaxChars
	}

	compactMaxChars := cfg.CompactMaxChars
	if compactMaxChars <= 0 {
		compactMaxChars = defaultCompactMaxChars
	}

	agentIdleTTL := cfg.AgentIdleTTL
	if agentIdleTTL <= 0 {
		agentIdleTTL = defaultAgentIdleTTL
	}

	logger := cfg.Logger
	if logger == nil {
		logger = observability.NewLoggerWithWriter(io.Discard, observability.LevelError)
	}

	dataDir := filepath.Clean(strings.TrimSpace(cfg.DataDir))
	if dataDir == "." || dataDir == "" {
		dataDir = uploadTempDir()
	}

	server := &Server{
		authToken:          cfg.AuthToken,
		dataDir:            dataDir,
		agents:             agentsList,
		allowedRoots:       roots,
		store:              cfg.Store,
		allowedAgent:       allowedAgent,
		turns:              turnController,
		turnAgentFactory:   turnAgentFactory,
		agentModelsFactory: cfg.AgentModelsFactory,
		agentIdleTTL:       agentIdleTTL,
		logger:             logger,
		contextRecentTurns: contextRecentTurns,
		contextMaxChars:    contextMaxChars,
		compactMaxChars:    compactMaxChars,
		permissionTimeout:  permissionTimeout,
		frontendHandler:    cfg.FrontendHandler,
		permissions:        make(map[string]*pendingPermission),
		agentsByScope:      make(map[string]*managedAgent),
		janitorStop:        make(chan struct{}),
		janitorDone:        make(chan struct{}),
	}
	go server.idleJanitorLoop()
	return server
}

// ServeHTTP handles all HTTP requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	loggingWriter := newLoggingResponseWriter(w)
	s.serveHTTP(loggingWriter, r)
	s.logRequestCompletion(r, loggingWriter, startedAt)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if attachmentID, ok := parseAttachmentPath(r.URL.Path); ok {
		s.handleAttachment(w, r, attachmentID)
		return
	}

	if r.URL.Path == "/healthz" {
		s.handleHealthz(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/v1/") {
		if !s.isAuthorized(r) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or invalid bearer token", map[string]any{
				"header": "Authorization",
			})
			return
		}

		clientID := strings.TrimSpace(r.Header.Get("X-Client-ID"))
		if clientID == "" {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "missing required header X-Client-ID", map[string]any{
				"header": "X-Client-ID",
			})
			return
		}

		if s.store == nil {
			writeError(w, http.StatusInternalServerError, codeInternal, "storage is not configured", map[string]any{})
			return
		}

		if err := s.store.UpsertClient(r.Context(), clientID); err != nil {
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to upsert client", map[string]any{
				"reason": err.Error(),
			})
			return
		}

		s.routeV1(w, r, clientID)
		return
	}

	if s.frontendHandler != nil {
		s.frontendHandler.ServeHTTP(w, r)
		return
	}

	writeError(w, http.StatusNotFound, codeNotFound, "endpoint not found", map[string]any{"path": r.URL.Path})
}

func (s *Server) logRequestCompletion(r *http.Request, w *loggingResponseWriter, startedAt time.Time) {
	if s.logger == nil {
		return
	}
	s.logger.HTTPRequest(observability.HTTPRequestLogEntry{
		RemoteAddr:  requestClientAddr(r),
		Method:      r.Method,
		Path:        requestLogPath(r),
		Proto:       r.Proto,
		Status:      w.StatusCode(),
		RequestTime: startedAt,
		Duration:    time.Since(startedAt),
	})
}

func (s *Server) routeV1(w http.ResponseWriter, r *http.Request, clientID string) {
	if r.URL.Path == "/v1/agents" {
		s.handleAgents(w, r)
		return
	}
	if agentID, ok := parseAgentModelsPath(r.URL.Path); ok {
		s.handleAgentModels(w, r, agentID)
		return
	}

	if r.URL.Path == "/v1/path-search" {
		s.handlePathSearch(w, r)
		return
	}

	if r.URL.Path == "/v1/recent-directories" {
		s.handleRecentDirectories(w, r, clientID)
		return
	}

	if r.URL.Path == "/v1/threads" {
		s.handleThreadsCollection(w, r, clientID)
		return
	}

	if permissionID, ok := parsePermissionPath(r.URL.Path); ok {
		s.handlePermissionDecision(w, r, clientID, permissionID)
		return
	}

	if turnID, ok := parseTurnCancelPath(r.URL.Path); ok {
		s.handleCancelTurn(w, r, clientID, turnID)
		return
	}

	if threadID, subresource, ok := parseThreadPath(r.URL.Path); ok {
		s.handleThreadResource(w, r, clientID, threadID, subresource)
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found", map[string]any{"path": r.URL.Path})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request, attachmentID string) {
	if err := requireMethod(r, http.MethodGet); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}
	if !s.isAttachmentAuthorized(r) {
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or invalid attachment token", map[string]any{})
		return
	}

	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if clientID == "" {
		writeError(w, http.StatusBadRequest, codeInvalidArgument, "missing required query parameter client_id", map[string]any{
			"query": "client_id",
		})
		return
	}
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "storage is not configured", map[string]any{})
		return
	}

	attachment, err := s.store.GetTurnAttachment(r.Context(), attachmentID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "attachment not found", map[string]any{})
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to load attachment", map[string]any{
			"reason": err.Error(),
		})
		return
	}

	turn, err := s.store.GetTurn(r.Context(), attachment.TurnID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "attachment not found", map[string]any{})
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to load attachment turn", map[string]any{
			"reason": err.Error(),
		})
		return
	}
	if _, ok := s.getOwnedThread(r.Context(), clientID, turn.ThreadID); !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "attachment not found", map[string]any{})
		return
	}

	attachmentPath := filepath.Clean(strings.TrimSpace(attachment.FilePath))
	if attachmentPath == "" || !isPathAllowed(attachmentPath, []string{s.dataDir}) {
		writeError(w, http.StatusInternalServerError, codeInternal, "attachment path is invalid", map[string]any{})
		return
	}

	file, err := os.Open(attachmentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, codeNotFound, "attachment file not found", map[string]any{})
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to open attachment file", map[string]any{
			"reason": err.Error(),
		})
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to stat attachment file", map[string]any{
			"reason": err.Error(),
		})
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusNotFound, codeNotFound, "attachment file not found", map[string]any{})
		return
	}

	contentType := strings.TrimSpace(attachment.MimeType)
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.Name)))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", normalizeUploadFilename(attachment.Name)))
	http.ServeContent(w, r, attachment.Name, info.ModTime(), file)
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Agents []AgentInfo `json:"agents"`
	}{Agents: s.agents})
}

func (s *Server) handleAgentModels(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}

	if _, ok := s.allowedAgent[agentID]; !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "agent not found", map[string]any{
			"agent": agentID,
		})
		return
	}

	models, found, err := s.loadStoredAgentModels(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to load stored agent models", map[string]any{
			"agent":  agentID,
			"reason": err.Error(),
		})
		return
	}
	if !found {
		models = []agents.ModelOption{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agentId": agentID,
		"models":  models,
	})
}

func (s *Server) handleThreadsCollection(w http.ResponseWriter, r *http.Request, clientID string) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateThread(w, r, clientID)
	case http.MethodGet:
		s.handleListThreads(w, r, clientID)
	default:
		writeMethodNotAllowed(w, r)
	}
}

func (s *Server) handleThreadResource(w http.ResponseWriter, r *http.Request, clientID, threadID, subresource string) {
	switch subresource {
	case "":
		switch r.Method {
		case http.MethodGet:
			s.handleGetThread(w, r, clientID, threadID)
		case http.MethodPatch:
			s.handleUpdateThread(w, r, clientID, threadID)
		case http.MethodDelete:
			s.handleDeleteThread(w, r, clientID, threadID)
		default:
			writeMethodNotAllowed(w, r)
		}
	case "turns":
		s.handleCreateTurnStream(w, r, clientID, threadID)
	case "compact":
		s.handleCompactThread(w, r, clientID, threadID)
	case "history":
		s.handleThreadHistory(w, r, clientID, threadID)
	case "sessions":
		s.handleThreadSessions(w, r, clientID, threadID)
	case "session-history":
		s.handleThreadSessionHistory(w, r, clientID, threadID)
	case "config-options":
		s.handleThreadConfigOptions(w, r, clientID, threadID)
	case "slash-commands":
		s.handleThreadSlashCommands(w, r, clientID, threadID)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found", map[string]any{"path": r.URL.Path})
	}
}

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request, clientID string) {
	var req struct {
		Agent        string          `json:"agent"`
		CWD          string          `json:"cwd"`
		Title        string          `json:"title"`
		AgentOptions json.RawMessage `json:"agentOptions"`
	}

	if err := requireMethod(r, http.MethodPost); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body", map[string]any{"reason": err.Error()})
		return
	}

	req.Agent = strings.TrimSpace(req.Agent)
	if _, ok := s.allowedAgent[req.Agent]; !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "agent is not in allowlist", map[string]any{
			"field":         "agent",
			"allowedAgents": sortedAgentIDs(s.allowedAgent),
		})
		return
	}

	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cwd is required", map[string]any{"field": "cwd"})
		return
	}

	// Expand ~ to home directory
	expandedCWD, err := expandPath(cwd)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to expand path", map[string]any{"field": "cwd", "reason": err.Error()})
		return
	}
	cwd = expandedCWD

	if !filepath.IsAbs(cwd) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cwd must be an absolute path", map[string]any{"field": "cwd"})
		return
	}
	cwd = filepath.Clean(cwd)
	if !isPathAllowed(cwd, s.allowedRoots) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "cwd is outside allowed roots", map[string]any{
			"field":         "cwd",
			"cwd":           cwd,
			"allowed_roots": s.allowedRoots,
		})
		return
	}

	if _, err := os.Stat(cwd); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "cwd does not exist", map[string]any{
				"field": "cwd",
				"cwd":   cwd,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to check cwd", map[string]any{
			"field":  "cwd",
			"reason": err.Error(),
		})
		return
	}

	agentOptionsJSON, err := normalizeAgentOptions(req.AgentOptions)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "agentOptions must be a JSON object", map[string]any{"field": "agentOptions"})
		return
	}

	threadID := newThreadID()
	_, err = s.store.CreateThread(r.Context(), storage.CreateThreadParams{
		ThreadID:         threadID,
		ClientID:         clientID,
		AgentID:          req.Agent,
		CWD:              cwd,
		Title:            req.Title,
		AgentOptionsJSON: agentOptionsJSON,
		Summary:          "",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create thread", map[string]any{"reason": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"threadId": threadID})
}

func (s *Server) handleListThreads(w http.ResponseWriter, r *http.Request, clientID string) {
	if err := requireMethod(r, http.MethodGet); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	threads, err := s.store.ListThreadsByClient(r.Context(), clientID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list threads", map[string]any{"reason": err.Error()})
		return
	}

	items := make([]threadResponse, 0, len(threads))
	for _, thread := range threads {
		item, convErr := toThreadResponse(thread)
		if convErr != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to encode thread", map[string]any{"reason": convErr.Error()})
			return
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{"threads": items})
}

func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodGet); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "thread not found", map[string]any{})
		return
	}

	resp, convErr := toThreadResponse(thread)
	if convErr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to encode thread", map[string]any{"reason": convErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"thread": resp})
}

func (s *Server) handleUpdateThread(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodPatch); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
		return
	}

	var req struct {
		Title        *string          `json:"title"`
		AgentOptions *json.RawMessage `json:"agentOptions"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidArgument, "invalid JSON body", map[string]any{"reason": err.Error()})
		return
	}

	agentOptionsJSON := ""
	sessionOnlyUpdate := false
	currentSessionID := threadSessionID(thread.AgentOptionsJSON)
	currentFreshSession := threadFreshSessionRequested(thread.AgentOptionsJSON)
	if req.AgentOptions != nil {
		var err error
		agentOptionsJSON, err = normalizeAgentOptions(*req.AgentOptions)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "agentOptions must be a JSON object", map[string]any{"field": "agentOptions"})
			return
		}

		nextSessionID := threadSessionID(agentOptionsJSON)
		if nextSessionID != "" && nextSessionID != currentSessionID {
			agentOptionsJSON, err = withoutThreadConfigState(agentOptionsJSON)
			if err != nil {
				writeError(w, http.StatusBadRequest, codeInvalidArgument, "agentOptions must be a JSON object", map[string]any{"field": "agentOptions"})
				return
			}
		}
		sessionOnlyUpdate, err = isSessionOnlyAgentOptionsUpdate(thread.AgentOptionsJSON, agentOptionsJSON)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "agentOptions must be a JSON object", map[string]any{"field": "agentOptions"})
			return
		}

		nextSessionID = threadSessionID(agentOptionsJSON)
		shouldRequestFreshSession := currentFreshSession
		switch {
		case nextSessionID != "":
			shouldRequestFreshSession = false
		case currentSessionID != "":
			shouldRequestFreshSession = true
		}

		agentOptionsJSON, _, err = withThreadFreshSessionRequested(agentOptionsJSON, shouldRequestFreshSession)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "agentOptions must be a JSON object", map[string]any{"field": "agentOptions"})
			return
		}
	}

	allowSessionSelectionWhileActive := req.Title == nil && req.AgentOptions != nil && sessionOnlyUpdate
	if s.turns.IsThreadActive(thread.ThreadID) && !allowSessionSelectionWhileActive {
		writeError(w, http.StatusConflict, codeConflict, "thread has an active turn", map[string]any{"threadId": thread.ThreadID})
		return
	}

	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if err := s.store.UpdateThreadTitle(r.Context(), thread.ThreadID, title); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
				return
			}
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to update thread", map[string]any{"reason": err.Error()})
			return
		}
	}

	if req.AgentOptions != nil {
		if err := s.store.UpdateThreadAgentOptions(r.Context(), thread.ThreadID, agentOptionsJSON); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
				return
			}
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to update thread", map[string]any{"reason": err.Error()})
			return
		}

		nextSessionID := threadSessionID(agentOptionsJSON)
		if sessionOnlyUpdate && currentSessionID != nextSessionID && nextSessionID == "" {
			s.closeThreadAgentScope(thread.ThreadID, agentOptionsJSON, "thread_session_reset")
			if plainAgentOptionsJSON, changed, err := withThreadFreshSessionRequested(agentOptionsJSON, false); err == nil && changed {
				s.closeThreadAgentScope(thread.ThreadID, plainAgentOptionsJSON, "thread_session_reset")
			}
		}
		if !sessionOnlyUpdate {
			s.closeThreadAgents(thread.ThreadID, "thread_updated")
		}
	}

	updatedThread, ok := s.getOwnedThread(r.Context(), clientID, thread.ThreadID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
		return
	}

	resp, convErr := toThreadResponse(updatedThread)
	if convErr != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to encode thread", map[string]any{"reason": convErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"thread": resp})
}

func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodDelete); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
		return
	}

	deleteGuardTurnID := "delete-" + newTurnID()
	if err := s.turns.ActivateThreadExclusive(thread.ThreadID, deleteGuardTurnID, nil); err != nil {
		if errors.Is(err, runtime.ErrActiveTurnExists) {
			writeError(w, http.StatusConflict, codeConflict, "thread has an active turn", map[string]any{"threadId": thread.ThreadID})
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to lock thread for delete", map[string]any{"reason": err.Error()})
		return
	}
	defer s.turns.ReleaseThreadExclusive(thread.ThreadID, deleteGuardTurnID)

	if err := s.store.DeleteThread(r.Context(), thread.ThreadID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to delete thread", map[string]any{"reason": err.Error()})
		return
	}

	s.closeThreadAgents(thread.ThreadID, "thread_deleted")

	writeJSON(w, http.StatusOK, map[string]any{
		"threadId": thread.ThreadID,
		"status":   "deleted",
	})
}

func (s *Server) handleCreateTurnStream(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodPost); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "thread not found", map[string]any{})
		return
	}

	req, err := s.decodeTurnCreateRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request body", map[string]any{"reason": err.Error()})
		return
	}
	req.Prompt = agents.NormalizePrompt(req.Prompt)
	keepUploads := false
	defer func() {
		if keepUploads {
			return
		}
		removeStoredAttachments(req.Uploads)
	}()
	if !req.Stream {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "stream must be true", map[string]any{"field": "stream"})
		return
	}
	if len(req.Prompt.Content) == 0 {
		writeError(w, http.StatusBadRequest, codeInvalidArgument, "input or attachments are required", map[string]any{
			"fields": []string{"input", "attachments"},
		})
		return
	}

	injectedPrompt, err := s.buildInjectedPrompt(r.Context(), thread, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to build context window", map[string]any{
			"reason": err.Error(),
		})
		return
	}

	streamAgent, err := s.resolveTurnAgent(thread)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to resolve agent provider", map[string]any{
			"agent":  thread.AgentID,
			"reason": err.Error(),
		})
		return
	}

	turnID := newTurnID()
	turnSessionID := threadSessionID(thread.AgentOptionsJSON)
	turnCtx, cancelTurn := context.WithCancel(r.Context())
	persistCtx := context.WithoutCancel(r.Context())
	if err := s.turns.Activate(thread.ThreadID, turnSessionID, turnID, cancelTurn); err != nil {
		if errors.Is(err, runtime.ErrActiveTurnExists) {
			writeError(w, http.StatusConflict, "CONFLICT", "session already has an active turn", map[string]any{
				"threadId":  thread.ThreadID,
				"sessionId": turnSessionID,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to activate turn", map[string]any{"reason": err.Error()})
		return
	}
	defer func() {
		cancelTurn()
		s.turns.Release(thread.ThreadID, turnSessionID, turnID)
	}()
	if err := s.syncThreadConfigSelections(r.Context(), thread, streamAgent); err != nil {
		writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to sync thread config options", map[string]any{
			"threadId": thread.ThreadID,
			"reason":   err.Error(),
		})
		return
	}

	if _, err := s.store.CreateTurn(r.Context(), storage.CreateTurnParams{
		TurnID:      turnID,
		ThreadID:    thread.ThreadID,
		RequestText: req.Prompt.LegacyText(),
		Status:      "running",
		IsInternal:  false,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create turn", map[string]any{"reason": err.Error()})
		return
	}
	if err := s.persistTurnAttachments(persistCtx, turnID, req.Uploads); err != nil {
		s.finalizeTurnWithBestEffort(persistCtx, turnID, "failed", "error", "", err.Error())
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to persist turn attachments", map[string]any{
			"reason": err.Error(),
		})
		return
	}
	keepUploads = true

	streamWriter, err := sse.NewWriter(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "SSE is not supported by response writer", map[string]any{})
		return
	}

	aggregated := strings.Builder{}

	emit := func(eventType string, payload map[string]any) error {
		dataJSON, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		if _, appendErr := s.store.AppendEvent(persistCtx, turnID, eventType, string(dataJSON)); appendErr != nil {
			return appendErr
		}
		return streamWriter.Event(eventType, payload)
	}
	appendOnlyEvent := func(eventType string, payload map[string]any) error {
		dataJSON, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		_, appendErr := s.store.AppendEvent(persistCtx, turnID, eventType, string(dataJSON))
		return appendErr
	}

	if req.Prompt.HasResourceLinks() {
		if err := appendOnlyEvent(eventTypeUserPrompt, req.Prompt.EventPayload(turnID)); err != nil {
			s.finalizeTurnWithBestEffort(persistCtx, turnID, "failed", "error", "", err.Error())
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to persist user prompt", map[string]any{"reason": err.Error()})
			return
		}
	}

	w.WriteHeader(http.StatusOK)

	turnCtx = agents.WithPermissionHandler(turnCtx, func(permissionCtx context.Context, req agents.PermissionRequest) (agents.PermissionResponse, error) {
		permissionID := s.nextPermissionID(req.RequestID)
		pending := newPendingPermission(clientID, req.Options)
		s.registerPermission(permissionID, pending)
		defer s.unregisterPermission(permissionID, pending)

		payload := map[string]any{
			"turnId":       turnID,
			"permissionId": permissionID,
			"approval":     req.Approval,
			"command":      req.Command,
			"requestId":    req.RequestID,
		}
		if len(req.Options) > 0 {
			payload["options"] = req.Options
		}
		if err := emit("permission_required", payload); err != nil {
			pending.Resolve(permissionFailClosedResponse())
			return permissionFailClosedResponse(), err
		}

		return s.waitPermissionResponse(permissionCtx, pending), nil
	})
	turnCtx = agents.WithPlanHandler(turnCtx, func(planCtx context.Context, entries []agents.PlanEntry) error {
		_ = planCtx
		payloadEntries := agents.ClonePlanEntries(entries)
		if payloadEntries == nil {
			payloadEntries = []agents.PlanEntry{}
		}
		return emit("plan_update", map[string]any{
			"turnId":  turnID,
			"entries": payloadEntries,
		})
	})
	turnCtx = agents.WithReasoningHandler(turnCtx, func(reasoningCtx context.Context, delta string) error {
		_ = reasoningCtx
		return emit(eventTypeReasoningDelta, map[string]any{
			"turnId": turnID,
			"delta":  delta,
		})
	})
	turnCtx = agents.WithSessionInfoHandler(turnCtx, func(sessionInfoCtx context.Context, update agents.SessionInfoUpdate) error {
		_ = sessionInfoCtx
		return emit(eventTypeSessionInfoUpdate, map[string]any{
			"turnId":    turnID,
			"sessionId": update.SessionID,
			"title":     update.Title,
		})
	})
	turnCtx = agents.WithMessageContentHandler(turnCtx, func(messageCtx context.Context, event agents.ACPMessageContent) error {
		_ = messageCtx
		return emit(eventTypeMessageContent, event.EventPayload(turnID))
	})
	turnCtx = agents.WithToolCallHandler(turnCtx, func(toolCallCtx context.Context, event agents.ACPToolCall) error {
		_ = toolCallCtx
		eventType := strings.TrimSpace(event.Type)
		switch eventType {
		case eventTypeToolCall, eventTypeToolCallUpdate:
		default:
			return nil
		}
		return emit(eventType, event.EventPayload(turnID))
	})
	turnCtx = agents.WithSlashCommandsHandler(turnCtx, func(commandsCtx context.Context, commands []agents.SlashCommand) error {
		_ = commandsCtx
		if err := s.persistAgentSlashCommands(persistCtx, thread.AgentID, commands); err != nil {
			s.logger.Warn("thread.slash_commands_persist_failed",
				"threadId", thread.ThreadID,
				"agent", thread.AgentID,
				"reason", err.Error(),
			)
		}
		return nil
	})
	turnCtx = agents.WithConfigOptionsHandler(turnCtx, func(configOptionsCtx context.Context, options []agents.ConfigOption) error {
		_ = configOptionsCtx
		s.persistThreadConfigSnapshotBestEffort(persistCtx, &thread, options)
		return nil
	})
	turnCtx = agents.WithSessionBoundHandler(turnCtx, func(sessionCtx context.Context, sessionID string) error {
		_ = sessionCtx
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return nil
		}
		if err := s.turns.BindTurnSession(turnID, sessionID); err != nil {
			if !errors.Is(err, runtime.ErrActiveTurnExists) {
				s.logger.Warn("thread.session_bind_runtime_failed",
					"threadId", thread.ThreadID,
					"agent", thread.AgentID,
					"turnId", turnID,
					"reason", err.Error(),
				)
			}
		} else {
			turnSessionID = sessionID
		}

		nextAgentOptionsJSON, changed, err := withThreadSessionID(thread.AgentOptionsJSON, sessionID)
		if err != nil {
			s.logger.Warn("thread.session_bind_encode_failed",
				"threadId", thread.ThreadID,
				"agent", thread.AgentID,
				"reason", err.Error(),
			)
		} else if changed {
			if err := s.store.UpdateThreadAgentOptions(persistCtx, thread.ThreadID, nextAgentOptionsJSON); err != nil {
				s.logger.Warn("thread.session_bind_persist_failed",
					"threadId", thread.ThreadID,
					"agent", thread.AgentID,
					"reason", err.Error(),
				)
			} else {
				if err := s.rebindManagedAgentScope(thread.ThreadID, thread.AgentOptionsJSON, nextAgentOptionsJSON); err != nil {
					s.logger.Warn("thread.session_bind_agent_scope_failed",
						"threadId", thread.ThreadID,
						"agent", thread.AgentID,
						"turnId", turnID,
						"reason", err.Error(),
					)
				}
				thread.AgentOptionsJSON = nextAgentOptionsJSON
				s.persistThreadSessionConfigSnapshotBestEffort(persistCtx, thread)
			}
		}

		if err := emit("session_bound", map[string]any{
			"threadId":  thread.ThreadID,
			"sessionId": sessionID,
		}); err != nil {
			s.logger.Warn("thread.session_bind_emit_failed",
				"threadId", thread.ThreadID,
				"agent", thread.AgentID,
				"reason", err.Error(),
			)
		}
		return nil
	})

	if err := emit("turn_started", map[string]any{"turnId": turnID}); err != nil {
		s.finalizeTurnWithBestEffort(persistCtx, turnID, "failed", "error", "", err.Error())
		return
	}

	stopReason, streamErr := agents.StreamPrompt(turnCtx, streamAgent, injectedPrompt, func(delta string) error {
		aggregated.WriteString(delta)
		return emit("message_delta", map[string]any{"turnId": turnID, "delta": delta})
	})

	finalStatus := "completed"
	finalReason := string(agents.StopReasonEndTurn)
	errorMessage := ""

	if streamErr != nil {
		finalStatus = "failed"
		finalReason = "error"
		errorMessage = streamErr.Error()
		_ = emit("error", map[string]any{
			"turnId":  turnID,
			"code":    classifyStreamErrorCode(streamErr),
			"message": streamErr.Error(),
		})
	} else if stopReason == agents.StopReasonCancelled {
		finalStatus = "cancelled"
		finalReason = string(agents.StopReasonCancelled)
	}

	if err := emit("turn_completed", map[string]any{"turnId": turnID, "stopReason": finalReason}); err != nil && errorMessage == "" {
		errorMessage = err.Error()
		if finalStatus == "completed" {
			finalStatus = "failed"
			finalReason = "error"
		}
	}

	s.finalizeTurnWithBestEffort(persistCtx, turnID, finalStatus, finalReason, aggregated.String(), errorMessage)
}

func (s *Server) persistTurnAttachments(ctx context.Context, turnID string, uploads []storedTurnAttachment) error {
	if len(uploads) == 0 {
		return nil
	}

	params := make([]storage.CreateTurnAttachmentParams, 0, len(uploads))
	for _, upload := range uploads {
		attachmentID := strings.TrimSpace(upload.PromptContent.AttachmentID)
		if attachmentID == "" {
			return errors.New("attachmentID is required")
		}
		params = append(params, storage.CreateTurnAttachmentParams{
			AttachmentID: attachmentID,
			TurnID:       turnID,
			Name:         strings.TrimSpace(upload.PromptContent.Name),
			MimeType:     strings.TrimSpace(upload.PromptContent.MimeType),
			Size:         upload.PromptContent.Size,
			FilePath:     strings.TrimSpace(upload.FilePath),
		})
	}
	return s.store.CreateTurnAttachments(ctx, params)
}

func (s *Server) handleCompactThread(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodPost); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "thread not found", map[string]any{})
		return
	}

	var req struct {
		MaxSummaryChars int `json:"maxSummaryChars"`
	}
	if r.Body != nil {
		if err := decodeJSONBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body", map[string]any{"reason": err.Error()})
			return
		}
	}

	summaryLimit := req.MaxSummaryChars
	if summaryLimit <= 0 {
		summaryLimit = s.compactMaxChars
	}

	streamAgent, err := s.resolveTurnAgent(thread)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to resolve agent provider", map[string]any{
			"agent":  thread.AgentID,
			"reason": err.Error(),
		})
		return
	}

	compactPrompt, err := s.buildCompactPrompt(r.Context(), thread, summaryLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to build compact prompt", map[string]any{
			"reason": err.Error(),
		})
		return
	}

	turnID := newTurnID()
	turnCtx, cancelTurn := context.WithCancel(r.Context())
	persistCtx := context.WithoutCancel(r.Context())
	if err := s.turns.ActivateThreadExclusive(thread.ThreadID, turnID, cancelTurn); err != nil {
		if errors.Is(err, runtime.ErrActiveTurnExists) {
			writeError(w, http.StatusConflict, "CONFLICT", "thread already has an active turn", map[string]any{"threadId": thread.ThreadID})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to activate compact turn", map[string]any{"reason": err.Error()})
		return
	}
	defer func() {
		cancelTurn()
		s.turns.ReleaseThreadExclusive(thread.ThreadID, turnID)
	}()

	if _, err := s.store.CreateTurn(r.Context(), storage.CreateTurnParams{
		TurnID:      turnID,
		ThreadID:    thread.ThreadID,
		RequestText: compactPrompt,
		Status:      "running",
		IsInternal:  true,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create compact turn", map[string]any{"reason": err.Error()})
		return
	}

	appendOnlyEvent := func(eventType string, payload map[string]any) error {
		dataJSON, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		_, appendErr := s.store.AppendEvent(persistCtx, turnID, eventType, string(dataJSON))
		return appendErr
	}

	if err := appendOnlyEvent("turn_started", map[string]any{"turnId": turnID}); err != nil {
		s.finalizeTurnWithBestEffort(persistCtx, turnID, "failed", "error", "", err.Error())
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to persist compact start event", map[string]any{"reason": err.Error()})
		return
	}

	aggregated := strings.Builder{}
	turnCtx = agents.WithPlanHandler(turnCtx, func(planCtx context.Context, entries []agents.PlanEntry) error {
		_ = planCtx
		payloadEntries := agents.ClonePlanEntries(entries)
		if payloadEntries == nil {
			payloadEntries = []agents.PlanEntry{}
		}
		return appendOnlyEvent("plan_update", map[string]any{
			"turnId":  turnID,
			"entries": payloadEntries,
		})
	})
	turnCtx = agents.WithReasoningHandler(turnCtx, func(reasoningCtx context.Context, delta string) error {
		_ = reasoningCtx
		return appendOnlyEvent(eventTypeReasoningDelta, map[string]any{
			"turnId": turnID,
			"delta":  delta,
		})
	})
	turnCtx = agents.WithMessageContentHandler(turnCtx, func(messageCtx context.Context, event agents.ACPMessageContent) error {
		_ = messageCtx
		return appendOnlyEvent(eventTypeMessageContent, event.EventPayload(turnID))
	})
	turnCtx = agents.WithToolCallHandler(turnCtx, func(toolCallCtx context.Context, event agents.ACPToolCall) error {
		_ = toolCallCtx
		eventType := strings.TrimSpace(event.Type)
		switch eventType {
		case eventTypeToolCall, eventTypeToolCallUpdate:
		default:
			return nil
		}
		return appendOnlyEvent(eventType, event.EventPayload(turnID))
	})
	stopReason, streamErr := streamAgent.Stream(turnCtx, compactPrompt, func(delta string) error {
		aggregated.WriteString(delta)
		return appendOnlyEvent("message_delta", map[string]any{
			"turnId": turnID,
			"delta":  delta,
		})
	})

	finalStatus := "completed"
	finalReason := string(agents.StopReasonEndTurn)
	errorMessage := ""

	if streamErr != nil {
		finalStatus = "failed"
		finalReason = "error"
		errorMessage = streamErr.Error()
		_ = appendOnlyEvent("error", map[string]any{
			"turnId":  turnID,
			"code":    classifyStreamErrorCode(streamErr),
			"message": streamErr.Error(),
		})
	} else if stopReason == agents.StopReasonCancelled {
		finalStatus = "cancelled"
		finalReason = string(agents.StopReasonCancelled)
	}

	if err := appendOnlyEvent("turn_completed", map[string]any{"turnId": turnID, "stopReason": finalReason}); err != nil && errorMessage == "" {
		errorMessage = err.Error()
		if finalStatus == "completed" {
			finalStatus = "failed"
			finalReason = "error"
		}
	}

	newSummary := clampToChars(strings.TrimSpace(aggregated.String()), summaryLimit)
	if finalStatus == "completed" && finalReason == string(agents.StopReasonEndTurn) {
		if err := s.store.UpdateThreadSummary(persistCtx, thread.ThreadID, newSummary); err != nil {
			finalStatus = "failed"
			finalReason = "error"
			errorMessage = err.Error()
		}
	}

	s.finalizeTurnWithBestEffort(persistCtx, turnID, finalStatus, finalReason, aggregated.String(), errorMessage)

	if finalStatus != "completed" {
		statusCode := http.StatusInternalServerError
		errorCode := codeInternal
		if streamErr != nil {
			errorCode = classifyStreamErrorCode(streamErr)
			switch errorCode {
			case codeTimeout:
				statusCode = http.StatusGatewayTimeout
			case codeUpstreamUnavailable:
				statusCode = http.StatusServiceUnavailable
			}
		}
		writeError(w, statusCode, errorCode, "compact failed", map[string]any{
			"turnId": turnID,
			"reason": errorMessage,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"threadId":     thread.ThreadID,
		"turnId":       turnID,
		"status":       finalStatus,
		"stopReason":   finalReason,
		"summary":      newSummary,
		"summaryChars": runeLen(newSummary),
	})
}

func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request, clientID, turnID string) {
	if err := requireMethod(r, http.MethodPost); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	turn, err := s.store.GetTurn(r.Context(), turnID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "turn not found", map[string]any{})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load turn", map[string]any{"reason": err.Error()})
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, turn.ThreadID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "turn not found", map[string]any{})
		return
	}

	if err := s.turns.Cancel(turnID); err != nil {
		if errors.Is(err, runtime.ErrTurnNotActive) {
			writeError(w, http.StatusConflict, "CONFLICT", "turn is not active", map[string]any{"turnId": turnID})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to cancel turn", map[string]any{"reason": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"turnId":   turnID,
		"threadId": thread.ThreadID,
		"status":   "cancelling",
	})
}

func (s *Server) handlePermissionDecision(w http.ResponseWriter, r *http.Request, clientID, permissionID string) {
	if err := requireMethod(r, http.MethodPost); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	var req struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body", map[string]any{"reason": err.Error()})
		return
	}

	response := agents.PermissionResponse{
		SelectedOptionID: strings.TrimSpace(req.OptionID),
	}
	if rawOutcome := strings.TrimSpace(req.Outcome); rawOutcome != "" {
		outcome, ok := normalizePermissionOutcome(rawOutcome)
		if !ok {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "outcome must be approved, declined, or cancelled", map[string]any{
				"field": "outcome",
			})
			return
		}
		response.Outcome = outcome
	}
	if response.Outcome == "" && response.SelectedOptionID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "outcome or optionId is required", map[string]any{
			"field": "outcome",
		})
		return
	}

	resolvedResponse, err := s.resolvePermission(permissionID, clientID, response)
	if err != nil {
		if errors.Is(err, errPermissionNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "permission not found", map[string]any{})
			return
		}
		if errors.Is(err, errPermissionInvalidOption) {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "optionId must match one of the advertised permission options", map[string]any{
				"field":        "optionId",
				"permissionId": permissionID,
			})
			return
		}
		if errors.Is(err, errPermissionOutcomeRequired) {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "outcome is required for this permission selection", map[string]any{
				"field":        "outcome",
				"permissionId": permissionID,
			})
			return
		}
		if errors.Is(err, errPermissionAlreadyResolved) {
			writeError(w, http.StatusConflict, "CONFLICT", "permission already resolved", map[string]any{
				"permissionId": permissionID,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to resolve permission", map[string]any{
			"reason": err.Error(),
		})
		return
	}

	payload := map[string]any{
		"permissionId": permissionID,
		"status":       "recorded",
		"outcome":      string(resolvedResponse.Outcome),
	}
	if resolvedResponse.SelectedOptionID != "" {
		payload["optionId"] = resolvedResponse.SelectedOptionID
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleThreadHistory(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodGet); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	if _, ok := s.getOwnedThread(r.Context(), clientID, threadID); !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "thread not found", map[string]any{})
		return
	}

	includeEvents := parseBoolQuery(r, "includeEvents")
	includeInternal := parseBoolQuery(r, "includeInternal")

	turns, err := s.store.ListTurnsByThread(r.Context(), threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list history", map[string]any{"reason": err.Error()})
		return
	}

	respTurns := make([]turnHistoryResponse, 0, len(turns))
	for _, turn := range turns {
		if !includeInternal && turn.IsInternal {
			continue
		}

		respTurn := turnHistoryResponse{
			TurnID:       turn.TurnID,
			RequestText:  turn.RequestText,
			ResponseText: turn.ResponseText,
			IsInternal:   turn.IsInternal,
			Status:       turn.Status,
			StopReason:   turn.StopReason,
			ErrorMessage: turn.ErrorMessage,
			CreatedAt:    turn.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		if turn.CompletedAt != nil {
			completed := turn.CompletedAt.UTC().Format(time.RFC3339Nano)
			respTurn.CompletedAt = &completed
		}

		if includeEvents {
			events, eventsErr := s.store.ListEventsByTurn(r.Context(), turn.TurnID)
			if eventsErr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list events", map[string]any{"reason": eventsErr.Error()})
				return
			}
			respEvents := make([]eventHistoryResponse, 0, len(events))
			for _, event := range events {
				raw := json.RawMessage(event.DataJSON)
				if len(strings.TrimSpace(event.DataJSON)) == 0 || !json.Valid(raw) {
					raw = json.RawMessage(`{}`)
				}
				respEvents = append(respEvents, eventHistoryResponse{
					EventID:   event.EventID,
					Seq:       event.Seq,
					Type:      event.Type,
					Data:      raw,
					CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339Nano),
				})
			}
			respTurn.Events = respEvents
		}

		respTurns = append(respTurns, respTurn)
	}

	writeJSON(w, http.StatusOK, map[string]any{"turns": respTurns})
}

func (s *Server) handleThreadSessions(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodGet); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
		return
	}

	provider, err := s.turnAgentFactory(thread)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to resolve agent provider", map[string]any{
			"agent":  thread.AgentID,
			"reason": err.Error(),
		})
		return
	}
	if closer, ok := provider.(io.Closer); ok {
		defer closer.Close()
	}

	lister, ok := provider.(agents.SessionLister)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"threadId":   thread.ThreadID,
			"supported":  false,
			"sessions":   []agents.SessionInfo{},
			"nextCursor": "",
		})
		return
	}

	result, err := lister.ListSessions(r.Context(), agents.SessionListRequest{
		CWD:    thread.CWD,
		Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")),
	})
	if err != nil {
		if errors.Is(err, agents.ErrSessionListUnsupported) || errors.Is(err, agents.ErrSessionLoadUnsupported) {
			writeJSON(w, http.StatusOK, map[string]any{
				"threadId":   thread.ThreadID,
				"supported":  false,
				"sessions":   []agents.SessionInfo{},
				"nextCursor": "",
			})
			return
		}
		writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to query thread sessions", map[string]any{
			"threadId": thread.ThreadID,
			"agent":    thread.AgentID,
			"reason":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"threadId":   thread.ThreadID,
		"supported":  true,
		"sessions":   result.Sessions,
		"nextCursor": result.NextCursor,
	})
}

func (s *Server) handleThreadSessionHistory(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodGet); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, codeInvalidArgument, "sessionId is required", map[string]any{
			"field": "sessionId",
		})
		return
	}

	cachedResult, found, err := s.loadCachedSessionTranscript(r.Context(), thread, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to load session transcript cache", map[string]any{
			"threadId":  thread.ThreadID,
			"sessionId": sessionID,
			"reason":    err.Error(),
		})
		return
	}
	_, configFound, configErr := s.loadStoredSessionConfigOptions(r.Context(), thread, sessionID)
	if configErr != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to load session config cache", map[string]any{
			"threadId":  thread.ThreadID,
			"sessionId": sessionID,
			"reason":    configErr.Error(),
		})
		return
	}
	if found && configFound {
		writeJSON(w, http.StatusOK, map[string]any{
			"threadId":  thread.ThreadID,
			"sessionId": sessionID,
			"supported": true,
			"messages":  cachedResult.Messages,
		})
		return
	}

	provider, err := s.turnAgentFactory(thread)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to resolve agent provider", map[string]any{
			"agent":  thread.AgentID,
			"reason": err.Error(),
		})
		return
	}
	if closer, ok := provider.(io.Closer); ok {
		defer closer.Close()
	}

	loader, ok := provider.(agents.SessionTranscriptLoader)
	if !ok {
		if found {
			writeJSON(w, http.StatusOK, map[string]any{
				"threadId":  thread.ThreadID,
				"sessionId": sessionID,
				"supported": true,
				"messages":  cachedResult.Messages,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"threadId":  thread.ThreadID,
			"sessionId": sessionID,
			"supported": false,
			"messages":  []agents.SessionTranscriptMessage{},
		})
		return
	}

	result, err := loader.LoadSessionTranscript(r.Context(), agents.SessionTranscriptRequest{
		CWD:       thread.CWD,
		SessionID: sessionID,
	})
	if err != nil {
		if found {
			writeJSON(w, http.StatusOK, map[string]any{
				"threadId":  thread.ThreadID,
				"sessionId": sessionID,
				"supported": true,
				"messages":  cachedResult.Messages,
			})
			return
		}
		if errors.Is(err, agents.ErrSessionLoadUnsupported) || errors.Is(err, agents.ErrSessionListUnsupported) {
			writeJSON(w, http.StatusOK, map[string]any{
				"threadId":  thread.ThreadID,
				"sessionId": sessionID,
				"supported": false,
				"messages":  []agents.SessionTranscriptMessage{},
			})
			return
		}
		if errors.Is(err, agents.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "session not found", map[string]any{
				"threadId":  thread.ThreadID,
				"sessionId": sessionID,
			})
			return
		}
		writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to load session transcript", map[string]any{
			"threadId":  thread.ThreadID,
			"sessionId": sessionID,
			"agent":     thread.AgentID,
			"reason":    err.Error(),
		})
		return
	}

	result = agents.CloneSessionTranscriptResult(result)
	s.persistSessionLoadConfigSnapshotBestEffort(r.Context(), &thread, sessionID, result.ConfigOptions)
	if err := s.persistSessionTranscriptCache(r.Context(), thread, sessionID, result); err != nil {
		s.logger.Warn("session_history.cache_persist_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"sessionId", sessionID,
			"reason", err.Error(),
		)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"threadId":  thread.ThreadID,
		"sessionId": sessionID,
		"supported": true,
		"messages":  result.Messages,
	})
}

func (s *Server) loadCachedSessionTranscript(
	ctx context.Context,
	thread storage.Thread,
	sessionID string,
) (agents.SessionTranscriptResult, bool, error) {
	cache, err := s.store.GetSessionTranscriptCache(ctx, thread.AgentID, thread.CWD, sessionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return agents.SessionTranscriptResult{}, false, nil
		}
		return agents.SessionTranscriptResult{}, false, err
	}

	result, err := decodeSessionTranscriptCache(cache.MessagesJSON)
	if err != nil {
		s.logger.Warn("session_history.cache_decode_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"sessionId", sessionID,
			"reason", err.Error(),
		)
		return agents.SessionTranscriptResult{}, false, nil
	}
	return result, true, nil
}

func (s *Server) persistSessionTranscriptCache(
	ctx context.Context,
	thread storage.Thread,
	sessionID string,
	result agents.SessionTranscriptResult,
) error {
	messagesJSON, err := encodeSessionTranscriptCache(result)
	if err != nil {
		return err
	}
	return s.store.UpsertSessionTranscriptCache(ctx, storage.UpsertSessionTranscriptCacheParams{
		AgentID:      thread.AgentID,
		CWD:          thread.CWD,
		SessionID:    sessionID,
		MessagesJSON: messagesJSON,
	})
}

func decodeSessionTranscriptCache(raw string) (agents.SessionTranscriptResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "[]"
	}

	var messages []agents.SessionTranscriptMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return agents.SessionTranscriptResult{}, fmt.Errorf("decode session transcript cache: %w", err)
	}
	return agents.CloneSessionTranscriptResult(agents.SessionTranscriptResult{Messages: messages}), nil
}

func encodeSessionTranscriptCache(result agents.SessionTranscriptResult) (string, error) {
	result = agents.CloneSessionTranscriptResult(result)
	messages := result.Messages
	if len(messages) == 0 {
		messages = []agents.SessionTranscriptMessage{}
	}
	payload, err := json.Marshal(messages)
	if err != nil {
		return "", fmt.Errorf("encode session transcript cache: %w", err)
	}
	return string(payload), nil
}

func (s *Server) handleThreadConfigOptions(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
		return
	}

	switch r.Method {
	case http.MethodGet:
		options, found, err := s.loadStoredThreadConfigOptions(r.Context(), thread)
		if err != nil {
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to load stored thread config options", map[string]any{
				"threadId": thread.ThreadID,
				"reason":   err.Error(),
			})
			return
		}
		if !found {
			options = []agents.ConfigOption{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"threadId":      thread.ThreadID,
			"configOptions": options,
		})
	case http.MethodPost:
		if s.turns.IsThreadActive(thread.ThreadID) {
			writeError(w, http.StatusConflict, codeConflict, "thread has an active turn", map[string]any{"threadId": thread.ThreadID})
			return
		}

		var req struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		if err := decodeJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "invalid JSON body", map[string]any{"reason": err.Error()})
			return
		}
		req.ConfigID = strings.TrimSpace(req.ConfigID)
		req.Value = strings.TrimSpace(req.Value)
		if req.ConfigID == "" {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "configId is required", map[string]any{"field": "configId"})
			return
		}
		if req.Value == "" {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, "value is required", map[string]any{"field": "value"})
			return
		}

		currentOptions, err := s.loadThreadConfigOptionsForUpdate(r.Context(), thread)
		if err != nil {
			if errors.Is(err, errThreadConfigOptionsUnavailable) {
				writeError(w, http.StatusConflict, codeConflict, "thread config options are not available yet", map[string]any{
					"threadId": thread.ThreadID,
				})
				return
			}
			writeError(w, http.StatusServiceUnavailable, codeUpstreamUnavailable, "failed to load thread config options", map[string]any{
				"threadId": thread.ThreadID,
				"reason":   err.Error(),
			})
			return
		}
		if err := validateThreadConfigSelection(currentOptions, req.ConfigID, req.Value); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidArgument, err.Error(), map[string]any{
				"threadId": thread.ThreadID,
				"configId": req.ConfigID,
				"value":    req.Value,
			})
			return
		}

		options, err := s.updatedThreadConfigOptions(r.Context(), thread, currentOptions, req.ConfigID, req.Value)
		if err != nil {
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to update thread config options", map[string]any{
				"threadId": thread.ThreadID,
				"reason":   err.Error(),
			})
			return
		}

		currentModel := acpmodel.CurrentValueForConfig(options, "model")
		agentOptionsJSON, err := withThreadConfigState(thread.AgentOptionsJSON, currentModel, options)
		if err != nil {
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to normalize thread agent options", map[string]any{
				"threadId": thread.ThreadID,
				"reason":   err.Error(),
			})
			return
		}
		if err := s.store.UpdateThreadAgentOptions(r.Context(), thread.ThreadID, agentOptionsJSON); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
				return
			}
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to update thread", map[string]any{"reason": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"threadId":      thread.ThreadID,
			"configOptions": options,
		})
	}
}

func (s *Server) handleThreadSlashCommands(w http.ResponseWriter, r *http.Request, clientID, threadID string) {
	if err := requireMethod(r, http.MethodGet); err != nil {
		writeMethodNotAllowed(w, r)
		return
	}

	thread, ok := s.getOwnedThread(r.Context(), clientID, threadID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "thread not found", map[string]any{})
		return
	}

	commands, found, err := s.loadStoredAgentSlashCommands(r.Context(), thread.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to load slash commands", map[string]any{
			"threadId": thread.ThreadID,
			"agent":    thread.AgentID,
			"reason":   err.Error(),
		})
		return
	}
	if !found {
		s.persistThreadSlashCommandsBestEffort(r.Context(), thread, nil)
		commands, found, err = s.loadStoredAgentSlashCommands(r.Context(), thread.AgentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, codeInternal, "failed to load slash commands", map[string]any{
				"threadId": thread.ThreadID,
				"agent":    thread.AgentID,
				"reason":   err.Error(),
			})
			return
		}
		if !found {
			commands = []agents.SlashCommand{}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"threadId": thread.ThreadID,
		"agentId":  thread.AgentID,
		"commands": commands,
	})
}

func (s *Server) finalizeTurnWithBestEffort(ctx context.Context, turnID, status, stopReason, responseText, errorMessage string) {
	_ = s.store.FinalizeTurn(ctx, storage.FinalizeTurnParams{
		TurnID:       turnID,
		ResponseText: responseText,
		Status:       status,
		StopReason:   stopReason,
		ErrorMessage: errorMessage,
	})
}

func normalizeThreadAgentOptionsForScope(agentOptionsJSON string) string {
	scopeOptions := map[string]any{}
	if sessionID := threadSessionID(agentOptionsJSON); sessionID != "" {
		scopeOptions["sessionId"] = sessionID
	}
	if threadFreshSessionRequested(agentOptionsJSON) {
		scopeOptions[threadAgentOptionFreshSessionKey] = true
	}
	normalized, err := json.Marshal(scopeOptions)
	if err != nil {
		return "{}"
	}
	return string(normalized)
}

func threadAgentScopeKey(thread storage.Thread) string {
	return threadAgentScopeKeyFromOptions(thread.ThreadID, thread.AgentOptionsJSON)
}

func threadAgentScopeKeyFromOptions(threadID, agentOptionsJSON string) string {
	return threadID + "\x00" + normalizeThreadAgentOptionsForScope(agentOptionsJSON)
}

func (s *Server) resolveTurnAgent(thread storage.Thread) (agents.Streamer, error) {
	scopeKey := threadAgentScopeKey(thread)
	sessionID := threadSessionID(thread.AgentOptionsJSON)
	s.agentMu.Lock()
	entry, ok := s.agentsByScope[scopeKey]
	if ok {
		entry.lastUsed = time.Now().UTC()
		provider := entry.provider
		s.agentMu.Unlock()
		return provider, nil
	}
	s.agentMu.Unlock()

	if s.turnAgentFactory == nil {
		return nil, errors.New("turn agent factory is not configured")
	}
	provider, err := s.turnAgentFactory(thread)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, errors.New("turn agent factory returned nil provider")
	}

	var closer io.Closer
	if c, ok := provider.(io.Closer); ok {
		closer = c
	}

	s.agentMu.Lock()
	if existing, exists := s.agentsByScope[scopeKey]; exists {
		existing.lastUsed = time.Now().UTC()
		s.agentMu.Unlock()
		if closer != nil {
			_ = closer.Close()
		}
		return existing.provider, nil
	}
	s.agentsByScope[scopeKey] = &managedAgent{
		scopeKey:  scopeKey,
		threadID:  thread.ThreadID,
		sessionID: sessionID,
		provider:  provider,
		closer:    closer,
		lastUsed:  time.Now().UTC(),
	}
	s.agentMu.Unlock()
	return provider, nil
}

// Close stops background janitor and closes all cached thread agents.
func (s *Server) Close() error {
	select {
	case <-s.janitorStop:
	default:
		close(s.janitorStop)
	}
	<-s.janitorDone
	return s.closeAllThreadAgents()
}

func (s *Server) idleJanitorLoop() {
	defer close(s.janitorDone)
	interval := s.agentIdleTTL / 2
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.janitorStop:
			return
		case <-ticker.C:
			s.reapIdleAgents(time.Now().UTC())
		}
	}
}

func (s *Server) reapIdleAgents(now time.Time) {
	if s.agentIdleTTL <= 0 {
		return
	}

	type reclaimItem struct {
		threadID  string
		scopeKey  string
		sessionID string
		name      string
		idleFor   time.Duration
		closer    io.Closer
	}
	items := make([]reclaimItem, 0)

	s.agentMu.Lock()
	for scopeKey, entry := range s.agentsByScope {
		if s.turns.IsSessionActive(entry.threadID, entry.sessionID) {
			continue
		}
		idleFor := now.Sub(entry.lastUsed)
		if idleFor < s.agentIdleTTL {
			continue
		}
		delete(s.agentsByScope, scopeKey)
		items = append(items, reclaimItem{
			threadID:  entry.threadID,
			scopeKey:  scopeKey,
			sessionID: entry.sessionID,
			name:      entry.provider.Name(),
			idleFor:   idleFor,
			closer:    entry.closer,
		})
	}
	s.agentMu.Unlock()

	for _, item := range items {
		if item.closer != nil {
			_ = item.closer.Close()
		}
		s.logger.Info("agent.idle_reclaimed",
			"threadId", item.threadID,
			"sessionId", item.sessionID,
			"agentName", item.name,
			"idleFor", item.idleFor.String(),
		)
	}
}

func (s *Server) closeAllThreadAgents() error {
	type closeItem struct {
		threadID  string
		sessionID string
		name      string
		closer    io.Closer
	}

	items := make([]closeItem, 0)
	s.agentMu.Lock()
	for scopeKey, entry := range s.agentsByScope {
		items = append(items, closeItem{
			threadID:  entry.threadID,
			sessionID: entry.sessionID,
			name:      entry.provider.Name(),
			closer:    entry.closer,
		})
		delete(s.agentsByScope, scopeKey)
	}
	s.agentMu.Unlock()

	for _, item := range items {
		if item.closer != nil {
			_ = item.closer.Close()
		}
		s.logger.Info("agent.closed",
			"threadId", item.threadID,
			"sessionId", item.sessionID,
			"agentName", item.name,
			"reason", "server_close",
		)
	}
	return nil
}

func (s *Server) closeThreadAgents(threadID, reason string) {
	if strings.TrimSpace(threadID) == "" {
		return
	}

	type closeItem struct {
		sessionID string
		name      string
		closer    io.Closer
	}

	items := make([]closeItem, 0)
	s.agentMu.Lock()
	for scopeKey, entry := range s.agentsByScope {
		if entry.threadID != threadID {
			continue
		}
		items = append(items, closeItem{
			sessionID: entry.sessionID,
			name:      entry.provider.Name(),
			closer:    entry.closer,
		})
		delete(s.agentsByScope, scopeKey)
	}
	s.agentMu.Unlock()

	for _, item := range items {
		if item.closer != nil {
			_ = item.closer.Close()
		}
		s.logger.Info("agent.closed",
			"threadId", threadID,
			"sessionId", item.sessionID,
			"agentName", item.name,
			"reason", reason,
		)
	}
}

func (s *Server) closeThreadAgentScope(threadID, agentOptionsJSON, reason string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}

	sessionID := threadSessionID(agentOptionsJSON)
	if s.turns.IsSessionActive(threadID, sessionID) {
		return
	}

	scopeKey := threadAgentScopeKeyFromOptions(threadID, agentOptionsJSON)

	var item *managedAgent
	s.agentMu.Lock()
	entry, ok := s.agentsByScope[scopeKey]
	if ok {
		delete(s.agentsByScope, scopeKey)
		item = entry
	}
	s.agentMu.Unlock()
	if item == nil {
		return
	}

	if item.closer != nil {
		_ = item.closer.Close()
	}
	s.logger.Info("agent.closed",
		"threadId", item.threadID,
		"sessionId", item.sessionID,
		"agentName", item.provider.Name(),
		"reason", reason,
	)
}

func (s *Server) rebindManagedAgentScope(threadID, fromAgentOptionsJSON, toAgentOptionsJSON string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	fromScopeKey := threadAgentScopeKeyFromOptions(threadID, fromAgentOptionsJSON)
	toScopeKey := threadAgentScopeKeyFromOptions(threadID, toAgentOptionsJSON)
	if fromScopeKey == toScopeKey {
		return nil
	}

	s.agentMu.Lock()
	defer s.agentMu.Unlock()

	entry, ok := s.agentsByScope[fromScopeKey]
	if !ok {
		return nil
	}
	if _, exists := s.agentsByScope[toScopeKey]; exists {
		return nil
	}

	delete(s.agentsByScope, fromScopeKey)
	entry.scopeKey = toScopeKey
	entry.sessionID = threadSessionID(toAgentOptionsJSON)
	entry.lastUsed = time.Now().UTC()
	s.agentsByScope[toScopeKey] = entry
	return nil
}

func (s *Server) buildInjectedPrompt(ctx context.Context, thread storage.Thread, prompt agents.Prompt) (agents.Prompt, error) {
	prompt = agents.NormalizePrompt(prompt)
	if threadSessionID(thread.AgentOptionsJSON) != "" || threadFreshSessionRequested(thread.AgentOptionsJSON) {
		return prompt, nil
	}

	recentTurns, err := s.loadRecentVisibleTurns(ctx, thread.ThreadID)
	if err != nil {
		return agents.Prompt{}, err
	}

	currentInput := prompt.Text()
	if strings.TrimSpace(thread.Summary) == "" && len(recentTurns) == 0 && currentInput == "" {
		return prompt, nil
	}

	content := make([]agents.PromptContent, 0, len(prompt.Content))
	injectedText := composeContextPrompt(
		thread.Summary,
		recentTurns,
		currentInput,
		s.contextMaxChars,
	)
	if strings.TrimSpace(injectedText) != "" {
		content = append(content, agents.PromptContent{
			Type: agents.PromptContentTypeText,
			Text: injectedText,
		})
	}
	for _, item := range prompt.Content {
		if item.Type == agents.PromptContentTypeResourceLink {
			content = append(content, item)
		}
	}
	return agents.NormalizePrompt(agents.Prompt{Content: content}), nil
}

func (s *Server) buildCompactPrompt(ctx context.Context, thread storage.Thread, maxSummaryChars int) (string, error) {
	recentTurns, err := s.loadRecentVisibleTurns(ctx, thread.ThreadID)
	if err != nil {
		return "", err
	}

	instruction := fmt.Sprintf(
		"Please generate an updated rolling summary of the conversation. "+
			"Output plain text only, keep key decisions/constraints, and limit to %d characters.",
		maxSummaryChars,
	)
	return composeContextPrompt(
		thread.Summary,
		recentTurns,
		instruction,
		s.contextMaxChars,
	), nil
}

func (s *Server) loadRecentVisibleTurns(ctx context.Context, threadID string) ([]storage.Turn, error) {
	turns, err := s.store.ListTurnsByThread(ctx, threadID)
	if err != nil {
		return nil, err
	}

	filtered := make([]storage.Turn, 0, len(turns))
	for _, turn := range turns {
		if turn.IsInternal {
			continue
		}
		filtered = append(filtered, turn)
	}

	if len(filtered) > s.contextRecentTurns {
		filtered = filtered[len(filtered)-s.contextRecentTurns:]
	}
	return filtered, nil
}

func composeContextPrompt(summary string, recentTurns []storage.Turn, currentInput string, maxChars int) string {
	summary = strings.TrimSpace(summary)
	currentInput = strings.TrimSpace(currentInput)

	recentCopy := make([]storage.Turn, len(recentTurns))
	copy(recentCopy, recentTurns)

	// Preserve raw user input on the very first turn so slash-command style inputs
	// (for example "/mcp ...") are not masked by context wrapper headings.
	if summary == "" && len(recentCopy) == 0 {
		if maxChars <= 0 || runeLen(currentInput) <= maxChars {
			return currentInput
		}
		return clampToChars(currentInput, maxChars)
	}

	for i := 0; i < 256; i++ {
		prompt := renderContextPrompt(summary, recentCopy, currentInput)
		if maxChars <= 0 || runeLen(prompt) <= maxChars {
			return prompt
		}

		if len(recentCopy) > 0 {
			recentCopy = recentCopy[1:]
			continue
		}

		if runeLen(summary) > 0 {
			summary = clampToChars(summary, runeLen(summary)-maxInt(1, runeLen(summary)/4))
			continue
		}

		if runeLen(currentInput) > 0 {
			currentInput = truncateFromEnd(currentInput, runeLen(currentInput)-maxInt(1, runeLen(currentInput)/4))
			continue
		}

		return clampToChars(prompt, maxChars)
	}

	return clampToChars(renderContextPrompt(summary, recentCopy, currentInput), maxChars)
}

func renderContextPrompt(summary string, recentTurns []storage.Turn, currentInput string) string {
	var builder strings.Builder
	builder.WriteString("[Conversation Summary]\n")
	if summary == "" {
		builder.WriteString("(empty)")
	} else {
		builder.WriteString(summary)
	}

	builder.WriteString("\n\n[Recent Turns]\n")
	if len(recentTurns) == 0 {
		builder.WriteString("(none)")
	} else {
		for _, turn := range recentTurns {
			builder.WriteString("User: ")
			builder.WriteString(strings.TrimSpace(turn.RequestText))
			builder.WriteString("\nAssistant: ")
			builder.WriteString(strings.TrimSpace(turn.ResponseText))
			builder.WriteString("\n")
		}
		builder.WriteString("----")
	}

	builder.WriteString("\n\n[Current User Input]\n")
	builder.WriteString(currentInput)
	return builder.String()
}

func clampToChars(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars])
}

func truncateFromEnd(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[len(runes)-maxChars:])
}

func runeLen(text string) int {
	return len([]rune(text))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func classifyStreamErrorCode(err error) string {
	if err == nil {
		return codeInternal
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return codeTimeout
	}
	if errors.Is(err, context.Canceled) {
		return codeTimeout
	}
	return codeUpstreamUnavailable
}

func (s *Server) getOwnedThread(ctx context.Context, clientID, threadID string) (storage.Thread, bool) {
	thread, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return storage.Thread{}, false
	}
	if thread.ClientID != clientID {
		return storage.Thread{}, false
	}
	return thread, true
}

type threadResponse struct {
	ThreadID     string          `json:"threadId"`
	Agent        string          `json:"agent"`
	CWD          string          `json:"cwd"`
	Title        string          `json:"title"`
	AgentOptions json.RawMessage `json:"agentOptions"`
	Summary      string          `json:"summary"`
	CreatedAt    string          `json:"createdAt"`
	UpdatedAt    string          `json:"updatedAt"`
}

type turnHistoryResponse struct {
	TurnID       string                 `json:"turnId"`
	RequestText  string                 `json:"requestText"`
	ResponseText string                 `json:"responseText"`
	IsInternal   bool                   `json:"isInternal,omitempty"`
	Status       string                 `json:"status"`
	StopReason   string                 `json:"stopReason"`
	ErrorMessage string                 `json:"errorMessage"`
	CreatedAt    string                 `json:"createdAt"`
	CompletedAt  *string                `json:"completedAt,omitempty"`
	Events       []eventHistoryResponse `json:"events,omitempty"`
}

type eventHistoryResponse struct {
	EventID   int64           `json:"eventId"`
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"createdAt"`
}

var (
	errPermissionNotFound        = errors.New("permission not found")
	errPermissionAlreadyResolved = errors.New("permission already resolved")
	errPermissionInvalidOption   = errors.New("permission option is invalid")
	errPermissionOutcomeRequired = errors.New("permission outcome is required")
)

type pendingPermission struct {
	clientID string

	options map[string]agents.PermissionOption

	ch   chan agents.PermissionResponse
	once sync.Once
}

type managedAgent struct {
	scopeKey  string
	threadID  string
	sessionID string
	provider  agents.Streamer
	closer    io.Closer
	lastUsed  time.Time
}

type threadConfigSelectionState interface {
	CurrentModelID() string
	CurrentConfigOverrides() map[string]string
}

func newPendingPermission(clientID string, options []agents.PermissionOption) *pendingPermission {
	optionMap := make(map[string]agents.PermissionOption, len(options))
	for _, option := range options {
		optionID := strings.TrimSpace(option.OptionID)
		if optionID == "" {
			continue
		}
		optionMap[optionID] = agents.PermissionOption{
			OptionID: optionID,
			Name:     strings.TrimSpace(option.Name),
			Kind:     strings.TrimSpace(option.Kind),
		}
	}
	return &pendingPermission{
		clientID: clientID,
		options:  optionMap,
		ch:       make(chan agents.PermissionResponse, 1),
	}
}

func (p *pendingPermission) Resolve(response agents.PermissionResponse) bool {
	resolved := false
	p.once.Do(func() {
		p.ch <- normalizePermissionResponse(response)
		close(p.ch)
		resolved = true
	})
	return resolved
}

func (p *pendingPermission) normalizeDecision(response agents.PermissionResponse) (agents.PermissionResponse, error) {
	response = normalizePermissionResponse(response)
	if response.SelectedOptionID != "" {
		if len(p.options) > 0 {
			option, ok := p.options[response.SelectedOptionID]
			if !ok {
				return agents.PermissionResponse{}, errPermissionInvalidOption
			}
			if response.Outcome == "" {
				if inferred, ok := permissionOutcomeForOptionKind(option.Kind); ok {
					response.Outcome = inferred
				}
			}
		}
	}
	if response.Outcome == "" {
		return agents.PermissionResponse{}, errPermissionOutcomeRequired
	}
	return response, nil
}

func toThreadResponse(thread storage.Thread) (threadResponse, error) {
	raw, err := sanitizeThreadAgentOptionsForResponse(thread.AgentOptionsJSON)
	if err != nil {
		return threadResponse{}, fmt.Errorf("invalid agent_options_json for thread %s", thread.ThreadID)
	}

	return threadResponse{
		ThreadID:     thread.ThreadID,
		Agent:        thread.AgentID,
		CWD:          thread.CWD,
		Title:        thread.Title,
		AgentOptions: raw,
		Summary:      thread.Summary,
		CreatedAt:    thread.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    thread.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func parseThreadPath(path string) (threadID, subresource string, ok bool) {
	const prefix = "/v1/threads/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	threadID = parts[0]
	if len(parts) == 1 {
		return threadID, "", true
	}
	if len(parts) == 2 && parts[1] != "" {
		return threadID, parts[1], true
	}
	return "", "", false
}

func parseAttachmentPath(path string) (attachmentID string, ok bool) {
	const prefix = "/attachments/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	return raw, true
}

func parseAgentModelsPath(path string) (agentID string, ok bool) {
	const prefix = "/v1/agents/"
	const suffix = "/models"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	raw = strings.Trim(raw, "/")
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	return raw, true
}

func parsePermissionPath(path string) (permissionID string, ok bool) {
	const prefix = "/v1/permissions/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	return raw, true
}

func parseTurnCancelPath(path string) (turnID string, ok bool) {
	const prefix = "/v1/turns/"
	const suffix = "/cancel"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	raw = strings.Trim(raw, "/")
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	return raw, true
}

func normalizePermissionOutcome(raw string) (agents.PermissionOutcome, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(agents.PermissionOutcomeApproved):
		return agents.PermissionOutcomeApproved, true
	case string(agents.PermissionOutcomeDeclined):
		return agents.PermissionOutcomeDeclined, true
	case string(agents.PermissionOutcomeCancelled):
		return agents.PermissionOutcomeCancelled, true
	default:
		return "", false
	}
}

func permissionOutcomeForOptionKind(raw string) (agents.PermissionOutcome, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "allow", "allow_once", "allow_always", "approve", "approved":
		return agents.PermissionOutcomeApproved, true
	case "deny", "denied", "decline", "declined", "reject", "reject_once", "reject_always":
		return agents.PermissionOutcomeDeclined, true
	case "cancel", "cancelled", "canceled":
		return agents.PermissionOutcomeCancelled, true
	default:
		return "", false
	}
}

func (s *Server) nextPermissionID(requestID string) string {
	seq := atomic.AddUint64(&s.permissionSeq, 1)
	safeRequestID := sanitizePermissionIDComponent(requestID)
	if safeRequestID == "" {
		return fmt.Sprintf("perm_%d", seq)
	}
	return fmt.Sprintf("perm_%s_%d", safeRequestID, seq)
}

func sanitizePermissionIDComponent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func (s *Server) registerPermission(permissionID string, pending *pendingPermission) {
	s.permissionsMu.Lock()
	s.permissions[permissionID] = pending
	s.permissionsMu.Unlock()
}

func (s *Server) unregisterPermission(permissionID string, pending *pendingPermission) {
	s.permissionsMu.Lock()
	current, ok := s.permissions[permissionID]
	if ok && current == pending {
		delete(s.permissions, permissionID)
	}
	s.permissionsMu.Unlock()
}

func permissionFailClosedResponse() agents.PermissionResponse {
	return agents.PermissionResponse{Outcome: agents.PermissionOutcomeDeclined}
}

func normalizePermissionResponse(response agents.PermissionResponse) agents.PermissionResponse {
	response.SelectedOptionID = strings.TrimSpace(response.SelectedOptionID)
	if response.Outcome == "" {
		return response
	}
	switch response.Outcome {
	case agents.PermissionOutcomeApproved, agents.PermissionOutcomeDeclined, agents.PermissionOutcomeCancelled:
		return response
	default:
		return agents.PermissionResponse{
			Outcome:          agents.PermissionOutcomeDeclined,
			SelectedOptionID: response.SelectedOptionID,
		}
	}
}

func (s *Server) waitPermissionResponse(ctx context.Context, pending *pendingPermission) agents.PermissionResponse {
	timeout := s.permissionTimeout
	if timeout <= 0 {
		timeout = defaultPermissionTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case response := <-pending.ch:
		if response.Outcome == "" {
			return permissionFailClosedResponse()
		}
		return response
	case <-timer.C:
		pending.Resolve(permissionFailClosedResponse())
	case <-ctx.Done():
		pending.Resolve(permissionFailClosedResponse())
	}

	response, ok := <-pending.ch
	if !ok || response.Outcome == "" {
		return permissionFailClosedResponse()
	}
	return response
}

func (s *Server) resolvePermission(permissionID, clientID string, response agents.PermissionResponse) (agents.PermissionResponse, error) {
	s.permissionsMu.Lock()
	pending, ok := s.permissions[permissionID]
	s.permissionsMu.Unlock()
	if !ok {
		return agents.PermissionResponse{}, errPermissionNotFound
	}
	if pending.clientID != clientID {
		return agents.PermissionResponse{}, errPermissionNotFound
	}
	normalized, err := pending.normalizeDecision(response)
	if err != nil {
		return agents.PermissionResponse{}, err
	}
	if !pending.Resolve(normalized) {
		return agents.PermissionResponse{}, errPermissionAlreadyResolved
	}
	return normalized, nil
}

func (s *Server) loadStoredAgentModels(ctx context.Context, agentID string) ([]agents.ModelOption, bool, error) {
	catalogs, err := s.store.ListAgentConfigCatalogsByAgent(ctx, agentID)
	if err != nil {
		return nil, false, err
	}
	if len(catalogs) == 0 {
		return nil, false, nil
	}

	models := make([]agents.ModelOption, 0)
	for _, catalog := range catalogs {
		options, err := decodeStoredConfigOptions(catalog.ConfigOptionsJSON)
		if err != nil {
			s.logger.Warn("config_catalog.decode_failed",
				"agent", agentID,
				"modelId", catalog.ModelID,
				"reason", err.Error(),
			)
			continue
		}
		modelOption, ok := acpmodel.FindModelConfigOption(options)
		if !ok {
			continue
		}
		for _, value := range modelOption.Options {
			modelID := strings.TrimSpace(value.Value)
			if modelID == "" {
				continue
			}
			name := strings.TrimSpace(value.Name)
			if name == "" {
				name = modelID
			}
			models = append(models, agents.ModelOption{ID: modelID, Name: name})
		}
	}

	models = acpmodel.NormalizeModelOptions(models)
	if len(models) == 0 {
		return nil, false, nil
	}
	return models, true, nil
}

func (s *Server) loadStoredThreadConfigOptions(ctx context.Context, thread storage.Thread) ([]agents.ConfigOption, bool, error) {
	modelID, overrides := threadConfigSelections(thread.AgentOptionsJSON)
	if modelID != "" {
		catalog, err := s.store.GetAgentConfigCatalog(ctx, thread.AgentID, modelID)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}

		options, err := decodeStoredConfigOptions(catalog.ConfigOptionsJSON)
		if err != nil {
			return nil, false, err
		}
		return applyThreadConfigSelections(options, modelID, overrides), true, nil
	}

	sessionID := threadSessionID(thread.AgentOptionsJSON)
	if sessionID == "" {
		return nil, false, nil
	}

	return s.loadStoredSessionConfigOptions(ctx, thread, sessionID)
}

func (s *Server) loadStoredSessionConfigOptions(
	ctx context.Context,
	thread storage.Thread,
	sessionID string,
) ([]agents.ConfigOption, bool, error) {
	cache, err := s.store.GetSessionConfigCache(ctx, thread.AgentID, thread.CWD, sessionID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	options, err := decodeStoredConfigOptions(cache.ConfigOptionsJSON)
	if err != nil {
		return nil, false, err
	}
	return options, true, nil
}

func (s *Server) loadStoredAgentConfigOptions(
	ctx context.Context,
	agentID, modelID string,
) ([]agents.ConfigOption, bool, error) {
	lookupModelID := strings.TrimSpace(modelID)
	if lookupModelID == "" {
		lookupModelID = storage.DefaultAgentConfigCatalogModelID
	}

	catalog, err := s.store.GetAgentConfigCatalog(ctx, agentID, lookupModelID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	options, err := decodeStoredConfigOptions(catalog.ConfigOptionsJSON)
	if err != nil {
		return nil, false, err
	}
	return options, true, nil
}

func (s *Server) loadThreadConfigOptionsForUpdate(
	ctx context.Context,
	thread storage.Thread,
) ([]agents.ConfigOption, error) {
	options, found, err := s.loadStoredThreadConfigOptions(ctx, thread)
	if err != nil {
		return nil, err
	}
	if found {
		return options, nil
	}
	return nil, errThreadConfigOptionsUnavailable
}

func (s *Server) loadStoredAgentSlashCommands(ctx context.Context, agentID string) ([]agents.SlashCommand, bool, error) {
	stored, err := s.store.GetAgentSlashCommands(ctx, agentID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	commands, err := decodeStoredSlashCommands(stored.CommandsJSON)
	if err != nil {
		return nil, false, err
	}
	return commands, true, nil
}

func (s *Server) persistThreadSlashCommandsBestEffort(ctx context.Context, thread storage.Thread, provider any) {
	if _, found, err := s.loadStoredAgentSlashCommands(ctx, thread.AgentID); err == nil && found {
		return
	}

	if provider == nil {
		resolved, err := s.resolveTurnAgent(thread)
		if err != nil {
			s.logger.Warn("thread.slash_commands_resolve_failed",
				"threadId", thread.ThreadID,
				"agent", thread.AgentID,
				"reason", err.Error(),
			)
			return
		}
		provider = resolved
	}

	reader, ok := provider.(agents.SlashCommandsProvider)
	if !ok {
		return
	}

	commands, known, err := reader.SlashCommands(ctx)
	if err != nil {
		s.logger.Warn("thread.slash_commands_probe_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"reason", err.Error(),
		)
		return
	}
	if !known {
		return
	}
	if err := s.persistAgentSlashCommands(ctx, thread.AgentID, commands); err != nil {
		s.logger.Warn("thread.slash_commands_persist_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"reason", err.Error(),
		)
	}
}

func (s *Server) persistAgentConfigCatalog(
	ctx context.Context,
	agentID string,
	agentOptionsJSON string,
	options []agents.ConfigOption,
) error {
	modelID, _ := threadConfigSelections(agentOptionsJSON)
	if currentModel := strings.TrimSpace(acpmodel.CurrentValueForConfig(options, "model")); currentModel != "" {
		modelID = currentModel
	}
	if strings.TrimSpace(modelID) == "" {
		modelID = storage.DefaultAgentConfigCatalogModelID
	}

	configOptionsJSON, err := encodeStoredConfigOptions(options)
	if err != nil {
		return err
	}

	return s.store.UpsertAgentConfigCatalog(ctx, storage.UpsertAgentConfigCatalogParams{
		AgentID:           agentID,
		ModelID:           modelID,
		ConfigOptionsJSON: configOptionsJSON,
	})
}

func (s *Server) updatedThreadConfigOptions(
	ctx context.Context,
	thread storage.Thread,
	currentOptions []agents.ConfigOption,
	configID, value string,
) ([]agents.ConfigOption, error) {
	modelID, overrides := threadConfigSelections(thread.AgentOptionsJSON)
	nextModelID := modelID
	nextOverrides := cloneThreadConfigOverrides(overrides)

	if strings.EqualFold(strings.TrimSpace(configID), "model") {
		nextModelID = strings.TrimSpace(value)
	} else {
		if nextOverrides == nil {
			nextOverrides = make(map[string]string)
		}
		nextOverrides[strings.TrimSpace(configID)] = strings.TrimSpace(value)
	}

	baseOptions := currentOptions
	if strings.EqualFold(strings.TrimSpace(configID), "model") {
		storedOptions, found, err := s.loadStoredAgentConfigOptions(ctx, thread.AgentID, nextModelID)
		if err != nil {
			return nil, err
		}
		if found {
			baseOptions = storedOptions
		} else {
			baseOptions = modelOnlyThreadConfigOptions(currentOptions)
			nextOverrides = nil
		}
	}

	return applyThreadConfigSelections(baseOptions, nextModelID, nextOverrides), nil
}

func (s *Server) syncThreadConfigSelections(
	ctx context.Context,
	thread storage.Thread,
	provider agents.Streamer,
) error {
	manager, ok := provider.(agents.ConfigOptionManager)
	if !ok {
		return nil
	}
	state, ok := provider.(threadConfigSelectionState)
	if !ok {
		return nil
	}

	desiredModelID, desiredOverrides := threadConfigSelections(thread.AgentOptionsJSON)
	currentModelID := strings.TrimSpace(state.CurrentModelID())
	if desiredModelID != "" && desiredModelID != currentModelID {
		options, err := manager.SetConfigOption(ctx, "model", desiredModelID)
		if err != nil {
			return fmt.Errorf("apply model config before turn: %w", err)
		}
		s.persistAgentConfigCatalogBestEffort(ctx, thread, options)
	}

	currentOverrides := state.CurrentConfigOverrides()
	if desiredModelID != "" && desiredModelID != currentModelID {
		currentOverrides = state.CurrentConfigOverrides()
	}
	configIDs := make([]string, 0, len(desiredOverrides))
	for configID := range desiredOverrides {
		configIDs = append(configIDs, configID)
	}
	sort.Strings(configIDs)

	for _, configID := range configIDs {
		value := strings.TrimSpace(desiredOverrides[configID])
		if value == "" || value == strings.TrimSpace(currentOverrides[configID]) {
			continue
		}
		options, err := manager.SetConfigOption(ctx, configID, value)
		if err != nil {
			return fmt.Errorf("apply %s config before turn: %w", configID, err)
		}
		s.persistAgentConfigCatalogBestEffort(ctx, thread, options)
	}

	return nil
}

func (s *Server) persistAgentConfigCatalogBestEffort(
	ctx context.Context,
	thread storage.Thread,
	options []agents.ConfigOption,
) {
	normalized := acpmodel.NormalizeConfigOptions(options)
	if len(normalized) == 0 {
		return
	}
	if err := s.persistAgentConfigCatalog(ctx, thread.AgentID, thread.AgentOptionsJSON, normalized); err != nil {
		s.logger.Warn("config_catalog.persist_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"reason", err.Error(),
		)
	}
}

func (s *Server) persistThreadConfigSnapshotBestEffort(
	ctx context.Context,
	thread *storage.Thread,
	options []agents.ConfigOption,
) {
	if thread == nil {
		return
	}

	normalized := acpmodel.NormalizeConfigOptions(options)
	if len(normalized) == 0 {
		return
	}

	currentModel := strings.TrimSpace(acpmodel.CurrentValueForConfig(normalized, "model"))
	nextAgentOptionsJSON, err := withThreadConfigState(thread.AgentOptionsJSON, currentModel, normalized)
	if err != nil {
		s.logger.Warn("thread.config_snapshot_encode_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"reason", err.Error(),
		)
		return
	}
	if nextAgentOptionsJSON != thread.AgentOptionsJSON {
		if err := s.store.UpdateThreadAgentOptions(ctx, thread.ThreadID, nextAgentOptionsJSON); err != nil {
			s.logger.Warn("thread.config_snapshot_persist_failed",
				"threadId", thread.ThreadID,
				"agent", thread.AgentID,
				"reason", err.Error(),
			)
			return
		}
		thread.AgentOptionsJSON = nextAgentOptionsJSON
	}
	if err := s.persistAgentConfigCatalog(ctx, thread.AgentID, thread.AgentOptionsJSON, normalized); err != nil {
		s.logger.Warn("config_catalog.persist_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"reason", err.Error(),
		)
	}
	s.persistSessionConfigSnapshotBestEffort(ctx, *thread, normalized)
}

func (s *Server) persistThreadSessionConfigSnapshotBestEffort(ctx context.Context, thread storage.Thread) {
	options, found, err := s.loadStoredThreadConfigOptions(ctx, thread)
	if err != nil {
		s.logger.Warn("thread.session_config_restore_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"sessionId", threadSessionID(thread.AgentOptionsJSON),
			"reason", err.Error(),
		)
		return
	}
	if !found {
		return
	}
	s.persistSessionConfigSnapshotBestEffort(ctx, thread, options)
}

func (s *Server) persistSessionConfigSnapshotBestEffort(
	ctx context.Context,
	thread storage.Thread,
	options []agents.ConfigOption,
) {
	s.persistSessionConfigSnapshotForSessionIDBestEffort(ctx, thread, threadSessionID(thread.AgentOptionsJSON), options)
}

func (s *Server) persistSessionConfigSnapshotForSessionIDBestEffort(
	ctx context.Context,
	thread storage.Thread,
	sessionID string,
	options []agents.ConfigOption,
) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}

	configOptionsJSON, err := encodeStoredConfigOptions(options)
	if err != nil {
		s.logger.Warn("thread.session_config_encode_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"sessionId", sessionID,
			"reason", err.Error(),
		)
		return
	}

	if err := s.store.UpsertSessionConfigCache(ctx, storage.UpsertSessionConfigCacheParams{
		AgentID:           thread.AgentID,
		CWD:               thread.CWD,
		SessionID:         sessionID,
		ConfigOptionsJSON: configOptionsJSON,
	}); err != nil {
		s.logger.Warn("thread.session_config_persist_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"sessionId", sessionID,
			"reason", err.Error(),
		)
	}
}

func (s *Server) persistSessionLoadConfigSnapshotBestEffort(
	ctx context.Context,
	thread *storage.Thread,
	sessionID string,
	options []agents.ConfigOption,
) {
	if thread == nil {
		return
	}

	normalized := acpmodel.NormalizeConfigOptions(options)
	if len(normalized) == 0 {
		return
	}

	if threadSessionID(thread.AgentOptionsJSON) == strings.TrimSpace(sessionID) {
		s.persistThreadConfigSnapshotBestEffort(ctx, thread, normalized)
		return
	}

	if err := s.persistAgentConfigCatalog(ctx, thread.AgentID, thread.AgentOptionsJSON, normalized); err != nil {
		s.logger.Warn("config_catalog.persist_failed",
			"threadId", thread.ThreadID,
			"agent", thread.AgentID,
			"sessionId", sessionID,
			"reason", err.Error(),
		)
	}
	s.persistSessionConfigSnapshotForSessionIDBestEffort(ctx, *thread, sessionID, normalized)
}

func (s *Server) persistAgentSlashCommands(
	ctx context.Context,
	agentID string,
	commands []agents.SlashCommand,
) error {
	commandsJSON, err := encodeStoredSlashCommands(commands)
	if err != nil {
		return err
	}

	return s.store.UpsertAgentSlashCommands(ctx, storage.UpsertAgentSlashCommandsParams{
		AgentID:      agentID,
		CommandsJSON: commandsJSON,
	})
}

func normalizeAgentOptions(raw json.RawMessage) (string, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "{}", nil
	}

	var objectValue map[string]any
	if err := json.Unmarshal(raw, &objectValue); err != nil {
		return "", err
	}

	normalized, err := json.Marshal(objectValue)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func withThreadConfigState(agentOptionsJSON, modelID string, options []agents.ConfigOption) (string, error) {
	modelID = strings.TrimSpace(modelID)

	objectValue := map[string]any{}
	trimmed := strings.TrimSpace(agentOptionsJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &objectValue); err != nil {
			return "", err
		}
	}

	if modelID == "" {
		delete(objectValue, "modelId")
	} else {
		objectValue["modelId"] = modelID
	}

	configOverrides := configOverridesFromOptions(options)
	if len(configOverrides) == 0 {
		delete(objectValue, "configOverrides")
	} else {
		objectValue["configOverrides"] = configOverrides
	}

	normalized, err := json.Marshal(objectValue)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func withoutThreadConfigState(agentOptionsJSON string) (string, error) {
	objectValue := map[string]any{}
	trimmed := strings.TrimSpace(agentOptionsJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &objectValue); err != nil {
			return "", err
		}
	}

	delete(objectValue, "modelId")
	delete(objectValue, "configOverrides")

	normalized, err := json.Marshal(objectValue)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func configOverridesFromOptions(options []agents.ConfigOption) map[string]string {
	overrides := make(map[string]string, len(options))
	for _, option := range options {
		configID := strings.TrimSpace(option.ID)
		if configID == "" || strings.EqualFold(configID, "model") {
			continue
		}
		value := strings.TrimSpace(option.CurrentValue)
		if value == "" {
			continue
		}
		overrides[configID] = value
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

func cloneThreadConfigOverrides(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for configID, value := range input {
		cloned[configID] = value
	}
	return cloned
}

func validateThreadConfigSelection(options []agents.ConfigOption, configID, value string) error {
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return errors.New("configId is required")
	}
	if value == "" {
		return errors.New("value is required")
	}

	var option *agents.ConfigOption
	for i := range options {
		candidateID := strings.TrimSpace(options[i].ID)
		category := strings.TrimSpace(options[i].Category)
		if strings.EqualFold(candidateID, configID) {
			option = &options[i]
			break
		}
		if strings.EqualFold(configID, "model") && strings.EqualFold(category, "model") {
			option = &options[i]
			break
		}
	}
	if option == nil {
		return fmt.Errorf("config option %q is not available", configID)
	}
	if len(option.Options) == 0 {
		return nil
	}
	for _, candidate := range option.Options {
		if strings.EqualFold(strings.TrimSpace(candidate.Value), value) {
			return nil
		}
	}
	return fmt.Errorf("value %q is not available for config option %q", value, configID)
}

func decodeStoredConfigOptions(raw string) ([]agents.ConfigOption, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var options []agents.ConfigOption
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return nil, fmt.Errorf("decode stored config options: %w", err)
	}
	return acpmodel.NormalizeConfigOptions(options), nil
}

func encodeStoredConfigOptions(options []agents.ConfigOption) (string, error) {
	normalized := acpmodel.NormalizeConfigOptions(options)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode stored config options: %w", err)
	}
	return string(encoded), nil
}

func decodeStoredSlashCommands(raw string) ([]agents.SlashCommand, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var commands []agents.SlashCommand
	if err := json.Unmarshal([]byte(raw), &commands); err != nil {
		return nil, fmt.Errorf("decode stored slash commands: %w", err)
	}
	return agents.CloneSlashCommands(commands), nil
}

func encodeStoredSlashCommands(commands []agents.SlashCommand) (string, error) {
	normalized := agents.CloneSlashCommands(commands)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode stored slash commands: %w", err)
	}
	return string(encoded), nil
}

func threadConfigSelections(agentOptionsJSON string) (string, map[string]string) {
	var raw struct {
		ModelID         string         `json:"modelId"`
		ConfigOverrides map[string]any `json:"configOverrides"`
	}
	if strings.TrimSpace(agentOptionsJSON) == "" {
		return "", nil
	}
	if err := json.Unmarshal([]byte(agentOptionsJSON), &raw); err != nil {
		return "", nil
	}

	overrides := make(map[string]string, len(raw.ConfigOverrides))
	for rawID, rawValue := range raw.ConfigOverrides {
		configID := strings.TrimSpace(rawID)
		if configID == "" {
			continue
		}
		value, ok := rawValue.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		overrides[configID] = value
	}
	if len(overrides) == 0 {
		overrides = nil
	}

	return strings.TrimSpace(raw.ModelID), overrides
}

func threadSessionID(agentOptionsJSON string) string {
	var raw struct {
		SessionID string `json:"sessionId"`
	}
	if strings.TrimSpace(agentOptionsJSON) == "" {
		return ""
	}
	if err := json.Unmarshal([]byte(agentOptionsJSON), &raw); err != nil {
		return ""
	}
	return strings.TrimSpace(raw.SessionID)
}

func threadFreshSessionRequested(agentOptionsJSON string) bool {
	var raw struct {
		FreshSession bool `json:"_ngentFreshSession"`
	}
	if strings.TrimSpace(agentOptionsJSON) == "" {
		return false
	}
	if err := json.Unmarshal([]byte(agentOptionsJSON), &raw); err != nil {
		return false
	}
	return raw.FreshSession
}

func withoutThreadSessionID(agentOptionsJSON string) (string, error) {
	objectValue := map[string]any{}
	trimmed := strings.TrimSpace(agentOptionsJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &objectValue); err != nil {
			return "", err
		}
	}

	delete(objectValue, "sessionId")
	delete(objectValue, threadAgentOptionFreshSessionKey)
	normalized, err := json.Marshal(objectValue)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func isSessionOnlyAgentOptionsUpdate(currentAgentOptionsJSON, nextAgentOptionsJSON string) (bool, error) {
	currentWithoutSessionID, err := withoutThreadSessionID(currentAgentOptionsJSON)
	if err != nil {
		return false, err
	}
	nextWithoutSessionID, err := withoutThreadSessionID(nextAgentOptionsJSON)
	if err != nil {
		return false, err
	}
	if currentWithoutSessionID == nextWithoutSessionID {
		return threadSessionID(currentAgentOptionsJSON) != threadSessionID(nextAgentOptionsJSON) ||
			threadFreshSessionRequested(currentAgentOptionsJSON) != threadFreshSessionRequested(nextAgentOptionsJSON), nil
	}

	currentComparable, err := withoutThreadConfigState(currentWithoutSessionID)
	if err != nil {
		return false, err
	}
	nextComparable, err := withoutThreadConfigState(nextWithoutSessionID)
	if err != nil {
		return false, err
	}
	if currentComparable != nextComparable {
		return false, nil
	}

	return threadSessionID(currentAgentOptionsJSON) != threadSessionID(nextAgentOptionsJSON) ||
		threadFreshSessionRequested(currentAgentOptionsJSON) != threadFreshSessionRequested(nextAgentOptionsJSON), nil
}

func withThreadSessionID(agentOptionsJSON, sessionID string) (string, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	previousNormalized := normalizeThreadAgentOptionsForScope(agentOptionsJSON)

	objectValue := map[string]any{}
	trimmed := strings.TrimSpace(agentOptionsJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &objectValue); err != nil {
			return "", false, err
		}
	}

	if sessionID == "" {
		delete(objectValue, "sessionId")
	} else {
		objectValue["sessionId"] = sessionID
		delete(objectValue, threadAgentOptionFreshSessionKey)
	}

	normalized, err := json.Marshal(objectValue)
	if err != nil {
		return "", false, err
	}
	nextNormalized := string(normalized)
	return nextNormalized, previousNormalized != nextNormalized, nil
}

func withThreadFreshSessionRequested(agentOptionsJSON string, fresh bool) (string, bool, error) {
	previousNormalized := normalizeThreadAgentOptionsForScope(agentOptionsJSON)

	objectValue := map[string]any{}
	trimmed := strings.TrimSpace(agentOptionsJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &objectValue); err != nil {
			return "", false, err
		}
	}

	if fresh {
		objectValue[threadAgentOptionFreshSessionKey] = true
	} else {
		delete(objectValue, threadAgentOptionFreshSessionKey)
	}

	normalized, err := json.Marshal(objectValue)
	if err != nil {
		return "", false, err
	}
	nextNormalized := string(normalized)
	return nextNormalized, previousNormalized != nextNormalized, nil
}

func sanitizeThreadAgentOptionsForResponse(agentOptionsJSON string) (json.RawMessage, error) {
	objectValue := map[string]any{}
	trimmed := strings.TrimSpace(agentOptionsJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &objectValue); err != nil {
			return nil, err
		}
	}

	delete(objectValue, threadAgentOptionFreshSessionKey)
	normalized, err := json.Marshal(objectValue)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(normalized), nil
}

func applyThreadConfigSelections(
	options []agents.ConfigOption,
	modelID string,
	overrides map[string]string,
) []agents.ConfigOption {
	cloned := acpmodel.CloneConfigOptions(options)
	modelID = strings.TrimSpace(modelID)

	for i := range cloned {
		configID := strings.TrimSpace(cloned[i].ID)
		if configID == "" {
			continue
		}
		if strings.EqualFold(configID, "model") || strings.EqualFold(strings.TrimSpace(cloned[i].Category), "model") {
			if modelID != "" {
				cloned[i].CurrentValue = modelID
			}
			continue
		}
		if len(overrides) == 0 {
			continue
		}
		if value := strings.TrimSpace(overrides[configID]); value != "" {
			cloned[i].CurrentValue = value
		}
	}

	return acpmodel.NormalizeConfigOptions(cloned)
}

func modelOnlyThreadConfigOptions(options []agents.ConfigOption) []agents.ConfigOption {
	modelOption, ok := acpmodel.FindModelConfigOption(options)
	if !ok {
		return nil
	}
	return acpmodel.NormalizeConfigOptions([]agents.ConfigOption{modelOption})
}

func isPathAllowed(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func sortedAgentIDs(allowed map[string]struct{}) []string {
	if len(allowed) == 0 {
		return []string{}
	}
	ids := make([]string, 0, len(allowed))
	for id := range allowed {
		ids = append(ids, id)
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}

func newThreadID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("th_%d", time.Now().UTC().UnixMicro())
	}
	return fmt.Sprintf("th_%d_%s", time.Now().UTC().UnixMicro(), hex.EncodeToString(buf))
}

func newTurnID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("tu_%d", time.Now().UTC().UnixMicro())
	}
	return fmt.Sprintf("tu_%d_%s", time.Now().UTC().UnixMicro(), hex.EncodeToString(buf))
}

func parseBoolQuery(r *http.Request, key string) bool {
	value := strings.TrimSpace(strings.ToLower(r.URL.Query().Get(key)))
	return value == "1" || value == "true" || value == "yes"
}

func requireMethod(r *http.Request, method string) error {
	if r.Method != method {
		return errors.New("method not allowed")
	}
	return nil
}

func decodeJSONBody(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("extra JSON values are not allowed")
	}
	return nil
}

func (s *Server) decodeTurnCreateRequest(r *http.Request) (turnCreateRequest, error) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return decodeMultipartTurnCreateRequest(r, s.dataDir)
	}

	var req struct {
		Input  string `json:"input"`
		Stream bool   `json:"stream"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		return turnCreateRequest{}, err
	}

	return turnCreateRequest{
		Stream: req.Stream,
		Prompt: agents.TextPrompt(req.Input),
	}, nil
}

func decodeMultipartTurnCreateRequest(r *http.Request, dataDir string) (turnCreateRequest, error) {
	if err := r.ParseMultipartForm(maxTurnMultipartMemory); err != nil {
		return turnCreateRequest{}, err
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	text := strings.TrimSpace(r.FormValue("input"))
	stream := parseFormBoolValue(r.FormValue("stream"))
	attachments, err := persistTurnAttachments(dataDir, r.MultipartForm.File["attachments"])
	if err != nil {
		return turnCreateRequest{}, err
	}

	content := make([]agents.PromptContent, 0, len(attachments)+1)
	if text != "" {
		content = append(content, agents.PromptContent{
			Type: agents.PromptContentTypeText,
			Text: text,
		})
	}
	for _, attachment := range attachments {
		content = append(content, attachment.PromptContent)
	}

	return turnCreateRequest{
		Stream:  stream,
		Prompt:  agents.NormalizePrompt(agents.Prompt{Content: content}),
		Uploads: attachments,
	}, nil
}

func parseFormBoolValue(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes"
}

func persistTurnAttachments(dataDir string, files []*multipart.FileHeader) ([]storedTurnAttachment, error) {
	if len(files) == 0 {
		return nil, nil
	}

	attachments := make([]storedTurnAttachment, 0, len(files))
	for _, fileHeader := range files {
		attachment, err := persistTurnAttachment(dataDir, fileHeader)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	return attachments, nil
}

func persistTurnAttachment(dataDir string, fileHeader *multipart.FileHeader) (storedTurnAttachment, error) {
	if fileHeader == nil {
		return storedTurnAttachment{}, errors.New("attachment is required")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return storedTurnAttachment{}, fmt.Errorf("open attachment %q: %w", fileHeader.Filename, err)
	}
	defer src.Close()

	attachmentID := newAttachmentID()
	displayName := normalizeUploadFilename(fileHeader.Filename)
	dstFile, dstPath, err := createUploadTempFile(dataDir, attachmentID, displayName)
	if err != nil {
		return storedTurnAttachment{}, err
	}

	size, mimeType, copyErr := copyUploadToTempFile(dstFile, src, displayName, fileHeader.Header.Get("Content-Type"))
	closeErr := dstFile.Close()
	if copyErr != nil {
		_ = os.Remove(dstPath)
		return storedTurnAttachment{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dstPath)
		return storedTurnAttachment{}, fmt.Errorf("close temp upload %q: %w", displayName, closeErr)
	}

	finalPath, err := finalizeUploadPath(dataDir, attachmentID, displayName, mimeType)
	if err != nil {
		_ = os.Remove(dstPath)
		return storedTurnAttachment{}, err
	}
	if err := os.Rename(dstPath, finalPath); err != nil {
		_ = os.Remove(dstPath)
		return storedTurnAttachment{}, fmt.Errorf("move stored upload %q: %w", displayName, err)
	}

	return storedTurnAttachment{
		PromptContent: agents.PromptContent{
			Type:         agents.PromptContentTypeResourceLink,
			URI:          fileURIForPath(finalPath),
			Name:         displayName,
			MimeType:     mimeType,
			Size:         size,
			AttachmentID: attachmentID,
		},
		FilePath: finalPath,
	}, nil
}

func createUploadTempFile(dataDir, attachmentID, displayName string) (*os.File, string, error) {
	displayName = normalizeUploadFilename(displayName)
	ext := filepath.Ext(displayName)
	stem := sanitizeUploadTempStem(strings.TrimSuffix(displayName, ext))
	pattern := fmt.Sprintf("%s-%s-*%s", attachmentID, stem, ext)
	tempDir := filepath.Join(filepath.Clean(dataDir), "attachments", ".incoming")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create upload staging dir %q: %w", tempDir, err)
	}
	file, err := os.CreateTemp(tempDir, pattern)
	if err != nil {
		return nil, "", fmt.Errorf("create temp upload for %q: %w", displayName, err)
	}
	return file, file.Name(), nil
}

func copyUploadToTempFile(dst *os.File, src multipart.File, displayName, headerMime string) (int64, string, error) {
	if dst == nil {
		return 0, "", errors.New("temp upload file is required")
	}
	if src == nil {
		return 0, "", errors.New("upload source is required")
	}

	sniffBuf := make([]byte, 512)
	n, readErr := io.ReadFull(src, sniffBuf)
	switch {
	case readErr == nil:
	case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
	default:
		return 0, "", fmt.Errorf("read upload %q: %w", displayName, readErr)
	}

	total := int64(0)
	if n > 0 {
		written, err := dst.Write(sniffBuf[:n])
		total += int64(written)
		if err != nil {
			return 0, "", fmt.Errorf("write upload %q: %w", displayName, err)
		}
		if written != n {
			return 0, "", io.ErrShortWrite
		}
	}

	written, err := io.Copy(dst, src)
	total += written
	if err != nil {
		return 0, "", fmt.Errorf("copy upload %q: %w", displayName, err)
	}

	return total, detectUploadMimeType(displayName, headerMime, sniffBuf[:n]), nil
}

func detectUploadMimeType(displayName, headerMime string, sniff []byte) string {
	if mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(headerMime)); err == nil && mediaType != "" && mediaType != "application/octet-stream" {
		return mediaType
	}
	if len(sniff) > 0 {
		if detected := http.DetectContentType(sniff); detected != "" {
			return detected
		}
	}
	if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(displayName))); detected != "" {
		return detected
	}
	return "application/octet-stream"
}

func normalizeUploadFilename(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	name = strings.Map(func(r rune) rune {
		switch r {
		case 0, '/', '\\':
			return -1
		default:
			return r
		}
	}, name)
	if name == "" || name == "." {
		return "attachment"
	}
	return name
}

func sanitizeUploadTempStem(stem string) string {
	stem = strings.ToLower(strings.TrimSpace(stem))
	if stem == "" {
		return "attachment"
	}
	var builder strings.Builder
	for _, r := range stem {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
		if builder.Len() >= 32 {
			break
		}
	}
	result := strings.Trim(builder.String(), "-_")
	if result == "" {
		return "attachment"
	}
	return result
}

func buildStoredUploadFilename(attachmentID, displayName string) string {
	displayName = normalizeUploadFilename(displayName)
	ext := strings.ToLower(filepath.Ext(displayName))
	stem := sanitizeUploadTempStem(strings.TrimSuffix(displayName, ext))
	if ext == "" {
		return fmt.Sprintf("%s-%s", attachmentID, stem)
	}
	return fmt.Sprintf("%s-%s%s", attachmentID, stem, ext)
}

func uploadDirectoryCategory(displayName, mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(displayName)))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "images"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "text/"):
		return "text"
	case mimeType == "application/pdf",
		mimeType == "application/msword",
		mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		mimeType == "application/vnd.ms-excel",
		mimeType == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		mimeType == "application/vnd.ms-powerpoint",
		mimeType == "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return "documents"
	case mimeType == "application/zip",
		mimeType == "application/x-gzip",
		mimeType == "application/gzip",
		mimeType == "application/x-tar",
		mimeType == "application/x-7z-compressed",
		mimeType == "application/x-rar-compressed":
		return "archives"
	}

	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return "images"
	case ".mp3", ".wav", ".m4a", ".aac", ".flac", ".ogg":
		return "audio"
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		return "video"
	case ".txt", ".md", ".json", ".yaml", ".yml", ".csv", ".log":
		return "text"
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx":
		return "documents"
	case ".zip", ".tar", ".gz", ".tgz", ".bz2", ".7z", ".rar":
		return "archives"
	default:
		return "files"
	}
}

func finalizeUploadPath(dataDir, attachmentID, displayName, mimeType string) (string, error) {
	category := uploadDirectoryCategory(displayName, mimeType)
	dir := filepath.Join(filepath.Clean(dataDir), "attachments", category)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create upload dir %q: %w", dir, err)
	}
	return filepath.Join(dir, buildStoredUploadFilename(attachmentID, displayName)), nil
}

func newAttachmentID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("att_%d", time.Now().UTC().UnixMicro())
	}
	return fmt.Sprintf("att_%d_%s", time.Now().UTC().UnixMicro(), hex.EncodeToString(buf))
}

func removeStoredAttachments(attachments []storedTurnAttachment) {
	for _, attachment := range attachments {
		path := strings.TrimSpace(attachment.FilePath)
		if path == "" {
			continue
		}
		_ = os.Remove(path)
	}
}

func uploadTempDir() string {
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

func fileURIForPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	slashPath := filepath.ToSlash(path)
	if volume := filepath.VolumeName(path); volume != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func newLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	return &loggingResponseWriter{
		ResponseWriter: w,
		statusCode:     0,
		bytesWritten:   0,
	}
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Write(body []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytesWritten += n
	return n, err
}

func (w *loggingResponseWriter) StatusCode() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func (w *loggingResponseWriter) BytesWritten() int {
	return w.bytesWritten
}

func (w *loggingResponseWriter) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *loggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func requestClientAddr(r *http.Request) string {
	if r == nil {
		return "unknown"
	}

	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			if ip := strings.TrimSpace(parts[0]); ip != "" {
				return ip
			}
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return "unknown"
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}

	return remoteAddr
}

func requestLogPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return "/"
	}

	path := strings.TrimSpace(r.URL.RequestURI())
	if path == "" {
		path = strings.TrimSpace(r.URL.Path)
	}
	if path == "" {
		path = "/"
	}
	return observability.RedactString(path)
}

func (s *Server) isAuthorized(r *http.Request) bool {
	if s.authToken == "" {
		return true
	}

	const prefix = "Bearer "
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}

	provided := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	return s.matchesAuthToken(provided)
}

func (s *Server) isAttachmentAuthorized(r *http.Request) bool {
	if s.isAuthorized(r) {
		return true
	}
	if s.authToken == "" || r == nil || r.URL == nil {
		return s.authToken == ""
	}
	return s.matchesAuthToken(strings.TrimSpace(r.URL.Query().Get("access_token")))
}

func (s *Server) matchesAuthToken(provided string) bool {
	if provided == "" {
		return false
	}

	if len(provided) != len(s.authToken) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.authToken)) == 1
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, codeInvalidArgument, "method is not allowed for this endpoint", map[string]any{
		"method": r.Method,
		"path":   r.URL.Path,
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)

	encoder := json.NewEncoder(w)
	_ = encoder.Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}

	writeJSON(w, statusCode, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

// handlePathSearch handles path search requests for the working directory input.
// It searches for directories under $HOME matching the query.
// Search is triggered only when query has 3 or more characters.
// Priority: first level, then second level, then third level.
func (s *Server) handlePathSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 3 {
		writeJSON(w, http.StatusOK, map[string]any{
			"query":   query,
			"results": []string{},
		})
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		s.logger.Warn("path_search.home_dir_failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to get home directory", nil)
		return
	}

	results := s.searchDirectories(homeDir, query)

	writeJSON(w, http.StatusOK, map[string]any{
		"query":   query,
		"results": results,
	})
}

// searchDirectories searches for directories matching the query.
// Searches all levels and returns up to maxPathSearchResults matches.
// Skips hidden directories (those starting with a dot).
const maxPathSearchResults = 5

func (s *Server) searchDirectories(homeDir, query string) []string {
	queryLower := strings.ToLower(query)
	var results []string

	// First pass: search top-level directories
	entries, err := os.ReadDir(homeDir)
	if err != nil {
		s.logger.Warn("path_search.read_dir_failed", "dir", homeDir, "error", err.Error())
		return results
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden directories
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.Contains(strings.ToLower(name), queryLower) {
			results = append(results, filepath.Join(homeDir, name))
			if len(results) >= maxPathSearchResults {
				return results
			}
		}
	}

	// Second pass: search second-level directories (continue even if first level found matches)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden directories
		if strings.HasPrefix(name, ".") {
			continue
		}
		firstLevelPath := filepath.Join(homeDir, name)

		subEntries, err := os.ReadDir(firstLevelPath)
		if err != nil {
			continue // Skip directories we can't read
		}

		for _, subEntry := range subEntries {
			if !subEntry.IsDir() {
				continue
			}
			subName := subEntry.Name()
			// Skip hidden directories
			if strings.HasPrefix(subName, ".") {
				continue
			}
			if strings.Contains(strings.ToLower(subName), queryLower) {
				results = append(results, filepath.Join(firstLevelPath, subName))
				if len(results) >= maxPathSearchResults {
					return results
				}
			}
		}
	}

	// Third pass: search third-level directories (continue even if second level found matches)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden directories
		if strings.HasPrefix(name, ".") {
			continue
		}
		firstLevelPath := filepath.Join(homeDir, name)

		subEntries, err := os.ReadDir(firstLevelPath)
		if err != nil {
			continue // Skip directories we can't read
		}

		for _, subEntry := range subEntries {
			if !subEntry.IsDir() {
				continue
			}
			subName := subEntry.Name()
			// Skip hidden directories
			if strings.HasPrefix(subName, ".") {
				continue
			}
			secondLevelPath := filepath.Join(firstLevelPath, subName)

			thirdEntries, err := os.ReadDir(secondLevelPath)
			if err != nil {
				continue // Skip directories we can't read
			}

			for _, thirdEntry := range thirdEntries {
				if !thirdEntry.IsDir() {
					continue
				}
				thirdName := thirdEntry.Name()
				// Skip hidden directories
				if strings.HasPrefix(thirdName, ".") {
					continue
				}
				if strings.Contains(strings.ToLower(thirdName), queryLower) {
					results = append(results, filepath.Join(secondLevelPath, thirdName))
					if len(results) >= maxPathSearchResults {
						return results
					}
				}
			}
		}
	}

	return results
}

// handleRecentDirectories returns the most recently used directories for the client.
func (s *Server) handleRecentDirectories(w http.ResponseWriter, r *http.Request, clientID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}

	dirs, err := s.store.ListRecentDirectories(r.Context(), clientID, 5)
	if err != nil {
		s.logger.Warn("recent_directories.query_failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, codeInternal, "failed to get recent directories", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"directories": dirs,
	})
}

// expandPath expands ~ to the user's home directory.
// If the path starts with ~/, it replaces ~ with the home directory.
// Otherwise, it returns the path as-is.
func expandPath(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
