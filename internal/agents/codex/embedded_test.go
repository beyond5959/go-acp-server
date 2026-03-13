package codex_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/acp-adapter/pkg/codexacp"
	"github.com/beyond5959/ngent/internal/agents"
	codex "github.com/beyond5959/ngent/internal/agents/codex"
)

func TestStreamCapturesSlashCommandsEmittedBeforePrompt(t *testing.T) {
	client := newFakeCodexClient(t)
	defer func() {
		_ = client.Close()
	}()

	var snapshots [][]string
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = agents.WithSlashCommandsHandler(ctx, func(_ context.Context, commands []agents.SlashCommand) error {
		snapshots = append(snapshots, commandNames(commands))
		return nil
	})

	stopReason, err := client.Stream(ctx, "slash commands probe", func(string) error { return nil })
	if err != nil {
		t.Fatalf("Stream(): %v", err)
	}
	if stopReason != agents.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", stopReason, agents.StopReasonEndTurn)
	}

	want := []string{"review", "review-branch", "review-commit", "init", "compact", "logout", "mcp"}
	if !containsSlashSnapshot(snapshots, want) {
		t.Fatalf("slash snapshots = %v, want one matching %v", snapshots, want)
	}
}

func TestStreamReplaysCachedSlashCommandsAfterConfigOptionsInit(t *testing.T) {
	client := newFakeCodexClient(t)
	defer func() {
		_ = client.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	options, err := client.ConfigOptions(ctx)
	if err != nil {
		t.Fatalf("ConfigOptions(): %v", err)
	}
	if len(options) == 0 {
		t.Fatalf("ConfigOptions() returned no options")
	}

	var snapshots [][]string
	streamCtx := agents.WithSlashCommandsHandler(ctx, func(_ context.Context, commands []agents.SlashCommand) error {
		snapshots = append(snapshots, commandNames(commands))
		return nil
	})

	stopReason, err := client.Stream(streamCtx, "slash commands after config init", func(string) error { return nil })
	if err != nil {
		t.Fatalf("Stream(): %v", err)
	}
	if stopReason != agents.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", stopReason, agents.StopReasonEndTurn)
	}

	want := []string{"review", "review-branch", "review-commit", "init", "compact", "logout", "mcp"}
	if !containsSlashSnapshot(snapshots, want) {
		t.Fatalf("slash snapshots = %v, want one matching %v", snapshots, want)
	}
}

func TestSlashCommandsAfterConfigOptionsInit(t *testing.T) {
	client := newFakeCodexClient(t)
	defer func() {
		_ = client.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	options, err := client.ConfigOptions(ctx)
	if err != nil {
		t.Fatalf("ConfigOptions(): %v", err)
	}
	if len(options) == 0 {
		t.Fatalf("ConfigOptions() returned no options")
	}

	commands, known, err := client.SlashCommands(ctx)
	if err != nil {
		t.Fatalf("SlashCommands(): %v", err)
	}
	if !known {
		t.Fatalf("SlashCommands() reported commands as unknown")
	}

	want := []string{"review", "review-branch", "review-commit", "init", "compact", "logout", "mcp"}
	if got := commandNames(commands); !reflect.DeepEqual(got, want) {
		t.Fatalf("commandNames = %v, want %v", got, want)
	}
}

func newFakeCodexClient(t *testing.T) *codex.Client {
	t.Helper()

	appServerBin := buildFakeCodexAppServerBinary(t)
	client, err := codex.New(codex.Config{
		Dir: t.TempDir(),
		RuntimeConfig: codexacp.RuntimeConfig{
			AppServerCommand: appServerBin,
			LogLevel:         "debug",
			PatchApplyMode:   "appserver",
			RetryTurnOnCrash: true,
			InitialAuthMode:  "chatgpt_subscription",
		},
	})
	if err != nil {
		t.Fatalf("codex.New(): %v", err)
	}
	return client
}

func buildFakeCodexAppServerBinary(t *testing.T) string {
	t.Helper()

	moduleDir := strings.TrimSpace(goCommandOutput(t, "", "list", "-f", "{{.Dir}}", "-m", "github.com/beyond5959/acp-adapter"))
	if moduleDir == "" {
		t.Fatalf("go list returned empty acp-adapter module dir")
	}

	binaryPath := filepath.Join(t.TempDir(), "fake-codex-app-server")
	goCommandOutput(t, moduleDir, "build", "-o", binaryPath, "./testdata/fake_codex_app_server")
	return binaryPath
}

func goCommandOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("go", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s failed: %v, output=%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func containsSlashSnapshot(snapshots [][]string, want []string) bool {
	for _, snapshot := range snapshots {
		if reflect.DeepEqual(snapshot, want) {
			return true
		}
	}
	return false
}

func commandNames(commands []agents.SlashCommand) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}
