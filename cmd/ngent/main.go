package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/beyond5959/acp-adapter/pkg/codexacp"
	agentimpl "github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	claudeagent "github.com/beyond5959/ngent/internal/agents/claude"
	codexagent "github.com/beyond5959/ngent/internal/agents/codex"
	geminiagent "github.com/beyond5959/ngent/internal/agents/gemini"
	kimiagent "github.com/beyond5959/ngent/internal/agents/kimi"
	opencodeagent "github.com/beyond5959/ngent/internal/agents/opencode"
	qwenagent "github.com/beyond5959/ngent/internal/agents/qwen"
	"github.com/beyond5959/ngent/internal/httpapi"
	"github.com/beyond5959/ngent/internal/observability"
	"github.com/beyond5959/ngent/internal/runtime"
	"github.com/beyond5959/ngent/internal/storage"
	"github.com/beyond5959/ngent/internal/webui"
	qrcode "github.com/skip2/go-qrcode"
)

func main() {
	logger := observability.NewJSONLogger(slog.LevelInfo)

	defaultDBPath, err := resolveDefaultDBPath()
	if err != nil {
		logger.Error("startup.default_db_path_resolve_failed", "error", err.Error())
		os.Exit(1)
	}

	portFlag := flag.Int("port", 8686, "server listen port (1-65535)")
	allowPublic := flag.Bool("allow-public", false, "allow listening on public interfaces (default false for loopback-only)")
	debugFlag := flag.Bool("debug", false, "enable verbose debug logs, including ACP request/response payloads on stderr")
	authToken := flag.String("auth-token", "", "optional bearer token for /v1/* endpoints")
	dbPath := flag.String("db-path", defaultDBPath, "sqlite database path")
	contextRecentTurns := flag.Int("context-recent-turns", 10, "number of recent user+assistant turns injected into each prompt")
	contextMaxChars := flag.Int("context-max-chars", 20000, "maximum character budget for injected context prompt")
	compactMaxChars := flag.Int("compact-max-chars", 4000, "maximum summary characters produced by compact endpoint")
	agentIdleTTL := flag.Duration("agent-idle-ttl", 5*time.Minute, "idle TTL before closing cached thread agent provider")
	shutdownGraceTimeout := flag.Duration("shutdown-grace-timeout", 8*time.Second, "graceful shutdown timeout for active turns")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debugFlag {
		logLevel = slog.LevelDebug
	}
	logger = observability.NewJSONLogger(logLevel)
	observability.ConfigureACPDebug(logger, *debugFlag)

	codexRuntimeConfig := codexagent.DefaultRuntimeConfig()
	codexPreflightErr := codexagent.Preflight(codexRuntimeConfig)
	opencodePreflightErr := opencodeagent.Preflight()
	geminiPreflightErr := geminiagent.Preflight()
	kimiPreflightErr := kimiagent.Preflight()
	qwenPreflightErr := qwenagent.Preflight()
	claudePreflightErr := claudeagent.Preflight()

	if *contextRecentTurns <= 0 {
		logger.Error("startup.invalid_context_recent_turns", "value", *contextRecentTurns)
		os.Exit(1)
	}
	if *contextMaxChars <= 0 {
		logger.Error("startup.invalid_context_max_chars", "value", *contextMaxChars)
		os.Exit(1)
	}
	if *compactMaxChars <= 0 {
		logger.Error("startup.invalid_compact_max_chars", "value", *compactMaxChars)
		os.Exit(1)
	}
	if *agentIdleTTL <= 0 {
		logger.Error("startup.invalid_agent_idle_ttl", "value", agentIdleTTL.String())
		os.Exit(1)
	}
	if *shutdownGraceTimeout <= 0 {
		logger.Error("startup.invalid_shutdown_grace_timeout", "value", shutdownGraceTimeout.String())
		os.Exit(1)
	}

	codexAvailable := codexPreflightErr == nil
	opencodeAvailable := opencodePreflightErr == nil
	geminiAvailable := geminiPreflightErr == nil
	kimiAvailable := kimiPreflightErr == nil
	qwenAvailable := qwenPreflightErr == nil
	claudeAvailable := claudePreflightErr == nil
	if codexPreflightErr != nil {
		logger.Warn("startup.codex_embedded_unavailable", "error", codexPreflightErr.Error())
	}
	if opencodePreflightErr != nil {
		logger.Warn("startup.opencode_unavailable", "error", opencodePreflightErr.Error())
	}
	if geminiPreflightErr != nil {
		logger.Warn("startup.gemini_unavailable", "error", geminiPreflightErr.Error())
	}
	if kimiPreflightErr != nil {
		logger.Warn("startup.kimi_unavailable", "error", kimiPreflightErr.Error())
	}
	if qwenPreflightErr != nil {
		logger.Warn("startup.qwen_unavailable", "error", qwenPreflightErr.Error())
	}
	if claudePreflightErr != nil {
		logger.Warn("startup.claude_unavailable", "error", claudePreflightErr.Error())
	}
	if *debugFlag {
		logger.Info("startup.debug_enabled", "acpTrace", true)
	}
	agents := supportedAgents(codexAvailable, opencodeAvailable, geminiAvailable, kimiAvailable, qwenAvailable, claudeAvailable)
	allowedAgentIDs := agentIDsFromInfos(agents)

	listenAddr, port, err := resolveListenAddr(*portFlag, *allowPublic)
	if err != nil {
		logger.Error("startup.invalid_listen", "error", err.Error(), "port", *portFlag, "allowPublic", *allowPublic)
		os.Exit(1)
	}

	allowedRoots, err := resolveAllowedRoots()
	if err != nil {
		logger.Error("startup.invalid_allowed_roots", "error", err.Error())
		os.Exit(1)
	}
	modelDiscoveryDir := resolveModelDiscoveryDir(allowedRoots)
	if err := ensureDBPathParent(*dbPath); err != nil {
		logger.Error("startup.invalid_db_path", "error", err.Error(), "dbPath", *dbPath)
		os.Exit(1)
	}

	store, err := storage.New(*dbPath)
	if err != nil {
		logger.Error("startup.storage_open_failed", "error", err.Error(), "dbPath", *dbPath)
		os.Exit(1)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			logger.Error("shutdown.storage_close_failed", "error", closeErr.Error())
		}
	}()

	turnController := runtime.NewTurnController()
	handler := httpapi.New(httpapi.Config{
		AuthToken:       *authToken,
		Agents:          agents,
		AllowedAgentIDs: allowedAgentIDs,
		AllowedRoots:    allowedRoots,
		Store:           store,
		TurnController:  turnController,
		TurnAgentFactory: func(thread storage.Thread) (agentimpl.Streamer, error) {
			modelID := extractModelID(thread.AgentOptionsJSON)
			sessionID := extractSessionID(thread.AgentOptionsJSON)
			configOverrides := extractConfigOverrides(thread.AgentOptionsJSON)
			switch thread.AgentID {
			case agentimpl.AgentIDCodex:
				return codexagent.New(codexagent.Config{
					Dir:             thread.CWD,
					ModelID:         modelID,
					SessionID:       sessionID,
					ConfigOverrides: configOverrides,
					Name:            "codex-embedded",
					RuntimeConfig:   codexRuntimeConfig,
				})
			case agentimpl.AgentIDOpencode:
				return opencodeagent.New(opencodeagent.Config{
					Dir:             thread.CWD,
					ModelID:         modelID,
					SessionID:       sessionID,
					ConfigOverrides: configOverrides,
				})
			case agentimpl.AgentIDGemini:
				return geminiagent.New(geminiagent.Config{
					Dir:             thread.CWD,
					ModelID:         modelID,
					SessionID:       sessionID,
					ConfigOverrides: configOverrides,
				})
			case agentimpl.AgentIDKimi:
				return kimiagent.New(kimiagent.Config{
					Dir:             thread.CWD,
					ModelID:         modelID,
					SessionID:       sessionID,
					ConfigOverrides: configOverrides,
				})
			case agentimpl.AgentIDQwen:
				return qwenagent.New(qwenagent.Config{
					Dir:             thread.CWD,
					ModelID:         modelID,
					SessionID:       sessionID,
					ConfigOverrides: configOverrides,
				})
			case agentimpl.AgentIDClaude:
				return claudeagent.New(claudeagent.Config{
					Dir:             thread.CWD,
					ModelID:         modelID,
					SessionID:       sessionID,
					ConfigOverrides: configOverrides,
					Name:            "claude-embedded",
				})
			default:
				return nil, fmt.Errorf("unsupported thread agent %q", thread.AgentID)
			}
		},
		AgentModelsFactory: func(ctx context.Context, agentID string) ([]agentimpl.ModelOption, error) {
			switch agentID {
			case agentimpl.AgentIDCodex:
				if codexPreflightErr != nil {
					return nil, codexPreflightErr
				}
				return codexagent.DiscoverModels(ctx, codexagent.Config{
					Dir:           modelDiscoveryDir,
					Name:          "codex-embedded",
					RuntimeConfig: codexRuntimeConfig,
				})
			case agentimpl.AgentIDClaude:
				if claudePreflightErr != nil {
					return nil, claudePreflightErr
				}
				return claudeagent.DiscoverModels(ctx, claudeagent.Config{
					Dir:  modelDiscoveryDir,
					Name: "claude-embedded",
				})
			case agentimpl.AgentIDGemini:
				if geminiPreflightErr != nil {
					return nil, geminiPreflightErr
				}
				return geminiagent.DiscoverModels(ctx, geminiagent.Config{Dir: modelDiscoveryDir})
			case agentimpl.AgentIDKimi:
				if kimiPreflightErr != nil {
					return nil, kimiPreflightErr
				}
				return kimiagent.DiscoverModels(ctx, kimiagent.Config{Dir: modelDiscoveryDir})
			case agentimpl.AgentIDQwen:
				if qwenPreflightErr != nil {
					return nil, qwenPreflightErr
				}
				return qwenagent.DiscoverModels(ctx, qwenagent.Config{Dir: modelDiscoveryDir})
			case agentimpl.AgentIDOpencode:
				if opencodePreflightErr != nil {
					return nil, opencodePreflightErr
				}
				return opencodeagent.DiscoverModels(ctx, opencodeagent.Config{Dir: modelDiscoveryDir})
			default:
				return nil, fmt.Errorf("unsupported agent %q", agentID)
			}
		},
		ContextRecentTurns: *contextRecentTurns,
		ContextMaxChars:    *contextMaxChars,
		CompactMaxChars:    *compactMaxChars,
		AgentIdleTTL:       *agentIdleTTL,
		Logger:             logger,
		FrontendHandler:    webui.Handler(),
	})
	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	defer refreshCancel()
	startAgentConfigCatalogRefresh(refreshCtx, buildAgentConfigCatalogRefresher(
		store,
		logger,
		allowedAgentIDs,
		modelDiscoveryDir,
		codexRuntimeConfig,
		codexPreflightErr,
		opencodePreflightErr,
		geminiPreflightErr,
		kimiPreflightErr,
		qwenPreflightErr,
		claudePreflightErr,
	))
	defer func() {
		if closeErr := handler.Close(); closeErr != nil {
			logger.Error("shutdown.httpapi_close_failed", "error", closeErr.Error())
		}
	}()
	defer func() {
		if closeErr := codexagent.CloseDiscoveryClient(); closeErr != nil {
			logger.Warn("shutdown.codex_discovery_close_failed", "error", closeErr.Error())
		}
	}()

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	printStartupBanner(os.Stderr, port, agents, listenAddr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		gracefulShutdown(context.Background(), logger, srv, turnController, *shutdownGraceTimeout)
	}()

	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server.listen_failed", "error", err.Error())
		os.Exit(1)
	}

	logger.Info("shutdown.complete", "stoppedAt", time.Now().UTC().Format(time.RFC3339Nano))
}

const agentConfigCatalogRefreshTimeout = 20 * time.Second

type agentConfigCatalogStore interface {
	UpsertAgentConfigCatalog(ctx context.Context, params storage.UpsertAgentConfigCatalogParams) error
	ReplaceAgentConfigCatalogs(ctx context.Context, agentID string, params []storage.UpsertAgentConfigCatalogParams) error
}

type agentConfigCatalogRefresher struct {
	store              agentConfigCatalogStore
	logger             *slog.Logger
	agentIDs           []string
	fetchConfigOptions func(ctx context.Context, agentID, modelID string) ([]agentimpl.ConfigOption, error)
	discoverModels     func(ctx context.Context, agentID string, defaultOptions []agentimpl.ConfigOption) ([]agentimpl.ModelOption, error)
}

func startAgentConfigCatalogRefresh(ctx context.Context, refresher *agentConfigCatalogRefresher) {
	if refresher == nil {
		return
	}
	go refresher.Refresh(ctx)
}

func buildAgentConfigCatalogRefresher(
	store *storage.Store,
	logger *slog.Logger,
	agentIDs []string,
	modelDiscoveryDir string,
	codexRuntimeConfig codexacp.RuntimeConfig,
	codexPreflightErr error,
	opencodePreflightErr error,
	geminiPreflightErr error,
	kimiPreflightErr error,
	qwenPreflightErr error,
	claudePreflightErr error,
) *agentConfigCatalogRefresher {
	if store == nil {
		return nil
	}
	if logger == nil {
		logger = observability.NewJSONLogger(slog.LevelInfo)
	}

	return &agentConfigCatalogRefresher{
		store:    store,
		logger:   logger,
		agentIDs: append([]string(nil), agentIDs...),
		fetchConfigOptions: func(ctx context.Context, agentID, modelID string) ([]agentimpl.ConfigOption, error) {
			switch agentID {
			case agentimpl.AgentIDCodex:
				if codexPreflightErr != nil {
					return nil, codexPreflightErr
				}
				client, err := codexagent.New(codexagent.Config{
					Dir:           modelDiscoveryDir,
					ModelID:       modelID,
					Name:          "codex-embedded",
					RuntimeConfig: codexRuntimeConfig,
				})
				if err != nil {
					return nil, err
				}
				return queryAgentConfigOptions(ctx, client)
			case agentimpl.AgentIDClaude:
				if claudePreflightErr != nil {
					return nil, claudePreflightErr
				}
				client, err := claudeagent.New(claudeagent.Config{
					Dir:     modelDiscoveryDir,
					ModelID: modelID,
					Name:    "claude-embedded",
				})
				if err != nil {
					return nil, err
				}
				return queryAgentConfigOptions(ctx, client)
			case agentimpl.AgentIDGemini:
				if geminiPreflightErr != nil {
					return nil, geminiPreflightErr
				}
				client, err := geminiagent.New(geminiagent.Config{
					Dir:     modelDiscoveryDir,
					ModelID: modelID,
				})
				if err != nil {
					return nil, err
				}
				return queryAgentConfigOptions(ctx, client)
			case agentimpl.AgentIDKimi:
				if kimiPreflightErr != nil {
					return nil, kimiPreflightErr
				}
				client, err := kimiagent.New(kimiagent.Config{
					Dir:     modelDiscoveryDir,
					ModelID: modelID,
				})
				if err != nil {
					return nil, err
				}
				return queryAgentConfigOptions(ctx, client)
			case agentimpl.AgentIDQwen:
				if qwenPreflightErr != nil {
					return nil, qwenPreflightErr
				}
				client, err := qwenagent.New(qwenagent.Config{
					Dir:     modelDiscoveryDir,
					ModelID: modelID,
				})
				if err != nil {
					return nil, err
				}
				return queryAgentConfigOptions(ctx, client)
			case agentimpl.AgentIDOpencode:
				if opencodePreflightErr != nil {
					return nil, opencodePreflightErr
				}
				client, err := opencodeagent.New(opencodeagent.Config{
					Dir:     modelDiscoveryDir,
					ModelID: modelID,
				})
				if err != nil {
					return nil, err
				}
				return queryAgentConfigOptions(ctx, client)
			default:
				return nil, fmt.Errorf("unsupported agent %q", agentID)
			}
		},
		discoverModels: func(ctx context.Context, agentID string, defaultOptions []agentimpl.ConfigOption) ([]agentimpl.ModelOption, error) {
			if models := modelOptionsFromConfigOptions(defaultOptions); len(models) > 0 {
				return models, nil
			}

			switch agentID {
			case "codex":
				if codexPreflightErr != nil {
					return nil, codexPreflightErr
				}
				return codexagent.DiscoverModels(ctx, codexagent.Config{
					Dir:           modelDiscoveryDir,
					Name:          "codex-embedded",
					RuntimeConfig: codexRuntimeConfig,
				})
			case "claude":
				if claudePreflightErr != nil {
					return nil, claudePreflightErr
				}
				return claudeagent.DiscoverModels(ctx, claudeagent.Config{
					Dir:  modelDiscoveryDir,
					Name: "claude-embedded",
				})
			case "gemini":
				if geminiPreflightErr != nil {
					return nil, geminiPreflightErr
				}
				return geminiagent.DiscoverModels(ctx, geminiagent.Config{Dir: modelDiscoveryDir})
			case "kimi":
				if kimiPreflightErr != nil {
					return nil, kimiPreflightErr
				}
				return kimiagent.DiscoverModels(ctx, kimiagent.Config{Dir: modelDiscoveryDir})
			case "qwen":
				if qwenPreflightErr != nil {
					return nil, qwenPreflightErr
				}
				return qwenagent.DiscoverModels(ctx, qwenagent.Config{Dir: modelDiscoveryDir})
			case "opencode":
				if opencodePreflightErr != nil {
					return nil, opencodePreflightErr
				}
				return opencodeagent.DiscoverModels(ctx, opencodeagent.Config{Dir: modelDiscoveryDir})
			default:
				return nil, fmt.Errorf("unsupported agent %q", agentID)
			}
		},
	}
}

func (r *agentConfigCatalogRefresher) Refresh(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}

	for _, agentID := range r.agentIDs {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := r.refreshAgent(ctx, agentID); err != nil {
			r.logger.Warn("config_catalog.refresh_failed",
				"agent", agentID,
				"reason", err.Error(),
			)
		}
	}
}

func (r *agentConfigCatalogRefresher) refreshAgent(ctx context.Context, agentID string) error {
	defaultOptions, err := r.fetchOptionsWithTimeout(ctx, agentID, "")
	if err != nil {
		return err
	}

	entries := make([]storage.UpsertAgentConfigCatalogParams, 0, 4)
	defaultEntry, err := newAgentConfigCatalogEntry(agentID, storage.DefaultAgentConfigCatalogModelID, defaultOptions)
	if err != nil {
		return err
	}
	entries = append(entries, defaultEntry)

	models, err := r.discoverModelsWithTimeout(ctx, agentID, defaultOptions)
	if err != nil {
		if upsertErr := r.store.UpsertAgentConfigCatalog(ctx, defaultEntry); upsertErr != nil {
			return fmt.Errorf("discover models: %w (default upsert failed: %v)", err, upsertErr)
		}
		return fmt.Errorf("discover models: %w", err)
	}

	incomplete := false
	for _, model := range models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		options, err := r.fetchOptionsWithTimeout(ctx, agentID, modelID)
		if err != nil {
			incomplete = true
			r.logger.Warn("config_catalog.refresh_model_failed",
				"agent", agentID,
				"modelId", modelID,
				"reason", err.Error(),
			)
			continue
		}
		entry, err := newAgentConfigCatalogEntry(agentID, modelID, options)
		if err != nil {
			incomplete = true
			r.logger.Warn("config_catalog.encode_failed",
				"agent", agentID,
				"modelId", modelID,
				"reason", err.Error(),
			)
			continue
		}
		entries = append(entries, entry)
	}

	if incomplete {
		for _, entry := range entries {
			if err := r.store.UpsertAgentConfigCatalog(ctx, entry); err != nil {
				return fmt.Errorf("partial upsert model %q: %w", entry.ModelID, err)
			}
		}
		r.logger.Info("config_catalog.refresh_partial",
			"agent", agentID,
			"storedEntries", len(entries),
		)
		return nil
	}

	if err := r.store.ReplaceAgentConfigCatalogs(ctx, agentID, entries); err != nil {
		return fmt.Errorf("replace catalogs: %w", err)
	}
	r.logger.Info("config_catalog.refresh_complete",
		"agent", agentID,
		"storedEntries", len(entries),
	)
	return nil
}

func (r *agentConfigCatalogRefresher) fetchOptionsWithTimeout(
	ctx context.Context,
	agentID string,
	modelID string,
) ([]agentimpl.ConfigOption, error) {
	if r.fetchConfigOptions == nil {
		return nil, errors.New("config option fetcher is not configured")
	}
	callCtx, cancel := context.WithTimeout(ctx, agentConfigCatalogRefreshTimeout)
	defer cancel()
	options, err := r.fetchConfigOptions(callCtx, agentID, modelID)
	if err != nil {
		return nil, err
	}
	return acpmodel.NormalizeConfigOptions(options), nil
}

func (r *agentConfigCatalogRefresher) discoverModelsWithTimeout(
	ctx context.Context,
	agentID string,
	defaultOptions []agentimpl.ConfigOption,
) ([]agentimpl.ModelOption, error) {
	if r.discoverModels == nil {
		return modelOptionsFromConfigOptions(defaultOptions), nil
	}
	callCtx, cancel := context.WithTimeout(ctx, agentConfigCatalogRefreshTimeout)
	defer cancel()
	models, err := r.discoverModels(callCtx, agentID, defaultOptions)
	if err != nil {
		return nil, err
	}
	return acpmodel.NormalizeModelOptions(models), nil
}

func newAgentConfigCatalogEntry(
	agentID string,
	modelID string,
	options []agentimpl.ConfigOption,
) (storage.UpsertAgentConfigCatalogParams, error) {
	encoded, err := json.Marshal(acpmodel.NormalizeConfigOptions(options))
	if err != nil {
		return storage.UpsertAgentConfigCatalogParams{}, fmt.Errorf("encode config catalog: %w", err)
	}
	return storage.UpsertAgentConfigCatalogParams{
		AgentID:           agentID,
		ModelID:           modelID,
		ConfigOptionsJSON: string(encoded),
	}, nil
}

func queryAgentConfigOptions(ctx context.Context, manager agentimpl.ConfigOptionManager) ([]agentimpl.ConfigOption, error) {
	if manager == nil {
		return nil, errors.New("config option manager is nil")
	}
	if closer, ok := manager.(io.Closer); ok {
		defer func() {
			_ = closer.Close()
		}()
	}
	return manager.ConfigOptions(ctx)
}

func modelOptionsFromConfigOptions(options []agentimpl.ConfigOption) []agentimpl.ModelOption {
	modelConfig, ok := acpmodel.FindModelConfigOption(options)
	if !ok {
		return nil
	}

	models := make([]agentimpl.ModelOption, 0, len(modelConfig.Options)+1)
	for _, value := range modelConfig.Options {
		modelID := strings.TrimSpace(value.Value)
		if modelID == "" {
			continue
		}
		name := strings.TrimSpace(value.Name)
		if name == "" {
			name = modelID
		}
		models = append(models, agentimpl.ModelOption{ID: modelID, Name: name})
	}
	if current := strings.TrimSpace(modelConfig.CurrentValue); current != "" {
		models = append(models, agentimpl.ModelOption{ID: current, Name: current})
	}
	return acpmodel.NormalizeModelOptions(models)
}

// extractModelID reads an optional "modelId" string from a JSON agentOptions blob.
// Returns empty string if absent or unparseable.
func extractModelID(agentOptionsJSON string) string {
	var opts struct {
		ModelID string `json:"modelId"`
	}
	if strings.TrimSpace(agentOptionsJSON) == "" {
		return ""
	}
	if err := json.Unmarshal([]byte(agentOptionsJSON), &opts); err != nil {
		return ""
	}
	return strings.TrimSpace(opts.ModelID)
}

func extractSessionID(agentOptionsJSON string) string {
	var opts struct {
		SessionID string `json:"sessionId"`
	}
	if strings.TrimSpace(agentOptionsJSON) == "" {
		return ""
	}
	if err := json.Unmarshal([]byte(agentOptionsJSON), &opts); err != nil {
		return ""
	}
	return strings.TrimSpace(opts.SessionID)
}

func extractConfigOverrides(agentOptionsJSON string) map[string]string {
	var opts struct {
		ConfigOverrides map[string]any `json:"configOverrides"`
	}
	if strings.TrimSpace(agentOptionsJSON) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(agentOptionsJSON), &opts); err != nil {
		return nil
	}

	normalized := make(map[string]string, len(opts.ConfigOverrides))
	for rawID, rawValue := range opts.ConfigOverrides {
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
		normalized[configID] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func supportedAgents(codexAvailable, opencodeAvailable, geminiAvailable, kimiAvailable, qwenAvailable, claudeAvailable bool) []httpapi.AgentInfo {
	agents := make([]httpapi.AgentInfo, 0, len(agentimpl.AllAgentIDs()))
	appendIfAvailable := func(available bool, agentID, name string) {
		if !available {
			return
		}
		agents = append(agents, httpapi.AgentInfo{
			ID:     agentID,
			Name:   name,
			Status: "available",
		})
	}

	appendIfAvailable(codexAvailable, agentimpl.AgentIDCodex, "Codex")
	appendIfAvailable(claudeAvailable, agentimpl.AgentIDClaude, "Claude Code")
	appendIfAvailable(geminiAvailable, agentimpl.AgentIDGemini, "Gemini CLI")
	appendIfAvailable(kimiAvailable, agentimpl.AgentIDKimi, "Kimi CLI")
	appendIfAvailable(qwenAvailable, agentimpl.AgentIDQwen, "Qwen Code")
	appendIfAvailable(opencodeAvailable, agentimpl.AgentIDOpencode, "OpenCode")

	return agents
}

func agentIDsFromInfos(agents []httpapi.AgentInfo) []string {
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.ID)
		if agentID == "" {
			continue
		}
		ids = append(ids, agentID)
	}
	return ids
}

func resolveModelDiscoveryDir(allowedRoots []string) string {
	for _, root := range allowedRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			return root
		}
	}
	wd, err := os.Getwd()
	if err == nil && strings.TrimSpace(wd) != "" {
		return wd
	}
	return "/"
}

func resolveListenAddr(port int, allowPublic bool) (string, int, error) {
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}

	var host string
	if allowPublic {
		host = "0.0.0.0"
	} else {
		host = "127.0.0.1"
	}

	listenAddr := net.JoinHostPort(host, strconv.Itoa(port))
	return listenAddr, port, nil
}

func gracefulShutdown(
	baseCtx context.Context,
	logger *slog.Logger,
	srv *http.Server,
	turns *runtime.TurnController,
	timeout time.Duration,
) {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	if logger == nil {
		logger = observability.NewJSONLogger(slog.LevelInfo)
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}

	activeAtStart := 0
	if turns != nil {
		activeAtStart = turns.ActiveCount()
	}
	logger.Info("shutdown.start",
		"timeout", timeout.String(),
		"activeTurns", activeAtStart,
	)

	shutdownCtx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("shutdown.http_server", "error", err.Error())
	}

	if turns == nil {
		return
	}

	if err := turns.WaitForIdle(shutdownCtx); err == nil {
		logger.Info("shutdown.turns_drained")
		return
	}

	cancelled := turns.CancelAll()
	logger.Warn("shutdown.force_cancel_turns",
		"cancelledCount", cancelled,
		"activeTurnsAfterCancel", turns.ActiveCount(),
	)

	forceCtx, forceCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer forceCancel()
	if err := turns.WaitForIdle(forceCtx); err != nil {
		logger.Warn("shutdown.turns_not_fully_drained", "error", err.Error(), "activeTurns", turns.ActiveCount())
		return
	}
	logger.Info("shutdown.turns_drained_after_force_cancel")
}

func resolveAllowedRoots() ([]string, error) {
	root := filepath.Clean(string(filepath.Separator))
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("resolved root is not absolute: %q", root)
	}
	return []string{root}, nil
}

func resolveDefaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", errors.New("user home dir is empty")
	}
	return filepath.Join(home, ".ngent", "ngent.db"), nil
}

func ensureDBPathParent(dbPath string) error {
	path := strings.TrimSpace(dbPath)
	if path == "" {
		return errors.New("db path is empty")
	}
	parent := filepath.Dir(filepath.Clean(path))
	if parent == "." {
		return nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create db parent dir %q: %w", parent, err)
	}
	return nil
}

// printLogo prints the Ngent ASCII art logo to out.
func printLogo(out io.Writer) {
	if out == nil {
		return
	}
	logo := `
███╗   ██╗ ██████╗ ███████╗███╗   ██╗████████╗
████╗  ██║██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝
██╔██╗ ██║██║  ███╗█████╗  ██╔██╗ ██║   ██║   
██║╚██╗██║██║   ██║██╔══╝  ██║╚██╗██║   ██║   
██║ ╚████║╚██████╔╝███████╗██║ ╚████║   ██║   
╚═╝  ╚═══╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝   
`
	_, _ = fmt.Fprint(out, logo)
}

// getLANURL returns the LAN-accessible URL for the given listen address.
// It returns the URL and true if the server is listening on a LAN-accessible interface.
// It returns empty string and false for loopback-only binds.
func getLANURL(listenAddr string) (string, bool) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil {
		return "", false
	}

	var lanIP string
	switch host {
	case "", "0.0.0.0", "::":
		// Listening on all interfaces — detect the default outbound LAN IP.
		conn, dialErr := net.Dial("udp", "8.8.8.8:80")
		if dialErr != nil {
			return "", false
		}
		lanIP = conn.LocalAddr().(*net.UDPAddr).IP.String()
		_ = conn.Close()
	default:
		ip := net.ParseIP(host)
		if ip == nil || ip.IsLoopback() {
			return "", false // loopback-only; not reachable from LAN
		}
		lanIP = host
	}

	url := "http://" + net.JoinHostPort(lanIP, port) + "/"
	return url, true
}

// printStartupBanner prints a beautiful startup banner with server info.
func printStartupBanner(out io.Writer, port int, agents []httpapi.AgentInfo, listenAddr string) {
	if out == nil {
		return
	}

	printLogo(out)
	_, _ = fmt.Fprintln(out)

	// Server info box
	lanURL, isLAN := getLANURL(listenAddr)
	mode := "Local"
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if isLAN {
		mode = "LAN"
		url = lanURL
	}

	_, _ = fmt.Fprintln(out, "╭─ Server ────────────────────────────────╮")
	_, _ = fmt.Fprintf(out, "│  %-38s │\n", fmt.Sprintf("Port: %d", port))
	_, _ = fmt.Fprintf(out, "│  %-38s │\n", fmt.Sprintf("URL:  %s", url))
	_, _ = fmt.Fprintf(out, "│  %-38s │\n", fmt.Sprintf("Mode: %s", mode))
	_, _ = fmt.Fprintln(out, "╰─────────────────────────────────────────╯")
	_, _ = fmt.Fprintln(out)

	// Agents status
	_, _ = fmt.Fprint(out, "Agents: ")
	if len(agents) == 0 {
		_, _ = fmt.Fprintln(out, "none detected")
		_, _ = fmt.Fprintln(out)
		if isLAN {
			_, _ = fmt.Fprintln(out, "Scan QR code to connect from your phone:")
			_, _ = fmt.Fprintln(out)
			printQRCode(out, lanURL)
			_, _ = fmt.Fprintln(out)
		}

		_, _ = fmt.Fprintln(out, "Ready. Press Ctrl+C to stop.")
		_, _ = fmt.Fprintln(out)
		return
	}
	for i, agent := range agents {
		if i > 0 {
			_, _ = fmt.Fprint(out, " • ")
		}
		symbol := "○"
		if agent.Status == "available" {
			symbol = "●"
		}
		_, _ = fmt.Fprintf(out, "%s %s", symbol, agent.Name)
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out)

	// QR code for LAN mode
	if isLAN {
		_, _ = fmt.Fprintln(out, "Scan QR code to connect from your phone:")
		_, _ = fmt.Fprintln(out)
		printQRCode(out, lanURL)
		_, _ = fmt.Fprintln(out)
	}

	_, _ = fmt.Fprintln(out, "Ready. Press Ctrl+C to stop.")
	_, _ = fmt.Fprintln(out)
}

// printQRCode prints a QR code for the given URL to out.
func printQRCode(out io.Writer, url string) {
	if out == nil || url == "" {
		return
	}
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return
	}
	qr.DisableBorder = true
	_, _ = fmt.Fprintf(out, "%s", qrHalfBlocks(qr))
}

// qrHalfBlocks renders a QR code using Unicode half-block characters so that
// each terminal character encodes one module wide and two modules tall.
// This makes the output roughly 1/4 the area of a plain ASCII render.
func qrHalfBlocks(qr *qrcode.QRCode) string {
	bm := qr.Bitmap() // true = dark module
	var sb strings.Builder
	// 1-char quiet margin: blank line on top
	pad := strings.Repeat(" ", len(bm[0])+2)
	sb.WriteString(pad + "\n")
	for y := 0; y < len(bm); y += 2 {
		sb.WriteRune(' ') // left margin
		for x := 0; x < len(bm[y]); x++ {
			top := bm[y][x]
			bot := y+1 < len(bm) && bm[y+1][x]
			switch {
			case top && bot:
				sb.WriteRune('█')
			case top:
				sb.WriteRune('▀')
			case bot:
				sb.WriteRune('▄')
			default:
				sb.WriteRune(' ')
			}
		}
		sb.WriteString(" \n") // right margin
	}
	// blank line on bottom
	sb.WriteString(pad + "\n")
	return sb.String()
}
