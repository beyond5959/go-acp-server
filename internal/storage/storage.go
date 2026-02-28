package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrNotFound indicates the requested record does not exist.
	ErrNotFound = errors.New("storage: not found")
)

// Store wraps SQLite-backed persistence operations.
type Store struct {
	path string
	db   *sql.DB
	now  func() time.Time
}

// Thread stores one persisted thread row.
type Thread struct {
	ThreadID         string
	ClientID         string
	AgentID          string
	CWD              string
	Title            string
	AgentOptionsJSON string
	Summary          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CreateThreadParams contains input for CreateThread.
type CreateThreadParams struct {
	ThreadID         string
	ClientID         string
	AgentID          string
	CWD              string
	Title            string
	AgentOptionsJSON string
	Summary          string
}

// Turn stores one persisted turn row.
type Turn struct {
	TurnID       string
	ThreadID     string
	RequestText  string
	ResponseText string
	IsInternal   bool
	Status       string
	StopReason   string
	ErrorMessage string
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// CreateTurnParams contains input for CreateTurn.
type CreateTurnParams struct {
	TurnID      string
	ThreadID    string
	RequestText string
	Status      string
	IsInternal  bool
}

// FinalizeTurnParams contains fields used to close a turn.
type FinalizeTurnParams struct {
	TurnID       string
	ResponseText string
	Status       string
	StopReason   string
	ErrorMessage string
}

// Event stores one persisted turn event row.
type Event struct {
	EventID   int64
	TurnID    string
	Seq       int
	Type      string
	DataJSON  string
	CreatedAt time.Time
}

// New opens the SQLite database and applies idempotent migrations.
func New(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("storage: empty database path")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("storage: open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	store := &Store{
		path: path,
		db:   db,
		now:  time.Now,
	}

	if err := store.configure(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := store.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Migrate applies all pending migrations and records versions in schema_migrations.
func (s *Store) Migrate(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		);
	`); err != nil {
		return fmt.Errorf("storage: create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		applied, err := s.migrationApplied(ctx, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}

	return nil
}

// UpsertClient creates a client row or updates its last_seen_at.
func (s *Store) UpsertClient(ctx context.Context, clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return errors.New("storage: clientID is required")
	}

	ts := formatTime(s.now())
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO clients (client_id, created_at, last_seen_at)
		VALUES (?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET last_seen_at = excluded.last_seen_at;
	`, clientID, ts, ts); err != nil {
		return fmt.Errorf("storage: upsert client: %w", err)
	}

	return nil
}

// CreateThread inserts one thread row.
func (s *Store) CreateThread(ctx context.Context, params CreateThreadParams) (Thread, error) {
	if strings.TrimSpace(params.ThreadID) == "" {
		return Thread{}, errors.New("storage: threadID is required")
	}
	if strings.TrimSpace(params.ClientID) == "" {
		return Thread{}, errors.New("storage: clientID is required")
	}
	if strings.TrimSpace(params.AgentID) == "" {
		return Thread{}, errors.New("storage: agentID is required")
	}
	if strings.TrimSpace(params.CWD) == "" {
		return Thread{}, errors.New("storage: cwd is required")
	}
	if strings.TrimSpace(params.AgentOptionsJSON) == "" {
		params.AgentOptionsJSON = "{}"
	}

	now := s.now().UTC()
	nowText := formatTime(now)

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO threads (
			thread_id,
			client_id,
			agent_id,
			cwd,
			title,
			agent_options_json,
			summary,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
	`,
		params.ThreadID,
		params.ClientID,
		params.AgentID,
		params.CWD,
		params.Title,
		params.AgentOptionsJSON,
		params.Summary,
		nowText,
		nowText,
	); err != nil {
		return Thread{}, fmt.Errorf("storage: create thread: %w", err)
	}

	return Thread{
		ThreadID:         params.ThreadID,
		ClientID:         params.ClientID,
		AgentID:          params.AgentID,
		CWD:              params.CWD,
		Title:            params.Title,
		AgentOptionsJSON: params.AgentOptionsJSON,
		Summary:          params.Summary,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

// GetThread returns one thread by thread_id.
func (s *Store) GetThread(ctx context.Context, threadID string) (Thread, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			thread_id,
			client_id,
			agent_id,
			cwd,
			title,
			agent_options_json,
			summary,
			created_at,
			updated_at
		FROM threads
		WHERE thread_id = ?;
	`, threadID)

	var (
		thread      Thread
		createdAtDB string
		updatedAtDB string
	)
	if err := row.Scan(
		&thread.ThreadID,
		&thread.ClientID,
		&thread.AgentID,
		&thread.CWD,
		&thread.Title,
		&thread.AgentOptionsJSON,
		&thread.Summary,
		&createdAtDB,
		&updatedAtDB,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Thread{}, ErrNotFound
		}
		return Thread{}, fmt.Errorf("storage: get thread: %w", err)
	}

	createdAt, err := parseTime(createdAtDB)
	if err != nil {
		return Thread{}, fmt.Errorf("storage: parse thread.created_at: %w", err)
	}
	updatedAt, err := parseTime(updatedAtDB)
	if err != nil {
		return Thread{}, fmt.Errorf("storage: parse thread.updated_at: %w", err)
	}

	thread.CreatedAt = createdAt
	thread.UpdatedAt = updatedAt
	return thread, nil
}

// UpdateThreadSummary updates one thread summary and updates updated_at timestamp.
func (s *Store) UpdateThreadSummary(ctx context.Context, threadID, summary string) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("storage: threadID is required")
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE threads
		SET
			summary = ?,
			updated_at = ?
		WHERE thread_id = ?;
	`, summary, formatTime(s.now()), threadID)
	if err != nil {
		return fmt.Errorf("storage: update thread summary: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update thread summary rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListThreadsByClient returns all threads for one client.
func (s *Store) ListThreadsByClient(ctx context.Context, clientID string) ([]Thread, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			thread_id,
			client_id,
			agent_id,
			cwd,
			title,
			agent_options_json,
			summary,
			created_at,
			updated_at
		FROM threads
		WHERE client_id = ?
		ORDER BY created_at DESC;
	`, clientID)
	if err != nil {
		return nil, fmt.Errorf("storage: list threads: %w", err)
	}
	defer rows.Close()

	threads := make([]Thread, 0)
	for rows.Next() {
		var (
			thread      Thread
			createdAtDB string
			updatedAtDB string
		)
		if err := rows.Scan(
			&thread.ThreadID,
			&thread.ClientID,
			&thread.AgentID,
			&thread.CWD,
			&thread.Title,
			&thread.AgentOptionsJSON,
			&thread.Summary,
			&createdAtDB,
			&updatedAtDB,
		); err != nil {
			return nil, fmt.Errorf("storage: scan thread: %w", err)
		}

		createdAt, err := parseTime(createdAtDB)
		if err != nil {
			return nil, fmt.Errorf("storage: parse thread.created_at: %w", err)
		}
		updatedAt, err := parseTime(updatedAtDB)
		if err != nil {
			return nil, fmt.Errorf("storage: parse thread.updated_at: %w", err)
		}

		thread.CreatedAt = createdAt
		thread.UpdatedAt = updatedAt
		threads = append(threads, thread)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list threads rows: %w", err)
	}

	return threads, nil
}

// CreateTurn inserts a new turn row.
func (s *Store) CreateTurn(ctx context.Context, params CreateTurnParams) (Turn, error) {
	if strings.TrimSpace(params.TurnID) == "" {
		return Turn{}, errors.New("storage: turnID is required")
	}
	if strings.TrimSpace(params.ThreadID) == "" {
		return Turn{}, errors.New("storage: threadID is required")
	}
	if strings.TrimSpace(params.Status) == "" {
		params.Status = "running"
	}

	now := s.now().UTC()
	nowText := formatTime(now)

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO turns (
			turn_id,
			thread_id,
			request_text,
			response_text,
			is_internal,
			status,
			stop_reason,
			error_message,
			created_at,
			completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL);
	`,
		params.TurnID,
		params.ThreadID,
		params.RequestText,
		"",
		boolToSQLiteInt(params.IsInternal),
		params.Status,
		"",
		"",
		nowText,
	); err != nil {
		return Turn{}, fmt.Errorf("storage: create turn: %w", err)
	}

	return Turn{
		TurnID:       params.TurnID,
		ThreadID:     params.ThreadID,
		RequestText:  params.RequestText,
		ResponseText: "",
		IsInternal:   params.IsInternal,
		Status:       params.Status,
		StopReason:   "",
		ErrorMessage: "",
		CreatedAt:    now,
		CompletedAt:  nil,
	}, nil
}

// GetTurn returns one turn by turn_id.
func (s *Store) GetTurn(ctx context.Context, turnID string) (Turn, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			turn_id,
			thread_id,
			request_text,
			response_text,
			is_internal,
			status,
			stop_reason,
			error_message,
			created_at,
			completed_at
		FROM turns
		WHERE turn_id = ?;
	`, turnID)

	var (
		turn           Turn
		isInternalRaw  int
		createdAtDB    string
		completedAtRaw sql.NullString
	)
	if err := row.Scan(
		&turn.TurnID,
		&turn.ThreadID,
		&turn.RequestText,
		&turn.ResponseText,
		&isInternalRaw,
		&turn.Status,
		&turn.StopReason,
		&turn.ErrorMessage,
		&createdAtDB,
		&completedAtRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Turn{}, ErrNotFound
		}
		return Turn{}, fmt.Errorf("storage: get turn: %w", err)
	}

	createdAt, err := parseTime(createdAtDB)
	if err != nil {
		return Turn{}, fmt.Errorf("storage: parse turn.created_at: %w", err)
	}
	turn.CreatedAt = createdAt
	turn.IsInternal = sqliteIntToBool(isInternalRaw)
	if completedAtRaw.Valid {
		completedAt, err := parseTime(completedAtRaw.String)
		if err != nil {
			return Turn{}, fmt.Errorf("storage: parse turn.completed_at: %w", err)
		}
		turn.CompletedAt = &completedAt
	}

	return turn, nil
}

// ListTurnsByThread returns all turns for one thread.
func (s *Store) ListTurnsByThread(ctx context.Context, threadID string) ([]Turn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			turn_id,
			thread_id,
			request_text,
			response_text,
			is_internal,
			status,
			stop_reason,
			error_message,
			created_at,
			completed_at
		FROM turns
		WHERE thread_id = ?
		ORDER BY created_at ASC;
	`, threadID)
	if err != nil {
		return nil, fmt.Errorf("storage: list turns: %w", err)
	}
	defer rows.Close()

	turns := make([]Turn, 0)
	for rows.Next() {
		var (
			turn           Turn
			isInternalRaw  int
			createdAtDB    string
			completedAtRaw sql.NullString
		)
		if err := rows.Scan(
			&turn.TurnID,
			&turn.ThreadID,
			&turn.RequestText,
			&turn.ResponseText,
			&isInternalRaw,
			&turn.Status,
			&turn.StopReason,
			&turn.ErrorMessage,
			&createdAtDB,
			&completedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("storage: scan turn: %w", err)
		}

		createdAt, err := parseTime(createdAtDB)
		if err != nil {
			return nil, fmt.Errorf("storage: parse turn.created_at: %w", err)
		}
		turn.CreatedAt = createdAt
		turn.IsInternal = sqliteIntToBool(isInternalRaw)
		if completedAtRaw.Valid {
			completedAt, err := parseTime(completedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("storage: parse turn.completed_at: %w", err)
			}
			turn.CompletedAt = &completedAt
		}

		turns = append(turns, turn)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list turns rows: %w", err)
	}
	return turns, nil
}

// ListEventsByTurn returns all events for one turn ordered by sequence.
func (s *Store) ListEventsByTurn(ctx context.Context, turnID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			event_id,
			turn_id,
			seq,
			type,
			data_json,
			created_at
		FROM events
		WHERE turn_id = ?
		ORDER BY seq ASC;
	`, turnID)
	if err != nil {
		return nil, fmt.Errorf("storage: list events: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var (
			event       Event
			createdAtDB string
		)
		if err := rows.Scan(
			&event.EventID,
			&event.TurnID,
			&event.Seq,
			&event.Type,
			&event.DataJSON,
			&createdAtDB,
		); err != nil {
			return nil, fmt.Errorf("storage: scan event: %w", err)
		}
		createdAt, err := parseTime(createdAtDB)
		if err != nil {
			return nil, fmt.Errorf("storage: parse event.created_at: %w", err)
		}
		event.CreatedAt = createdAt
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list events rows: %w", err)
	}
	return events, nil
}

// AppendEvent appends one turn event and computes its next contiguous seq.
func (s *Store) AppendEvent(ctx context.Context, turnID, eventType, dataJSON string) (Event, error) {
	if strings.TrimSpace(turnID) == "" {
		return Event{}, errors.New("storage: turnID is required")
	}
	if strings.TrimSpace(eventType) == "" {
		return Event{}, errors.New("storage: event type is required")
	}
	if strings.TrimSpace(dataJSON) == "" {
		dataJSON = "{}"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, fmt.Errorf("storage: begin append event tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var maxSeq int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0)
		FROM events
		WHERE turn_id = ?;
	`, turnID).Scan(&maxSeq); err != nil {
		return Event{}, fmt.Errorf("storage: read max event seq: %w", err)
	}

	nextSeq := maxSeq + 1
	now := s.now().UTC()
	nowText := formatTime(now)

	result, err := tx.ExecContext(ctx, `
		INSERT INTO events (turn_id, seq, type, data_json, created_at)
		VALUES (?, ?, ?, ?, ?);
	`, turnID, nextSeq, eventType, dataJSON, nowText)
	if err != nil {
		return Event{}, fmt.Errorf("storage: append event: %w", err)
	}

	eventID, err := result.LastInsertId()
	if err != nil {
		return Event{}, fmt.Errorf("storage: read event id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("storage: commit append event tx: %w", err)
	}

	return Event{
		EventID:   eventID,
		TurnID:    turnID,
		Seq:       nextSeq,
		Type:      eventType,
		DataJSON:  dataJSON,
		CreatedAt: now,
	}, nil
}

// FinalizeTurn updates terminal turn fields and sets completed_at.
func (s *Store) FinalizeTurn(ctx context.Context, params FinalizeTurnParams) error {
	if strings.TrimSpace(params.TurnID) == "" {
		return errors.New("storage: turnID is required")
	}
	if strings.TrimSpace(params.Status) == "" {
		return errors.New("storage: status is required")
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE turns
		SET
			response_text = ?,
			status = ?,
			stop_reason = ?,
			error_message = ?,
			completed_at = ?
		WHERE turn_id = ?;
	`,
		params.ResponseText,
		params.Status,
		params.StopReason,
		params.ErrorMessage,
		formatTime(s.now()),
		params.TurnID,
	)
	if err != nil {
		return fmt.Errorf("storage: finalize turn: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: finalize turn rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) configure(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("storage: set pragma foreign_keys: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA busy_timeout = 5000;`); err != nil {
		return fmt.Errorf("storage: set pragma busy_timeout: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode = WAL;`); err != nil {
		return fmt.Errorf("storage: set pragma journal_mode: %w", err)
	}
	return nil
}

func (s *Store) migrationApplied(ctx context.Context, version int) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM schema_migrations
		WHERE version = ?;
	`, version).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: query schema_migrations: %w", err)
	}
	return true, nil
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin migration %d: %w", m.version, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, stmt := range m.sql {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migration %d (%s): %w", m.version, m.name, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (?, ?, ?);
	`, m.version, m.name, formatTime(s.now())); err != nil {
		return fmt.Errorf("storage: record migration %d: %w", m.version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit migration %d: %w", m.version, err)
	}
	return nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, raw)
}

func boolToSQLiteInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func sqliteIntToBool(v int) bool {
	return v != 0
}
