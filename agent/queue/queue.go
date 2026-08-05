package queue

import (
	"database/sql"
	"encoding/json"
	"strings"

	_ "modernc.org/sqlite"
)

type Queue struct {
	db *sql.DB
}

type TelemetryItem struct {
	ID      int
	Payload string
}

func NewQueue(dbPath string) (*Queue, error) {
	// Enable WAL mode and a busy timeout to prevent SQLITE_BUSY errors on concurrent access
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// Create table if not exists
	query := `
	CREATE TABLE IF NOT EXISTS telemetry (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		payload TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err = db.Exec(query)
	if err != nil {
		return nil, err
	}

	return &Queue{db: db}, nil
}

func (q *Queue) Push(payload map[string]interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = q.db.Exec("INSERT INTO telemetry (payload) VALUES (?)", string(data))
	return err
}

func (q *Queue) PopBatch(limit int) ([]TelemetryItem, error) {
	rows, err := q.db.Query("SELECT id, payload FROM telemetry ORDER BY created_at ASC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TelemetryItem
	for rows.Next() {
		var item TelemetryItem
		if err := rows.Scan(&item.ID, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (q *Queue) MarkSent(ids []int) error {
	if len(ids) == 0 {
		return nil
	}

	// Build a parameterized query: DELETE FROM telemetry WHERE id IN (?, ?, ...)
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := "DELETE FROM telemetry WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	_, err := q.db.Exec(query, args...)
	return err
}
