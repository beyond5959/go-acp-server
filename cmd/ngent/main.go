package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentimpl "github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
	blackboxagent "github.com/beyond5959/ngent/internal/agents/blackbox"
	claudeagent "github.com/beyond5959/ngent/internal/agents/claude"
	codexagent "github.com/beyond5959/ngent/internal/agents/codex"
	cursoragent "github.com/beyond5959/ngent/internal/agents/cursor"
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
	logger := observability.NewLogger(observability.LevelInfo)

	defaultDataPath, err := resolveDefaultDataPath()
	if err != nil {
		logger.Error("startup.default_data_path_resolve_failed", "error", err.Error())
		os.Exit(1)
	}

	portFlag := flag.Int("port", 8686, "server listen port (1-65535)")
	allowPublic := flag.Bool("allow-public", false, "allow listening on public interfaces (default false for loopback-only)")
	debugFlag := flag.Bool("debug", false, "enable verbose debug logs, including ACP request/response payloads on stderr")
	authToken := flag.String("auth-token", "", "optional bearer token for /v1/* endpoints")
	dataPath := flag.String("data-path", defaultDataPath, "data directory for sqlite and uploaded attachments")
	contextRecentTurns := flag.Int("context-recent-turns", 10, "number of recent user+assistant turns injected into each prompt")
	contextMaxChars := flag.Int("context-max-chars", 20000, "maximum character budget for injected context prompt")
	compactMaxChars := flag.Int("compact-max-chars", 4000, "maximum summary characters produced by compact endpoint")
	agentIdleTTL := flag.Duration("agent-idle-ttl", 5*time.Minute, "idle TTL before closing cached thread agent provider")
	shutdownGraceTimeout := flag.Duration("shutdown-grace-timeout", 8*time.Second, "graceful shutdown timeout for active turns")
	flag.Parse()

	logLevel := observability.LevelInfo
	if *debugFlag {
		logLevel = observability.LevelDebug
	}
	logger = observability.NewLogger(logLevel)
	observability.ConfigureACPDebug(logger, *debugFlag)

	codexRuntimeConfig := codexagent.DefaultRuntimeConfig()
	codexPreflightErr := codexagent.Preflight(codexRuntimeConfig)
	opencodePreflightErr := opencodeagent.Preflight()
	geminiPreflightErr := geminiagent.Preflight()
	kimiPreflightErr := kimiagent.Preflight()
	qwenPreflightErr := qwenagent.Preflight()
	blackboxPreflightErr := blackboxagent.Preflight()
	claudePreflightErr := claudeagent.Preflight()
	cursorPreflightErr := cursoragent.Preflight()

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
	blackboxAvailable := blackboxPreflightErr == nil
	claudeAvailable := claudePreflightErr == nil
	cursorAvailable := cursorPreflightErr == nil
	logStartupPreflight(logger, "startup.codex_embedded_unavailable", codexPreflightErr)
	logStartupPreflight(logger, "startup.opencode_unavailable", opencodePreflightErr)
	logStartupPreflight(logger, "startup.gemini_unavailable", geminiPreflightErr)
	logStartupPreflight(logger, "startup.kimi_unavailable", kimiPreflightErr)
	logStartupPreflight(logger, "startup.qwen_unavailable", qwenPreflightErr)
	logStartupPreflight(logger, "startup.blackbox_unavailable", blackboxPreflightErr)
	logStartupPreflight(logger, "startup.claude_unavailable", claudePreflightErr)
	logStartupPreflight(logger, "startup.cursor_unavailable", cursorPreflightErr)
	if *debugFlag {
		logger.Info("startup.debug_enabled", "acpTrace", true)
	}
	agents := supportedAgents(
		codexAvailable,
		opencodeAvailable,
		geminiAvailable,
		kimiAvailable,
		qwenAvailable,
		blackboxAvailable,
		claudeAvailable,
		cursorAvailable,
	)
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
	if err := ensureDataPath(*dataPath); err != nil {
		logger.Error("startup.invalid_data_path", "error", err.Error(), "dataPath", *dataPath)
		os.Exit(1)
	}
	dbPath := filepath.Join(filepath.Clean(*dataPath), "ngent.db")

	store, err := storage.New(dbPath)
	if err != nil {
		logger.Error("startup.storage_open_failed", "error", err.Error(), "dbPath", dbPath)
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
		DataDir:         *dataPath,
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
			case agentimpl.AgentIDBlackbox:
				return blackboxagent.New(blackboxagent.Config{
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
			case agentimpl.AgentIDCursor:
				return cursoragent.New(cursoragent.Config{
					Dir:             thread.CWD,
					ModelID:         modelID,
					SessionID:       sessionID,
					ConfigOverrides: configOverrides,
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
			case agentimpl.AgentIDBlackbox:
				if blackboxPreflightErr != nil {
					return nil, blackboxPreflightErr
				}
				return blackboxagent.DiscoverModels(ctx, blackboxagent.Config{Dir: modelDiscoveryDir})
			case agentimpl.AgentIDOpencode:
				if opencodePreflightErr != nil {
					return nil, opencodePreflightErr
				}
				return opencodeagent.DiscoverModels(ctx, opencodeagent.Config{Dir: modelDiscoveryDir})
			case agentimpl.AgentIDCursor:
				if cursorPreflightErr != nil {
					return nil, cursorPreflightErr
				}
				return cursoragent.DiscoverModels(ctx, cursoragent.Config{Dir: modelDiscoveryDir})
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

func supportedAgents(
	codexAvailable,
	opencodeAvailable,
	geminiAvailable,
	kimiAvailable,
	qwenAvailable,
	blackboxAvailable,
	claudeAvailable,
	cursorAvailable bool,
) []httpapi.AgentInfo {
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
	appendIfAvailable(blackboxAvailable, agentimpl.AgentIDBlackbox, "BLACKBOX AI")
	appendIfAvailable(cursorAvailable, agentimpl.AgentIDCursor, "Cursor CLI")

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

func logStartupPreflight(logger *observability.Logger, event string, err error) {
	if logger == nil || err == nil {
		return
	}
	if agentutil.IsMissingBinaryError(err) {
		return
	}
	logger.Warn(event, "error", err.Error())
}

func gracefulShutdown(
	baseCtx context.Context,
	logger *observability.Logger,
	srv *http.Server,
	turns *runtime.TurnController,
	timeout time.Duration,
) {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	if logger == nil {
		logger = observability.NewLogger(observability.LevelInfo)
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

func resolveDefaultDataPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", errors.New("user home dir is empty")
	}
	return filepath.Join(home, ".ngent"), nil
}

func ensureDataPath(dataPath string) error {
	path := strings.TrimSpace(dataPath)
	if path == "" {
		return errors.New("data path is empty")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create data dir %q: %w", path, err)
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
