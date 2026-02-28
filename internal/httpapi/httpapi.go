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
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beyond5959/go-acp-server/internal/agents"
	"github.com/beyond5959/go-acp-server/internal/runtime"
	"github.com/beyond5959/go-acp-server/internal/sse"
	"github.com/beyond5959/go-acp-server/internal/storage"
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
	UpdateThreadSummary(ctx context.Context, threadID, summary string) error
	ListThreadsByClient(ctx context.Context, clientID string) ([]storage.Thread, error)
	CreateTurn(ctx context.Context, params storage.CreateTurnParams) (storage.Turn, error)
	GetTurn(ctx context.Context, turnID string) (storage.Turn, error)
	ListTurnsByThread(ctx context.Context, threadID string) ([]storage.Turn, error)
	AppendEvent(ctx context.Context, turnID, eventType, dataJSON string) (storage.Event, error)
	ListEventsByTurn(ctx context.Context, turnID string) ([]storage.Event, error)
	FinalizeTurn(ctx context.Context, params storage.FinalizeTurnParams) error
}

// TurnAgentFactory resolves a per-turn agent provider from thread metadata.
type TurnAgentFactory func(thread storage.Thread) (agents.Streamer, error)

// Config controls HTTP API behavior.
type Config struct {
	AuthToken          string
	Agents             []AgentInfo
	AllowedAgentIDs    []string
	AllowedRoots       []string
	Store              ThreadStore
	TurnController     *runtime.TurnController
	Agent              agents.Streamer
	TurnAgentFactory   TurnAgentFactory
	AgentIdleTTL       time.Duration
	Logger             *slog.Logger
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
	agents             []AgentInfo
	allowedRoots       []string
	store              ThreadStore
	allowedAgent       map[string]struct{}
	turns              *runtime.TurnController
	turnAgentFactory   TurnAgentFactory
	agentIdleTTL       time.Duration
	logger             *slog.Logger
	contextRecentTurns int
	contextMaxChars    int
	compactMaxChars    int
	permissionTimeout  time.Duration
	frontendHandler    http.Handler

	permissionsMu sync.Mutex
	permissions   map[string]*pendingPermission
	permissionSeq uint64

	agentMu        sync.Mutex
	agentsByThread map[string]*managedAgent
	janitorStop    chan struct{}
	janitorDone    chan struct{}
}

const (
	defaultContextRecentTurns = 10
	defaultContextMaxChars    = 20000
	defaultCompactMaxChars    = 4000
	defaultAgentIdleTTL       = 5 * time.Minute
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
		permissionTimeout = 15 * time.Second
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
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	server := &Server{
		authToken:          cfg.AuthToken,
		agents:             agentsList,
		allowedRoots:       roots,
		store:              cfg.Store,
		allowedAgent:       allowedAgent,
		turns:              turnController,
		turnAgentFactory:   turnAgentFactory,
		agentIdleTTL:       agentIdleTTL,
		logger:             logger,
		contextRecentTurns: contextRecentTurns,
		contextMaxChars:    contextMaxChars,
		compactMaxChars:    compactMaxChars,
		permissionTimeout:  permissionTimeout,
		frontendHandler:    cfg.FrontendHandler,
		permissions:        make(map[string]*pendingPermission),
		agentsByThread:     make(map[string]*managedAgent),
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

	s.logger.Info(
		"http.request.completed",
		"requestTime", startedAt.UTC().Format(time.RFC3339Nano),
		"method", r.Method,
		"path", r.URL.Path,
		"ip", requestClientIP(r),
		"statusCode", w.StatusCode(),
		"durationMs", time.Since(startedAt).Milliseconds(),
		"responseBytes", w.BytesWritten(),
	)
}

func (s *Server) routeV1(w http.ResponseWriter, r *http.Request, clientID string) {
	if r.URL.Path == "/v1/agents" {
		s.handleAgents(w, r)
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

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Agents []AgentInfo `json:"agents"`
	}{Agents: s.agents})
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
		s.handleGetThread(w, r, clientID, threadID)
	case "turns":
		s.handleCreateTurnStream(w, r, clientID, threadID)
	case "compact":
		s.handleCompactThread(w, r, clientID, threadID)
	case "history":
		s.handleThreadHistory(w, r, clientID, threadID)
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
	if cwd == "" || !filepath.IsAbs(cwd) {
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

	var req struct {
		Input  string `json:"input"`
		Stream bool   `json:"stream"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body", map[string]any{"reason": err.Error()})
		return
	}
	if !req.Stream {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "stream must be true", map[string]any{"field": "stream"})
		return
	}

	injectedPrompt, err := s.buildInjectedPrompt(r.Context(), thread, req.Input)
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
	turnCtx, cancelTurn := context.WithCancel(r.Context())
	persistCtx := context.WithoutCancel(r.Context())
	if err := s.turns.Activate(thread.ThreadID, turnID, cancelTurn); err != nil {
		if errors.Is(err, runtime.ErrActiveTurnExists) {
			writeError(w, http.StatusConflict, "CONFLICT", "thread already has an active turn", map[string]any{"threadId": thread.ThreadID})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to activate turn", map[string]any{"reason": err.Error()})
		return
	}
	defer func() {
		cancelTurn()
		s.turns.Release(thread.ThreadID, turnID)
	}()

	if _, err := s.store.CreateTurn(r.Context(), storage.CreateTurnParams{
		TurnID:      turnID,
		ThreadID:    thread.ThreadID,
		RequestText: req.Input,
		Status:      "running",
		IsInternal:  false,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create turn", map[string]any{"reason": err.Error()})
		return
	}

	streamWriter, err := sse.NewWriter(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "SSE is not supported by response writer", map[string]any{})
		return
	}
	w.WriteHeader(http.StatusOK)

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

	turnCtx = agents.WithPermissionHandler(turnCtx, func(permissionCtx context.Context, req agents.PermissionRequest) (agents.PermissionResponse, error) {
		permissionID := s.nextPermissionID(req.RequestID)
		pending := newPendingPermission(clientID)
		s.registerPermission(permissionID, pending)
		defer s.unregisterPermission(permissionID, pending)

		payload := map[string]any{
			"turnId":       turnID,
			"permissionId": permissionID,
			"approval":     req.Approval,
			"command":      req.Command,
			"requestId":    req.RequestID,
		}
		if err := emit("permission_required", payload); err != nil {
			pending.Resolve(agents.PermissionOutcomeDeclined)
			return agents.PermissionResponse{Outcome: agents.PermissionOutcomeDeclined}, err
		}

		outcome := s.waitPermissionOutcome(permissionCtx, pending)
		return agents.PermissionResponse{Outcome: outcome}, nil
	})

	if err := emit("turn_started", map[string]any{"turnId": turnID}); err != nil {
		s.finalizeTurnWithBestEffort(persistCtx, turnID, "failed", "error", "", err.Error())
		return
	}

	stopReason, streamErr := streamAgent.Stream(turnCtx, injectedPrompt, func(delta string) error {
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
	if err := s.turns.Activate(thread.ThreadID, turnID, cancelTurn); err != nil {
		if errors.Is(err, runtime.ErrActiveTurnExists) {
			writeError(w, http.StatusConflict, "CONFLICT", "thread already has an active turn", map[string]any{"threadId": thread.ThreadID})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to activate compact turn", map[string]any{"reason": err.Error()})
		return
	}
	defer func() {
		cancelTurn()
		s.turns.Release(thread.ThreadID, turnID)
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
		Outcome string `json:"outcome"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body", map[string]any{"reason": err.Error()})
		return
	}

	outcome, ok := normalizePermissionOutcome(req.Outcome)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "outcome must be approved, declined, or cancelled", map[string]any{
			"field": "outcome",
		})
		return
	}

	if err := s.resolvePermission(permissionID, clientID, outcome); err != nil {
		if errors.Is(err, errPermissionNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "permission not found", map[string]any{})
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

	writeJSON(w, http.StatusOK, map[string]any{
		"permissionId": permissionID,
		"status":       "recorded",
		"outcome":      string(outcome),
	})
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

func (s *Server) finalizeTurnWithBestEffort(ctx context.Context, turnID, status, stopReason, responseText, errorMessage string) {
	_ = s.store.FinalizeTurn(ctx, storage.FinalizeTurnParams{
		TurnID:       turnID,
		ResponseText: responseText,
		Status:       status,
		StopReason:   stopReason,
		ErrorMessage: errorMessage,
	})
}

func (s *Server) resolveTurnAgent(thread storage.Thread) (agents.Streamer, error) {
	s.agentMu.Lock()
	entry, ok := s.agentsByThread[thread.ThreadID]
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
	if existing, exists := s.agentsByThread[thread.ThreadID]; exists {
		existing.lastUsed = time.Now().UTC()
		s.agentMu.Unlock()
		if closer != nil {
			_ = closer.Close()
		}
		return existing.provider, nil
	}
	s.agentsByThread[thread.ThreadID] = &managedAgent{
		threadID: thread.ThreadID,
		provider: provider,
		closer:   closer,
		lastUsed: time.Now().UTC(),
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
		threadID string
		name     string
		idleFor  time.Duration
		closer   io.Closer
	}
	items := make([]reclaimItem, 0)

	s.agentMu.Lock()
	for threadID, entry := range s.agentsByThread {
		if s.turns.IsThreadActive(threadID) {
			continue
		}
		idleFor := now.Sub(entry.lastUsed)
		if idleFor < s.agentIdleTTL {
			continue
		}
		delete(s.agentsByThread, threadID)
		items = append(items, reclaimItem{
			threadID: threadID,
			name:     entry.provider.Name(),
			idleFor:  idleFor,
			closer:   entry.closer,
		})
	}
	s.agentMu.Unlock()

	for _, item := range items {
		if item.closer != nil {
			_ = item.closer.Close()
		}
		s.logger.Info("agent.idle_reclaimed",
			"threadId", item.threadID,
			"agentName", item.name,
			"idleFor", item.idleFor.String(),
		)
	}
}

func (s *Server) closeAllThreadAgents() error {
	type closeItem struct {
		threadID string
		name     string
		closer   io.Closer
	}

	items := make([]closeItem, 0)
	s.agentMu.Lock()
	for threadID, entry := range s.agentsByThread {
		items = append(items, closeItem{
			threadID: threadID,
			name:     entry.provider.Name(),
			closer:   entry.closer,
		})
		delete(s.agentsByThread, threadID)
	}
	s.agentMu.Unlock()

	for _, item := range items {
		if item.closer != nil {
			_ = item.closer.Close()
		}
		s.logger.Info("agent.closed",
			"threadId", item.threadID,
			"agentName", item.name,
			"reason", "server_close",
		)
	}
	return nil
}

func (s *Server) buildInjectedPrompt(ctx context.Context, thread storage.Thread, input string) (string, error) {
	recentTurns, err := s.loadRecentVisibleTurns(ctx, thread.ThreadID)
	if err != nil {
		return "", err
	}

	return composeContextPrompt(
		thread.Summary,
		recentTurns,
		input,
		s.contextMaxChars,
	), nil
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
)

type pendingPermission struct {
	clientID string

	ch   chan agents.PermissionOutcome
	once sync.Once
}

type managedAgent struct {
	threadID string
	provider agents.Streamer
	closer   io.Closer
	lastUsed time.Time
}

func newPendingPermission(clientID string) *pendingPermission {
	return &pendingPermission{
		clientID: clientID,
		ch:       make(chan agents.PermissionOutcome, 1),
	}
}

func (p *pendingPermission) Resolve(outcome agents.PermissionOutcome) bool {
	resolved := false
	p.once.Do(func() {
		p.ch <- outcome
		close(p.ch)
		resolved = true
	})
	return resolved
}

func toThreadResponse(thread storage.Thread) (threadResponse, error) {
	raw := json.RawMessage(thread.AgentOptionsJSON)
	if len(strings.TrimSpace(thread.AgentOptionsJSON)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) {
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

func (s *Server) waitPermissionOutcome(ctx context.Context, pending *pendingPermission) agents.PermissionOutcome {
	timeout := s.permissionTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case outcome := <-pending.ch:
		if outcome == "" {
			return agents.PermissionOutcomeDeclined
		}
		return outcome
	case <-timer.C:
		pending.Resolve(agents.PermissionOutcomeDeclined)
	case <-ctx.Done():
		pending.Resolve(agents.PermissionOutcomeDeclined)
	}

	outcome, ok := <-pending.ch
	if !ok || outcome == "" {
		return agents.PermissionOutcomeDeclined
	}
	return outcome
}

func (s *Server) resolvePermission(permissionID, clientID string, outcome agents.PermissionOutcome) error {
	s.permissionsMu.Lock()
	pending, ok := s.permissions[permissionID]
	s.permissionsMu.Unlock()
	if !ok {
		return errPermissionNotFound
	}
	if pending.clientID != clientID {
		return errPermissionNotFound
	}
	if !pending.Resolve(outcome) {
		return errPermissionAlreadyResolved
	}
	return nil
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
		return fmt.Sprintf("th_%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("th_%d_%s", time.Now().UTC().UnixNano(), hex.EncodeToString(buf))
}

func newTurnID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("tu_%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("tu_%d_%s", time.Now().UTC().UnixNano(), hex.EncodeToString(buf))
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

func requestClientIP(r *http.Request) string {
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
	if err == nil && host != "" {
		return host
	}
	return remoteAddr
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
