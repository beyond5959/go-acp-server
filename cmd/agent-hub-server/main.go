package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

	agentimpl "github.com/example/code-agent-hub-server/internal/agents"
	acpagent "github.com/example/code-agent-hub-server/internal/agents/acp"
	"github.com/example/code-agent-hub-server/internal/httpapi"
	"github.com/example/code-agent-hub-server/internal/runtime"
	"github.com/example/code-agent-hub-server/internal/storage"
)

func main() {
	listenAddrFlag := flag.String("listen", "127.0.0.1:8686", "server listen address")
	allowPublic := flag.Bool("allow-public", false, "allow listening on public interfaces")
	authToken := flag.String("auth-token", "", "optional bearer token for /v1/* endpoints")
	dbPath := flag.String("db-path", "./agent-hub.db", "sqlite database path")
	codexACPGoBin := flag.String("codex-acp-go-bin", "", "absolute path to codex-acp-go binary")
	codexACPGoArgs := flag.String("codex-acp-go-args", "", "optional codex-acp-go args, split by whitespace")
	contextRecentTurns := flag.Int("context-recent-turns", 10, "number of recent user+assistant turns injected into each prompt")
	contextMaxChars := flag.Int("context-max-chars", 20000, "maximum character budget for injected context prompt")
	compactMaxChars := flag.Int("compact-max-chars", 4000, "maximum summary characters produced by compact endpoint")
	agentIdleTTL := flag.Duration("agent-idle-ttl", 5*time.Minute, "idle TTL before closing cached thread agent provider")
	shutdownGraceTimeout := flag.Duration("shutdown-grace-timeout", 8*time.Second, "graceful shutdown timeout for active turns")

	var allowedRootsFlag stringListFlag
	flag.Var(&allowedRootsFlag, "allowed-root", "allowed workspace root (repeatable)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	codexCfg, err := resolveCodexACPConfig(*codexACPGoBin, *codexACPGoArgs)
	if err != nil {
		logger.Error("startup.invalid_codex_acp_config", "error", err.Error())
		os.Exit(1)
	}

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

	agents := supportedAgents(codexCfg.Bin)

	listenAddr, port, err := validateListenAddr(*listenAddrFlag, *allowPublic)
	if err != nil {
		logger.Error("startup.invalid_listen", "error", err.Error(), "listenAddr", *listenAddrFlag, "allowPublic", *allowPublic)
		os.Exit(1)
	}

	allowedRoots, err := resolveAllowedRoots(allowedRootsFlag)
	if err != nil {
		logger.Error("startup.invalid_allowed_roots", "error", err.Error())
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
		AllowedAgentIDs: []string{"codex"},
		AllowedRoots:    allowedRoots,
		Store:           store,
		TurnController:  turnController,
		TurnAgentFactory: func(thread storage.Thread) (agentimpl.Streamer, error) {
			if thread.AgentID != "codex" {
				return nil, fmt.Errorf("unsupported thread agent %q", thread.AgentID)
			}

			if codexCfg.Bin == "" {
				return nil, errors.New("codex provider is unconfigured: set --codex-acp-go-bin")
			}

			return acpagent.New(acpagent.Config{
				Command: codexCfg.Bin,
				Args:    codexCfg.Args,
				Dir:     thread.CWD,
				Name:    "codex-acp-go",
			})
		},
		ContextRecentTurns: *contextRecentTurns,
		ContextMaxChars:    *contextMaxChars,
		CompactMaxChars:    *compactMaxChars,
		AgentIdleTTL:       *agentIdleTTL,
		Logger:             logger,
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

	logger.Info(
		"startup.complete",
		"startedAt", time.Now().UTC().Format(time.RFC3339Nano),
		"listenAddr", listenAddr,
		"port", port,
		"dbPath", *dbPath,
		"agents", agents,
		"allowedRoots", allowedRoots,
		"codexACPGoBinConfigured", codexCfg.Bin != "",
		"contextRecentTurns", *contextRecentTurns,
		"contextMaxChars", *contextMaxChars,
		"compactMaxChars", *compactMaxChars,
		"agentIdleTTL", agentIdleTTL.String(),
	)

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

func supportedAgents(codexBin string) []httpapi.AgentInfo {
	codexStatus := "unconfigured"
	if strings.TrimSpace(codexBin) != "" {
		codexStatus = "available"
	}

	return []httpapi.AgentInfo{
		{
			ID:     "codex",
			Name:   "Codex (via codex-acp-go)",
			Status: codexStatus,
		},
		{
			ID:     "claude",
			Name:   "Claude (placeholder)",
			Status: "unavailable",
		},
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

type codexACPConfig struct {
	Bin  string
	Args []string
}

func resolveCodexACPConfig(binPath, argsText string) (codexACPConfig, error) {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return codexACPConfig{}, nil
	}
	if !filepath.IsAbs(binPath) {
		return codexACPConfig{}, fmt.Errorf("--codex-acp-go-bin must be absolute: %q", binPath)
	}

	cleanBin := filepath.Clean(binPath)
	args := parseCommandArgs(argsText)

	return codexACPConfig{
		Bin:  cleanBin,
		Args: args,
	}, nil
}

func parseCommandArgs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return strings.Fields(raw)
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

func resolveAllowedRoots(configured []string) ([]string, error) {
	if len(configured) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve default allowed root from cwd: %w", err)
		}
		return []string{filepath.Clean(cwd)}, nil
	}

	roots := make([]string, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, root := range configured {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("allowed root must be absolute: %q", root)
		}
		cleaned := filepath.Clean(root)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		roots = append(roots, cleaned)
	}

	if len(roots) == 0 {
		return nil, errors.New("allowed roots resolved to empty set")
	}
	return roots, nil
}

type stringListFlag []string

func (s *stringListFlag) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringListFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}
