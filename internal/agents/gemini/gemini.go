package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpcli"
	"github.com/beyond5959/ngent/internal/agents/acpstdio"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

// Config configures the Gemini CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one gemini --experimental-acp process per ACP operation.
type Client struct {
	*acpcli.Client
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SessionTranscriptLoader = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a Gemini CLI ACP client.
func New(cfg Config) (*Client, error) {
	base, err := acpcli.New(agents.AgentIDGemini, cfg, acpcli.Hooks{
		OpenConn:                openConn(cfg.Dir),
		SessionNewParams:        acpcli.SessionNewParams(cfg.Dir),
		SessionLoadParams:       acpcli.SessionLoadParams(cfg.Dir),
		SessionListParams:       acpcli.SessionListParams(cfg.Dir),
		PromptParams:            promptParams,
		DiscoverModelsParams:    acpcli.DiscoverModelsParams(cfg.Dir),
		HandlePermissionRequest: handlePermissionRequest,
		Cancel:                  cancelWithCall,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: base}, nil
}

// Preflight checks that the gemini binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary(agents.AgentIDGemini)
}

func openConn(dir string) func(context.Context, acpcli.OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error) {
	return func(
		ctx context.Context,
		req acpcli.OpenConnRequest,
	) (*acpstdio.Conn, func(), json.RawMessage, error) {
		cliHome, err := makeCLIHome()
		if err != nil {
			return nil, nil, nil, acpcli.WrapOpenError(agents.AgentIDGemini, req.Purpose, fmt.Errorf("create CLI home: %w", err))
		}

		conn, cleanup, initResult, err := acpcli.OpenProcess(ctx, acpcli.ProcessConfig{
			Command: agents.AgentIDGemini,
			Args:    []string{"--experimental-acp"},
			Env:     buildGeminiCLIEnv(cliHome),
			ConnOptions: acpstdio.ConnOptions{
				Prefix:           agents.AgentIDGemini,
				AllowStdoutNoise: true,
			},
			InitializeParams: initializeParams(),
		})
		if err != nil {
			_ = os.RemoveAll(cliHome)
			return nil, nil, nil, acpcli.WrapOpenError(agents.AgentIDGemini, req.Purpose, err)
		}

		return conn, func() {
			cleanup()
			_ = os.RemoveAll(cliHome)
		}, initResult, nil
	}
}

func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}
}

func promptParams(sessionID string, prompt agents.Prompt, modelID string) map[string]any {
	params := acpcli.ACPPromptParams(sessionID, prompt)
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		params["model"] = modelID
	}
	return params
}

func handlePermissionRequest(
	ctx context.Context,
	params json.RawMessage,
	handler agents.PermissionHandler,
	hasHandler bool,
) (json.RawMessage, error) {
	var req struct {
		SessionID string         `json:"sessionId"`
		ToolCall  map[string]any `json:"toolCall"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return buildPermissionResponse("cancelled")
	}
	if !hasHandler {
		return buildPermissionResponse("reject_once")
	}

	resp, err := handler(ctx, agents.PermissionRequest{
		Approval: extractToolString(req.ToolCall, "title"),
		Command:  extractToolString(req.ToolCall, "kind"),
		RawParams: map[string]any{
			"sessionId": strings.TrimSpace(req.SessionID),
			"toolCall":  req.ToolCall,
		},
	})
	if err != nil {
		return buildPermissionResponse("reject_once")
	}
	if optionID := strings.TrimSpace(resp.SelectedOptionID); optionID != "" {
		return buildPermissionResponse(optionID)
	}
	switch resp.Outcome {
	case agents.PermissionOutcomeApproved:
		return buildPermissionResponse("allow_once")
	case agents.PermissionOutcomeCancelled:
		return buildPermissionResponse("cancelled")
	default:
		return buildPermissionResponse("reject_once")
	}
}

func buildPermissionResponse(optionID string) (json.RawMessage, error) {
	if strings.EqualFold(strings.TrimSpace(optionID), "cancelled") {
		return acpcli.BuildCancelledPermissionResponse()
	}
	return acpcli.BuildSelectedPermissionResponse(optionID)
}

func extractToolString(toolCall map[string]any, key string) string {
	if len(toolCall) == 0 {
		return ""
	}
	value, _ := toolCall[key]
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func cancelWithCall(conn *acpstdio.Conn, sessionID string) {
	if conn == nil {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = conn.Call(cancelCtx, "session/cancel", map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
	})
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.Client == nil {
		return agents.AgentIDGemini
	}
	return c.Client.Name()
}

// makeCLIHome creates a temporary GEMINI_CLI_HOME directory whose settings.json
// mirrors the user's configured auth type. This prevents Gemini CLI from writing
// interactive auth prompts to stdout during the ACP handshake, which would
// corrupt the JSON-RPC stream. Credential files (OAuth tokens, account records)
// are copied from the user's ~/.gemini so existing sessions remain valid.
func makeCLIHome() (string, error) {
	tmp, err := os.MkdirTemp("", "gemini-cli-home-*")
	if err != nil {
		return "", err
	}
	geminiDir := filepath.Join(tmp, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o700); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}

	userHome, _ := os.UserHomeDir()
	srcGeminiDir := filepath.Join(userHome, ".gemini")

	authType := readUserAuthType(srcGeminiDir)
	settings, _ := json.Marshal(map[string]any{
		"selectedAuthType": authType,
		"security":         map[string]any{"auth": map[string]any{"selectedType": authType}},
	})
	if err := os.WriteFile(filepath.Join(geminiDir, "settings.json"), settings, 0o600); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}

	for _, name := range []string{"oauth_creds.json", "google_accounts.json"} {
		_ = copyFile(filepath.Join(srcGeminiDir, name), filepath.Join(geminiDir, name))
	}

	return tmp, nil
}

// readUserAuthType determines the auth type to configure in the temporary
// GEMINI_CLI_HOME. Priority:
//  1. Use ~/.gemini/settings.json explicit selection when present.
//  2. Otherwise, if GEMINI_API_KEY is present in env, use "gemini-api-key".
//  3. Fall back to "oauth-personal" (the default `gemini auth login` flow).
func readUserAuthType(geminiDir string) string {
	data, err := os.ReadFile(filepath.Join(geminiDir, "settings.json"))
	if err == nil {
		var cfg struct {
			SelectedAuthType string `json:"selectedAuthType"`
			Security         struct {
				Auth struct {
					SelectedType string `json:"selectedType"`
				} `json:"auth"`
			} `json:"security"`
		}
		if err := json.Unmarshal(data, &cfg); err == nil {
			if t := strings.TrimSpace(cfg.Security.Auth.SelectedType); t != "" {
				return t
			}
			if t := strings.TrimSpace(cfg.SelectedAuthType); t != "" {
				return t
			}
		}
	}

	if os.Getenv("GEMINI_API_KEY") != "" {
		return "gemini-api-key"
	}
	return "oauth-personal"
}

// copyFile copies src to dst, creating dst if it does not exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// appendOrReplace sets KEY=value in env, replacing an existing entry if present.
func appendOrReplace(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, len(env))
	copy(result, env)
	for i, entry := range result {
		if strings.HasPrefix(entry, prefix) {
			result[i] = prefix + value
			return result
		}
	}
	return append(result, prefix+value)
}

func buildGeminiCLIEnv(cliHome string) []string {
	env := appendOrReplace(os.Environ(), "GEMINI_CLI_HOME", cliHome)
	if value, ok := os.LookupEnv("GOOGLE_GEMINI_BASE_URL"); ok {
		env = appendOrReplace(env, "GOOGLE_GEMINI_BASE_URL", value)
	}
	return env
}
