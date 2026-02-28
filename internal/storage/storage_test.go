package storage

import (
	"context"
	"database/sql"
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
