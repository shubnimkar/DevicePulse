package queue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/devicepulse/agent/safefs"
	_ "modernc.org/sqlite"
)

// While the API is unreachable the queue keeps growing. Defaults keep a week of
// data up to 512 MB (oldest rows are dropped first); both are tunable.
const (
	defaultQueueMaxMB       = 512
	defaultQueueMaxAgeHours = 168
	pruneEveryNPushes       = 100
)

type Queue struct {
	db     *sql.DB
	pushes atomic.Int64
}

type TelemetryItem struct {
	ID      int
	Payload string
}

func NewQueue(dbPath string) (*Queue, error) {
	ensureSharedSQLiteFiles(dbPath)

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
	ensureSharedSQLiteFiles(dbPath)

	q := &Queue{db: db}
	if err := q.Prune(); err != nil {
		log.Printf("Queue: startup prune failed: %v", err)
	}
	return q, nil
}

// ensureSharedSQLiteFiles makes the queue directory and database files usable by
// the per-user window service. The directory is writable by every local user and
// the agent runs as root here, so no path is ever chmod-ed by name: a planted
// symlink is removed (never followed) and permissions are set on the opened file.
func ensureSharedSQLiteFiles(dbPath string) {
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := safefs.ChmodDir(dir, 0777); err != nil && !os.IsNotExist(err) {
			log.Printf("Queue: not changing permissions of %s: %v", dir, err)
		}
	}
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if removed, err := safefs.RemoveIfSymlink(path); removed {
			log.Printf("Queue: removed unexpected symlink %s", path)
		} else if err != nil {
			log.Printf("Queue: cannot remove symlink %s: %v", path, err)
		}
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if err := safefs.ChmodFile(path, 0666); err != nil {
			log.Printf("Queue: not changing permissions of %s: %v", path, err)
		}
	}
}

func (q *Queue) Push(payload map[string]interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err = q.db.Exec("INSERT INTO telemetry (payload) VALUES (?)", string(data)); err != nil {
		return err
	}
	if q.pushes.Add(1)%pruneEveryNPushes == 0 {
		if err := q.Prune(); err != nil {
			log.Printf("Queue: prune failed: %v", err)
		}
	}
	return nil
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		return n
	}
	return def
}

// Prune drops rows older than DEVICEPULSE_QUEUE_MAX_AGE_HOURS and, if the queue
// is still larger than DEVICEPULSE_QUEUE_MAX_MB, the oldest rows beyond that size.
func (q *Queue) Prune() error {
	return q.pruneTo(
		int64(envInt("DEVICEPULSE_QUEUE_MAX_MB", defaultQueueMaxMB))<<20,
		time.Duration(envInt("DEVICEPULSE_QUEUE_MAX_AGE_HOURS", defaultQueueMaxAgeHours))*time.Hour,
	)
}

func (q *Queue) pruneTo(maxBytes int64, maxAge time.Duration) error {
	if _, err := q.db.Exec("DELETE FROM telemetry WHERE created_at < datetime('now', ?)",
		fmt.Sprintf("-%d seconds", int64(maxAge.Seconds()))); err != nil {
		return err
	}
	// Running total of payload bytes from the newest row backwards; the first row
	// that pushes it over the cap and everything older goes.
	var cutoff int64
	err := q.db.QueryRow(`SELECT id FROM (
		SELECT id, SUM(LENGTH(CAST(payload AS BLOB))) OVER (ORDER BY id DESC) AS running FROM telemetry
	) WHERE running > ? ORDER BY id DESC LIMIT 1`, maxBytes).Scan(&cutoff)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := q.db.Exec("DELETE FROM telemetry WHERE id <= ?", cutoff)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("Queue: dropped %d oldest unsent item(s) to stay under the size cap", n)
	}
	return nil
}

func (q *Queue) PopBatch(limit int) ([]TelemetryItem, error) {
	rows, err := q.db.Query("SELECT id, payload FROM telemetry ORDER BY id DESC LIMIT ?", limit)
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
