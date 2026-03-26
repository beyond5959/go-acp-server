package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hub.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() first open: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() repeat call: %v", err)
	}

	countFirst := countRows(t, store.db, "schema_migrations")
	if got, want := countFirst, len(migrations); got != want {
		t.Fatalf("schema_migrations rows = %d, want %d", got, want)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() first store: %v", err)
	}

	store2, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() second open: %v", err)
	}
	defer func() {
		_ = store2.Close()
	}()

	countSecond := countRows(t, store2.db, "schema_migrations")
	if got, want := countSecond, len(migrations); got != want {
		t.Fatalf("schema_migrations rows after reopen = %d, want %d", got, want)
	}
}

func TestMigrateRenamesLegacyDefaultAgentConfigCatalogModelID(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hub.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() seed open: %v", err)
	}

	legacyModelID := legacyDefaultAgentConfigCatalogModelID()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO agent_config_catalogs (agent_id, model_id, config_options_json, updated_at)
		VALUES (?, ?, ?, ?)
	`, "codex", legacyModelID, `[{"id":"model","currentValue":"gpt-5"}]`, "2026-03-23T10:00:00Z"); err != nil {
		t.Fatalf("insert legacy config catalog: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 10`); err != nil {
		t.Fatalf("delete schema_migrations version 10: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() seed store: %v", err)
	}

	store2, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() migrated open: %v", err)
	}
	defer func() {
		_ = store2.Close()
	}()

	catalog, err := store2.GetAgentConfigCatalog(ctx, "codex", DefaultAgentConfigCatalogModelID)
	if err != nil {
		t.Fatalf("GetAgentConfigCatalog(new default id): %v", err)
	}
	if got, want := catalog.ModelID, DefaultAgentConfigCatalogModelID; got != want {
		t.Fatalf("catalog.model_id = %q, want %q", got, want)
	}

	if _, err := store2.GetAgentConfigCatalog(ctx, "codex", legacyModelID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAgentConfigCatalog(legacy id) err = %v, want ErrNotFound", err)
	}
}

func TestCreateListGetThread(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	base := time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
	counter := 0
	store.now = func() time.Time {
		counter++
		return base.Add(time.Duration(counter) * time.Second)
	}

	if err := store.UpsertClient(ctx, "client-a"); err != nil {
		t.Fatalf("UpsertClient(): %v", err)
	}

	threadOne, err := store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-1",
		ClientID:         "client-a",
		AgentID:          "codex",
		CWD:              "/tmp/project-a",
		Title:            "first",
		AgentOptionsJSON: `{"temperature":0}`,
		Summary:          "summary-a",
	})
	if err != nil {
		t.Fatalf("CreateThread(th-1): %v", err)
	}

	_, err = store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-2",
		ClientID:         "client-a",
		AgentID:          "codex",
		CWD:              "/tmp/project-b",
		Title:            "second",
		AgentOptionsJSON: `{"temperature":1}`,
		Summary:          "summary-b",
	})
	if err != nil {
		t.Fatalf("CreateThread(th-2): %v", err)
	}

	gotThread, err := store.GetThread(ctx, "th-1")
	if err != nil {
		t.Fatalf("GetThread(th-1): %v", err)
	}
	if gotThread.ThreadID != threadOne.ThreadID {
		t.Fatalf("GetThread thread_id = %q, want %q", gotThread.ThreadID, threadOne.ThreadID)
	}
	if gotThread.CWD != threadOne.CWD {
		t.Fatalf("GetThread cwd = %q, want %q", gotThread.CWD, threadOne.CWD)
	}

	threads, err := store.ListThreadsByClient(ctx, "client-a")
	if err != nil {
		t.Fatalf("ListThreadsByClient(): %v", err)
	}
	if got, want := len(threads), 2; got != want {
		t.Fatalf("len(threads) = %d, want %d", got, want)
	}
	if threads[0].ThreadID != "th-2" {
		t.Fatalf("threads[0].thread_id = %q, want %q", threads[0].ThreadID, "th-2")
	}
	if threads[1].ThreadID != "th-1" {
		t.Fatalf("threads[1].thread_id = %q, want %q", threads[1].ThreadID, "th-1")
	}
}

func TestDeleteThreadCascadeData(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	if err := store.UpsertClient(ctx, "client-delete"); err != nil {
		t.Fatalf("UpsertClient(): %v", err)
	}

	_, err := store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-delete",
		ClientID:         "client-delete",
		AgentID:          "codex",
		CWD:              "/tmp/project-delete",
		Title:            "to-delete",
		AgentOptionsJSON: "{}",
		Summary:          "",
	})
	if err != nil {
		t.Fatalf("CreateThread(): %v", err)
	}

	_, err = store.CreateTurn(ctx, CreateTurnParams{
		TurnID:      "tu-delete",
		ThreadID:    "th-delete",
		RequestText: "hello",
		Status:      "running",
	})
	if err != nil {
		t.Fatalf("CreateTurn(): %v", err)
	}

	if _, err := store.AppendEvent(ctx, "tu-delete", "turn_started", `{"turnId":"tu-delete"}`); err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}
	if err := store.CreateTurnAttachments(ctx, []CreateTurnAttachmentParams{{
		AttachmentID: "att-delete",
		TurnID:       "tu-delete",
		Name:         "delete.txt",
		MimeType:     "text/plain",
		Size:         4,
		FilePath:     "/tmp/ngent/attachments/text/att-delete-delete.txt",
	}}); err != nil {
		t.Fatalf("CreateTurnAttachments(): %v", err)
	}

	if err := store.DeleteThread(ctx, "th-delete"); err != nil {
		t.Fatalf("DeleteThread(): %v", err)
	}

	if _, err := store.GetThread(ctx, "th-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetThread after delete err = %v, want ErrNotFound", err)
	}
	if _, err := store.GetTurn(ctx, "tu-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTurn after delete err = %v, want ErrNotFound", err)
	}

	if got := countRows(t, store.db, "threads"); got != 0 {
		t.Fatalf("threads rows = %d, want 0", got)
	}
	if got := countRows(t, store.db, "turns"); got != 0 {
		t.Fatalf("turns rows = %d, want 0", got)
	}
	if got := countRows(t, store.db, "events"); got != 0 {
		t.Fatalf("events rows = %d, want 0", got)
	}
	if got := countRows(t, store.db, "turn_attachments"); got != 0 {
		t.Fatalf("turn_attachments rows = %d, want 0", got)
	}
}

func TestDeleteThreadNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	err := store.DeleteThread(ctx, "missing-thread")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteThread missing err = %v, want ErrNotFound", err)
	}
}

func TestCreateTurnAppendEventFinalizeTurn(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	base := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
	counter := 0
	store.now = func() time.Time {
		counter++
		return base.Add(time.Duration(counter) * time.Second)
	}

	if err := store.UpsertClient(ctx, "client-b"); err != nil {
		t.Fatalf("UpsertClient(): %v", err)
	}

	_, err := store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-turn",
		ClientID:         "client-b",
		AgentID:          "codex",
		CWD:              "/tmp/project-turn",
		Title:            "turn-test",
		AgentOptionsJSON: "{}",
		Summary:          "",
	})
	if err != nil {
		t.Fatalf("CreateThread(): %v", err)
	}

	_, err = store.CreateTurn(ctx, CreateTurnParams{
		TurnID:      "tu-1",
		ThreadID:    "th-turn",
		RequestText: "hello",
		Status:      "running",
	})
	if err != nil {
		t.Fatalf("CreateTurn(): %v", err)
	}

	createdTurn, err := store.GetTurn(ctx, "tu-1")
	if err != nil {
		t.Fatalf("GetTurn(tu-1): %v", err)
	}
	if createdTurn.IsInternal {
		t.Fatalf("GetTurn(tu-1).IsInternal = true, want false")
	}

	e1, err := store.AppendEvent(ctx, "tu-1", "turn.started", `{"step":1}`)
	if err != nil {
		t.Fatalf("AppendEvent #1: %v", err)
	}
	e2, err := store.AppendEvent(ctx, "tu-1", "turn.delta", `{"step":2}`)
	if err != nil {
		t.Fatalf("AppendEvent #2: %v", err)
	}
	e3, err := store.AppendEvent(ctx, "tu-1", "turn.completed", `{"step":3}`)
	if err != nil {
		t.Fatalf("AppendEvent #3: %v", err)
	}

	if e1.Seq != 1 || e2.Seq != 2 || e3.Seq != 3 {
		t.Fatalf("unexpected seq values: got [%d,%d,%d], want [1,2,3]", e1.Seq, e2.Seq, e3.Seq)
	}

	seqs := loadEventSeqs(t, store.db, "tu-1")
	if got, want := fmt.Sprint(seqs), "[1 2 3]"; got != want {
		t.Fatalf("event seqs = %s, want %s", got, want)
	}

	if err := store.FinalizeTurn(ctx, FinalizeTurnParams{
		TurnID:       "tu-1",
		ResponseText: "world",
		Status:       "completed",
		StopReason:   "eot",
		ErrorMessage: "",
	}); err != nil {
		t.Fatalf("FinalizeTurn(): %v", err)
	}

	status, stopReason, responseText, completedAt := loadTurnTerminalFields(t, store.db, "tu-1")
	if status != "completed" {
		t.Fatalf("turn status = %q, want %q", status, "completed")
	}
	if stopReason != "eot" {
		t.Fatalf("turn stop_reason = %q, want %q", stopReason, "eot")
	}
	if responseText != "world" {
		t.Fatalf("turn response_text = %q, want %q", responseText, "world")
	}
	if completedAt == "" {
		t.Fatalf("turn completed_at is empty, want non-empty")
	}
}

func TestTurnAttachmentsCRUD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	if err := store.UpsertClient(ctx, "client-attachments"); err != nil {
		t.Fatalf("UpsertClient(): %v", err)
	}
	if _, err := store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-attachments",
		ClientID:         "client-attachments",
		AgentID:          "codex",
		CWD:              "/tmp/project-attachments",
		Title:            "attachments",
		AgentOptionsJSON: "{}",
		Summary:          "",
	}); err != nil {
		t.Fatalf("CreateThread(): %v", err)
	}
	if _, err := store.CreateTurn(ctx, CreateTurnParams{
		TurnID:      "tu-attachments",
		ThreadID:    "th-attachments",
		RequestText: "hello",
		Status:      "running",
	}); err != nil {
		t.Fatalf("CreateTurn(): %v", err)
	}

	if err := store.CreateTurnAttachments(ctx, []CreateTurnAttachmentParams{{
		AttachmentID: "att-1",
		TurnID:       "tu-attachments",
		Name:         "diagram.png",
		MimeType:     "image/png",
		Size:         42,
		FilePath:     "/tmp/ngent/attachments/images/att-1-diagram.png",
	}}); err != nil {
		t.Fatalf("CreateTurnAttachments(): %v", err)
	}

	attachment, err := store.GetTurnAttachment(ctx, "att-1")
	if err != nil {
		t.Fatalf("GetTurnAttachment(): %v", err)
	}
	if got, want := attachment.TurnID, "tu-attachments"; got != want {
		t.Fatalf("attachment.turn_id = %q, want %q", got, want)
	}
	if got, want := attachment.Name, "diagram.png"; got != want {
		t.Fatalf("attachment.name = %q, want %q", got, want)
	}
	if got, want := attachment.MimeType, "image/png"; got != want {
		t.Fatalf("attachment.mime_type = %q, want %q", got, want)
	}
	if got, want := attachment.Size, int64(42); got != want {
		t.Fatalf("attachment.size = %d, want %d", got, want)
	}
	if got, want := attachment.FilePath, "/tmp/ngent/attachments/images/att-1-diagram.png"; got != want {
		t.Fatalf("attachment.file_path = %q, want %q", got, want)
	}

	if err := store.DeleteThread(ctx, "th-attachments"); err != nil {
		t.Fatalf("DeleteThread(): %v", err)
	}
	if _, err := store.GetTurnAttachment(ctx, "att-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTurnAttachment(after delete) err = %v, want ErrNotFound", err)
	}
}

func TestUpdateThreadSummaryAndInternalTurnFlag(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	if err := store.UpsertClient(ctx, "client-c"); err != nil {
		t.Fatalf("UpsertClient(): %v", err)
	}
	_, err := store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-summary",
		ClientID:         "client-c",
		AgentID:          "codex",
		CWD:              "/tmp/project-summary",
		Title:            "summary-test",
		AgentOptionsJSON: "{}",
		Summary:          "",
	})
	if err != nil {
		t.Fatalf("CreateThread(): %v", err)
	}

	if err := store.UpdateThreadSummary(ctx, "th-summary", "new summary"); err != nil {
		t.Fatalf("UpdateThreadSummary(): %v", err)
	}
	thread, err := store.GetThread(ctx, "th-summary")
	if err != nil {
		t.Fatalf("GetThread(th-summary): %v", err)
	}
	if thread.Summary != "new summary" {
		t.Fatalf("thread summary = %q, want %q", thread.Summary, "new summary")
	}

	_, err = store.CreateTurn(ctx, CreateTurnParams{
		TurnID:      "tu-internal",
		ThreadID:    "th-summary",
		RequestText: "internal prompt",
		Status:      "running",
		IsInternal:  true,
	})
	if err != nil {
		t.Fatalf("CreateTurn(internal): %v", err)
	}

	turn, err := store.GetTurn(ctx, "tu-internal")
	if err != nil {
		t.Fatalf("GetTurn(tu-internal): %v", err)
	}
	if !turn.IsInternal {
		t.Fatalf("GetTurn(tu-internal).IsInternal = false, want true")
	}
}

func TestUpdateThreadAgentOptions(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	if err := store.UpsertClient(ctx, "client-model"); err != nil {
		t.Fatalf("UpsertClient(): %v", err)
	}
	_, err := store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-model",
		ClientID:         "client-model",
		AgentID:          "codex",
		CWD:              "/tmp/project-model",
		Title:            "model-test",
		AgentOptionsJSON: "{}",
		Summary:          "",
	})
	if err != nil {
		t.Fatalf("CreateThread(): %v", err)
	}

	if err := store.UpdateThreadAgentOptions(ctx, "th-model", `{"modelId":"gpt-5"}`); err != nil {
		t.Fatalf("UpdateThreadAgentOptions(): %v", err)
	}

	thread, err := store.GetThread(ctx, "th-model")
	if err != nil {
		t.Fatalf("GetThread(th-model): %v", err)
	}
	if thread.AgentOptionsJSON != `{"modelId":"gpt-5"}` {
		t.Fatalf("agent options = %q, want %q", thread.AgentOptionsJSON, `{"modelId":"gpt-5"}`)
	}

	if err := store.UpdateThreadAgentOptions(ctx, "missing-thread", `{"modelId":"gpt-5"}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateThreadAgentOptions(missing) err = %v, want ErrNotFound", err)
	}
}

func TestUpdateThreadTitle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	if err := store.UpsertClient(ctx, "client-title"); err != nil {
		t.Fatalf("UpsertClient(): %v", err)
	}
	_, err := store.CreateThread(ctx, CreateThreadParams{
		ThreadID:         "th-title",
		ClientID:         "client-title",
		AgentID:          "codex",
		CWD:              "/tmp/project-title",
		Title:            "before",
		AgentOptionsJSON: "{}",
		Summary:          "",
	})
	if err != nil {
		t.Fatalf("CreateThread(): %v", err)
	}

	if err := store.UpdateThreadTitle(ctx, "th-title", "after"); err != nil {
		t.Fatalf("UpdateThreadTitle(): %v", err)
	}

	thread, err := store.GetThread(ctx, "th-title")
	if err != nil {
		t.Fatalf("GetThread(th-title): %v", err)
	}
	if thread.Title != "after" {
		t.Fatalf("title = %q, want %q", thread.Title, "after")
	}

	if err := store.UpdateThreadTitle(ctx, "th-title", ""); err != nil {
		t.Fatalf("UpdateThreadTitle(clear): %v", err)
	}

	thread, err = store.GetThread(ctx, "th-title")
	if err != nil {
		t.Fatalf("GetThread(th-title after clear): %v", err)
	}
	if thread.Title != "" {
		t.Fatalf("cleared title = %q, want empty", thread.Title)
	}

	if err := store.UpdateThreadTitle(ctx, "missing-thread", "noop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateThreadTitle(missing) err = %v, want ErrNotFound", err)
	}
}

func TestAgentConfigCatalogCRUD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	base := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)
	counter := 0
	store.now = func() time.Time {
		counter++
		return base.Add(time.Duration(counter) * time.Second)
	}

	if err := store.UpsertAgentConfigCatalog(ctx, UpsertAgentConfigCatalogParams{
		AgentID:           "codex",
		ModelID:           DefaultAgentConfigCatalogModelID,
		ConfigOptionsJSON: `[{"id":"model","currentValue":"gpt-5"}]`,
	}); err != nil {
		t.Fatalf("UpsertAgentConfigCatalog(default): %v", err)
	}
	if err := store.UpsertAgentConfigCatalog(ctx, UpsertAgentConfigCatalogParams{
		AgentID:           "codex",
		ModelID:           "gpt-5",
		ConfigOptionsJSON: `[{"id":"reasoning","currentValue":"high"}]`,
	}); err != nil {
		t.Fatalf("UpsertAgentConfigCatalog(gpt-5): %v", err)
	}

	defaultCatalog, err := store.GetAgentConfigCatalog(ctx, "codex", DefaultAgentConfigCatalogModelID)
	if err != nil {
		t.Fatalf("GetAgentConfigCatalog(default): %v", err)
	}
	if defaultCatalog.ConfigOptionsJSON != `[{"id":"model","currentValue":"gpt-5"}]` {
		t.Fatalf("default config_options_json = %q", defaultCatalog.ConfigOptionsJSON)
	}

	catalogs, err := store.ListAgentConfigCatalogsByAgent(ctx, "codex")
	if err != nil {
		t.Fatalf("ListAgentConfigCatalogsByAgent(): %v", err)
	}
	if got, want := len(catalogs), 2; got != want {
		t.Fatalf("len(catalogs) = %d, want %d", got, want)
	}
	if got := catalogs[0].ModelID; got != DefaultAgentConfigCatalogModelID {
		t.Fatalf("catalogs[0].model_id = %q, want %q", got, DefaultAgentConfigCatalogModelID)
	}

	if err := store.ReplaceAgentConfigCatalogs(ctx, "codex", []UpsertAgentConfigCatalogParams{
		{
			ModelID:           DefaultAgentConfigCatalogModelID,
			ConfigOptionsJSON: `[{"id":"model","currentValue":"gpt-5-mini"}]`,
		},
		{
			ModelID:           "gpt-5-mini",
			ConfigOptionsJSON: `[{"id":"reasoning","currentValue":"medium"}]`,
		},
	}); err != nil {
		t.Fatalf("ReplaceAgentConfigCatalogs(): %v", err)
	}

	replaced, err := store.ListAgentConfigCatalogsByAgent(ctx, "codex")
	if err != nil {
		t.Fatalf("ListAgentConfigCatalogsByAgent() after replace: %v", err)
	}
	if got, want := len(replaced), 2; got != want {
		t.Fatalf("len(replaced) = %d, want %d", got, want)
	}
	if _, err := store.GetAgentConfigCatalog(ctx, "codex", "gpt-5"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAgentConfigCatalog(removed) err = %v, want ErrNotFound", err)
	}
	miniCatalog, err := store.GetAgentConfigCatalog(ctx, "codex", "gpt-5-mini")
	if err != nil {
		t.Fatalf("GetAgentConfigCatalog(gpt-5-mini): %v", err)
	}
	if miniCatalog.ConfigOptionsJSON != `[{"id":"reasoning","currentValue":"medium"}]` {
		t.Fatalf("mini config_options_json = %q", miniCatalog.ConfigOptionsJSON)
	}
}

func TestSessionTranscriptCacheCRUD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	base := time.Date(2026, 3, 13, 9, 0, 0, 0, time.UTC)
	counter := 0
	store.now = func() time.Time {
		counter++
		return base.Add(time.Duration(counter) * time.Second)
	}

	if _, err := store.GetSessionTranscriptCache(ctx, "codex", "/tmp/project", "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSessionTranscriptCache(missing) err = %v, want ErrNotFound", err)
	}

	if err := store.UpsertSessionTranscriptCache(ctx, UpsertSessionTranscriptCacheParams{
		AgentID:      "codex",
		CWD:          "/tmp/project",
		SessionID:    "session-1",
		MessagesJSON: `[{"role":"user","content":"hello"}]`,
	}); err != nil {
		t.Fatalf("UpsertSessionTranscriptCache(first): %v", err)
	}

	cache, err := store.GetSessionTranscriptCache(ctx, "codex", "/tmp/project", "session-1")
	if err != nil {
		t.Fatalf("GetSessionTranscriptCache(first): %v", err)
	}
	if cache.MessagesJSON != `[{"role":"user","content":"hello"}]` {
		t.Fatalf("messages_json = %q", cache.MessagesJSON)
	}
	if got, want := cache.UpdatedAt, base.Add(1*time.Second); !got.Equal(want) {
		t.Fatalf("updated_at = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}

	if err := store.UpsertSessionTranscriptCache(ctx, UpsertSessionTranscriptCacheParams{
		AgentID:      "codex",
		CWD:          "/tmp/project",
		SessionID:    "session-1",
		MessagesJSON: `[{"role":"assistant","content":"world"}]`,
	}); err != nil {
		t.Fatalf("UpsertSessionTranscriptCache(update): %v", err)
	}

	updated, err := store.GetSessionTranscriptCache(ctx, "codex", "/tmp/project", "session-1")
	if err != nil {
		t.Fatalf("GetSessionTranscriptCache(update): %v", err)
	}
	if updated.MessagesJSON != `[{"role":"assistant","content":"world"}]` {
		t.Fatalf("updated messages_json = %q", updated.MessagesJSON)
	}
	if got, want := updated.UpdatedAt, base.Add(2*time.Second); !got.Equal(want) {
		t.Fatalf("updated updated_at = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestSessionConfigCacheCRUD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	base := time.Date(2026, 3, 22, 9, 0, 0, 0, time.UTC)
	counter := 0
	store.now = func() time.Time {
		counter++
		return base.Add(time.Duration(counter) * time.Second)
	}

	if _, err := store.GetSessionConfigCache(ctx, "codex", "/tmp/project", "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSessionConfigCache(missing) err = %v, want ErrNotFound", err)
	}

	if err := store.UpsertSessionConfigCache(ctx, UpsertSessionConfigCacheParams{
		AgentID:           "codex",
		CWD:               "/tmp/project",
		SessionID:         "session-1",
		ConfigOptionsJSON: `[{"id":"model","currentValue":"gpt-5.3-codex"}]`,
	}); err != nil {
		t.Fatalf("UpsertSessionConfigCache(first): %v", err)
	}

	cache, err := store.GetSessionConfigCache(ctx, "codex", "/tmp/project", "session-1")
	if err != nil {
		t.Fatalf("GetSessionConfigCache(first): %v", err)
	}
	if cache.ConfigOptionsJSON != `[{"id":"model","currentValue":"gpt-5.3-codex"}]` {
		t.Fatalf("config_options_json = %q", cache.ConfigOptionsJSON)
	}
	if got, want := cache.UpdatedAt, base.Add(1*time.Second); !got.Equal(want) {
		t.Fatalf("updated_at = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}

	if err := store.UpsertSessionConfigCache(ctx, UpsertSessionConfigCacheParams{
		AgentID:           "codex",
		CWD:               "/tmp/project",
		SessionID:         "session-1",
		ConfigOptionsJSON: `[{"id":"model","currentValue":"gpt-5.2-codex"}]`,
	}); err != nil {
		t.Fatalf("UpsertSessionConfigCache(update): %v", err)
	}

	updated, err := store.GetSessionConfigCache(ctx, "codex", "/tmp/project", "session-1")
	if err != nil {
		t.Fatalf("GetSessionConfigCache(update): %v", err)
	}
	if updated.ConfigOptionsJSON != `[{"id":"model","currentValue":"gpt-5.2-codex"}]` {
		t.Fatalf("updated config_options_json = %q", updated.ConfigOptionsJSON)
	}
	if got, want := updated.UpdatedAt, base.Add(2*time.Second); !got.Equal(want) {
		t.Fatalf("updated updated_at = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestAgentSlashCommandsCRUD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer func() {
		_ = store.Close()
	}()

	base := time.Date(2026, 3, 13, 11, 0, 0, 0, time.UTC)
	counter := 0
	store.now = func() time.Time {
		counter++
		return base.Add(time.Duration(counter) * time.Second)
	}

	if _, err := store.GetAgentSlashCommands(ctx, "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAgentSlashCommands(missing) err = %v, want ErrNotFound", err)
	}

	if err := store.UpsertAgentSlashCommands(ctx, UpsertAgentSlashCommandsParams{
		AgentID:      "codex",
		CommandsJSON: `[{"name":"plan","description":"Toggle plan mode"}]`,
	}); err != nil {
		t.Fatalf("UpsertAgentSlashCommands(first): %v", err)
	}

	commands, err := store.GetAgentSlashCommands(ctx, "codex")
	if err != nil {
		t.Fatalf("GetAgentSlashCommands(first): %v", err)
	}
	if commands.CommandsJSON != `[{"name":"plan","description":"Toggle plan mode"}]` {
		t.Fatalf("commands_json = %q", commands.CommandsJSON)
	}
	if got, want := commands.UpdatedAt, base.Add(1*time.Second); !got.Equal(want) {
		t.Fatalf("updated_at = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}

	if err := store.UpsertAgentSlashCommands(ctx, UpsertAgentSlashCommandsParams{
		AgentID:      "codex",
		CommandsJSON: `[{"name":"clear","description":"Clear the context"}]`,
	}); err != nil {
		t.Fatalf("UpsertAgentSlashCommands(update): %v", err)
	}

	updated, err := store.GetAgentSlashCommands(ctx, "codex")
	if err != nil {
		t.Fatalf("GetAgentSlashCommands(update): %v", err)
	}
	if updated.CommandsJSON != `[{"name":"clear","description":"Clear the context"}]` {
		t.Fatalf("updated commands_json = %q", updated.CommandsJSON)
	}
	if got, want := updated.UpdatedAt, base.Add(2*time.Second); !got.Equal(want) {
		t.Fatalf("updated updated_at = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "hub.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q): %v", dbPath, err)
	}
	return store
}

func countRows(t *testing.T, db *sql.DB, tableName string) int {
	t.Helper()

	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count rows from %s: %v", tableName, err)
	}
	return count
}

func legacyDefaultAgentConfigCatalogModelID() string {
	return "__" + "agent" + "_" + "hub" + "_default__"
}

func loadEventSeqs(t *testing.T, db *sql.DB, turnID string) []int {
	t.Helper()

	rows, err := db.Query(`SELECT seq FROM events WHERE turn_id = ? ORDER BY seq ASC`, turnID)
	if err != nil {
		t.Fatalf("query event seqs: %v", err)
	}
	defer rows.Close()

	seqs := make([]int, 0)
	for rows.Next() {
		var seq int
		if err := rows.Scan(&seq); err != nil {
			t.Fatalf("scan event seq: %v", err)
		}
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate event seqs: %v", err)
	}

	return seqs
}

func loadTurnTerminalFields(t *testing.T, db *sql.DB, turnID string) (status, stopReason, responseText, completedAt string) {
	t.Helper()

	row := db.QueryRow(`
		SELECT status, stop_reason, response_text, COALESCE(completed_at, '')
		FROM turns
		WHERE turn_id = ?
	`, turnID)
	if err := row.Scan(&status, &stopReason, &responseText, &completedAt); err != nil {
		t.Fatalf("query finalized turn: %v", err)
	}
	return status, stopReason, responseText, completedAt
}
