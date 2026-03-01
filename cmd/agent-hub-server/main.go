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

	agentimpl "github.com/beyond5959/go-acp-server/internal/agents"
	codexagent "github.com/beyond5959/go-acp-server/internal/agents/codex"
	opencodeagent "github.com/beyond5959/go-acp-server/internal/agents/opencode"
	"github.com/beyond5959/go-acp-server/internal/httpapi"
	"github.com/beyond5959/go-acp-server/internal/runtime"
	"github.com/beyond5959/go-acp-server/internal/storage"
	"github.com/beyond5959/go-acp-server/internal/webui"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	defaultDBPath, err := resolveDefaultDBPath()
	if err != nil {
		logger.Error("startup.default_db_path_resolve_failed", "error", err.Error())
		os.Exit(1)
	}

	listenAddrFlag := flag.String("listen", "127.0.0.1:8686", "server listen address")
	allowPublic := flag.Bool("allow-public", false, "allow listening on public interfaces")
	authToken := flag.String("auth-token", "", "optional bearer token for /v1/* endpoints")
	dbPath := flag.String("db-path", defaultDBPath, "sqlite database path")
	contextRecentTurns := flag.Int("context-recent-turns", 10, "number of recent user+assistant turns injected into each prompt")
	contextMaxChars := flag.Int("context-max-chars", 20000, "maximum character budget for injected context prompt")
	compactMaxChars := flag.Int("compact-max-chars", 4000, "maximum summary characters produced by compact endpoint")
	agentIdleTTL := flag.Duration("agent-idle-ttl", 5*time.Minute, "idle TTL before closing cached thread agent provider")
	shutdownGraceTimeout := flag.Duration("shutdown-grace-timeout", 8*time.Second, "graceful shutdown timeout for active turns")
	flag.Parse()

	codexRuntimeConfig := codexagent.DefaultRuntimeConfig()
	codexPreflightErr := codexagent.Preflight(codexRuntimeConfig)
	opencodePreflightErr := opencodeagent.Preflight()

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
	if codexPreflightErr != nil {
		logger.Warn("startup.codex_embedded_unavailable", "error", codexPreflightErr.Error())
	}
	if opencodePreflightErr != nil {
		logger.Warn("startup.opencode_unavailable", "error", opencodePreflightErr.Error())
	}
	agents := supportedAgents(codexAvailable, opencodeAvailable)

	listenAddr, _, err := validateListenAddr(*listenAddrFlag, *allowPublic)
	if err != nil {
		logger.Error("startup.invalid_listen", "error", err.Error(), "listenAddr", *listenAddrFlag, "allowPublic", *allowPublic)
		os.Exit(1)
	}

	allowedRoots, err := resolveAllowedRoots()
	if err != nil {
		logger.Error("startup.invalid_allowed_roots", "error", err.Error())
		os.Exit(1)
	}
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
		AllowedAgentIDs: []string{"codex", "opencode"},
		AllowedRoots:    allowedRoots,
		Store:           store,
		TurnController:  turnController,
		TurnAgentFactory: func(thread storage.Thread) (agentimpl.Streamer, error) {
			switch thread.AgentID {
			case "codex":
				return codexagent.New(codexagent.Config{
					Dir:           thread.CWD,
					Name:          "codex-embedded",
					RuntimeConfig: codexRuntimeConfig,
				})
			case "opencode":
				modelID := extractModelID(thread.AgentOptionsJSON)
				return opencodeagent.New(opencodeagent.Config{
					Dir:     thread.CWD,
					ModelID: modelID,
				})
			default:
				return nil, fmt.Errorf("unsupported thread agent %q", thread.AgentID)
			}
		},
		ContextRecentTurns: *contextRecentTurns,
		ContextMaxChars:    *contextMaxChars,
		CompactMaxChars:    *compactMaxChars,
		AgentIdleTTL:       *agentIdleTTL,
		Logger:             logger,
		FrontendHandler:    webui.Handler(),
	})
	defer func() {
		if closeErr := handler.Close(); closeErr != nil {
			logger.Error("shutdown.httpapi_close_failed", "error", closeErr.Error())
		}
	}()

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	startedAt := time.Now()
	printStartupSummary(os.Stderr, startedAt, listenAddr, *dbPath, startupAgentSummary(agents))

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

func supportedAgents(codexAvailable, opencodeAvailable bool) []httpapi.AgentInfo {	codexStatus := "unavailable"
	if codexAvailable {
		codexStatus = "available"
	}
	opencodeStatus := "unavailable"
	if opencodeAvailable {
		opencodeStatus = "available"
	}

	return []httpapi.AgentInfo{
		{ID: "codex", Name: "Codex", Status: codexStatus},
		{ID: "opencode", Name: "OpenCode", Status: opencodeStatus},
		{ID: "claude", Name: "Claude Code", Status: "unavailable"},
	}
}

func validateListenAddr(listenAddr string, allowPublic bool) (string, int, error) {
	host, portText, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid --listen value %q: %w", listenAddr, err)
	}

	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port in --listen value %q", listenAddr)
	}

	if allowPublic {
		return listenAddr, port, nil
	}

	if host == "" || host == "0.0.0.0" || host == "::" {
		return "", 0, fmt.Errorf("public listen address %q requires --allow-public=true", listenAddr)
	}

	if host == "localhost" {
		return listenAddr, port, nil
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", 0, fmt.Errorf("non-loopback listen address %q requires --allow-public=true", listenAddr)
	}

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
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
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
	return filepath.Join(home, ".go-agent-server", "agent-hub.db"), nil
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

func printStartupSummary(out io.Writer, startedAt time.Time, listenAddr, dbPath, agents string) {
	if out == nil {
		return
	}
	timestamp := startedAt.Local().Format("2006-01-02 15:04:05 MST")
	if strings.TrimSpace(agents) == "" {
		agents = "none"
	}
	addr := strings.TrimSpace(listenAddr)
	_, _ = fmt.Fprintf(
		out,
		"Agent Hub Server started\n"+
			"  Time:   %s\n"+
			"  HTTP:   http://%s\n"+
			"  Web:    http://%s/\n"+
			"  DB:     %s\n"+
			"  Agents: %s\n"+
			"  Help:   agent-hub-server --help\n",
		timestamp,
		addr,
		addr,
		strings.TrimSpace(dbPath),
		strings.TrimSpace(agents),
	)
}

func startupAgentSummary(agents []httpapi.AgentInfo) string {
	if len(agents) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(agents))
	for _, agent := range agents {
		name := strings.TrimSpace(agent.Name)
		if name == "" {
			name = strings.TrimSpace(agent.ID)
		}
		if name == "" {
			name = "unknown"
		}
		status := strings.TrimSpace(agent.Status)
		if status == "" {
			status = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", name, status))
	}
	return strings.Join(parts, ", ")
}
