// Package storage persists experiments, results, attempts, telemetry, and
// run provenance to SQLite (via the pure-Go modernc.org/sqlite driver).
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/qumo-dev/qumo/tools/paramexp/experiment"
)

// Storage wraps a SQLite database.
type Storage struct {
	db *sql.DB
}

// Attempt is one execution attempt of an experiment (one row per retry).
type Attempt struct {
	ExperimentID int64
	Attempt      int
	StartedAt    time.Time
	DurationSec  float64
	ExitCode     int
	TimedOut     bool
	Error        string
	Stdout       string
	Stderr       string
	Telemetry    *experiment.Telemetry
}

// Open opens (creating if needed) the database at path and applies the schema.
// Use ":memory:" for an in-memory database (testing).
func Open(path string) (*Storage, error) {
	// Pragmas are applied to every connection via the DSN (modernc/sqlite), so
	// file and in-memory DBs behave identically (foreign keys ON, etc.).
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc/sqlite gives each pooled connection its own private in-memory
	// database, so pin a single connection for ":memory:" — otherwise a query
	// routed to a different connection sees an empty DB.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	s := &Storage{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return s, nil
}

func (s *Storage) init() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	return s.migrate()
}

// migrate adds columns introduced after the initial schema, guarded by
// introspection so existing DBs keep working.
func (s *Storage) migrate() error {
	return nil // current schema is created whole; placeholder for future columns.
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at         TEXT NOT NULL,
	finished_at        TEXT,
	framework_version  TEXT,
	git_revision       TEXT,
	git_dirty          INTEGER,
	config_hash        TEXT,
	config_json        TEXT,
	machine_json       TEXT,
	env_json           TEXT
);
CREATE TABLE IF NOT EXISTS experiments (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id      INTEGER NOT NULL REFERENCES runs(id),
	vector      TEXT NOT NULL,
	encoded_x   TEXT,
	phase       TEXT NOT NULL,
	manifest    TEXT,
	created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_experiments_run ON experiments(run_id);
CREATE TABLE IF NOT EXISTS attempts (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	experiment_id   INTEGER NOT NULL REFERENCES experiments(id),
	attempt         INTEGER NOT NULL,
	started_at      TEXT NOT NULL,
	duration_sec    REAL,
	exit_code       INTEGER,
	timed_out       INTEGER DEFAULT 0,
	error           TEXT,
	stdout          TEXT,
	stderr          TEXT,
	telemetry_json  TEXT
);
CREATE INDEX IF NOT EXISTS idx_attempts_exp ON attempts(experiment_id);
CREATE TABLE IF NOT EXISTS results (
	experiment_id   INTEGER PRIMARY KEY REFERENCES experiments(id),
	attempt_count   INTEGER DEFAULT 1,
	metrics         TEXT NOT NULL,
	duration_sec    REAL,
	exit_code       INTEGER,
	error           TEXT,
	stdout          TEXT,
	stderr          TEXT,
	timestamp       TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS telemetry (
	experiment_id   INTEGER PRIMARY KEY REFERENCES experiments(id),
	cpu_pct         REAL,
	gc_pause_ms     REAL,
	syscalls        REAL,
	retransmits     REAL,
	rss_mb          REAL,
	goroutines      REAL,
	raw             TEXT
);
`

// SaveRun inserts a provenance Run and writes back its ID.
func (s *Storage) SaveRun(r *Run) error {
	dirty := 0
	if r.GitDirty {
		dirty = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO runs (started_at, finished_at, framework_version, git_revision, git_dirty, config_hash, config_json, machine_json, env_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.StartedAt.Format(time.RFC3339Nano),
		nilTime(r.FinishedAt),
		r.FrameworkVersion, r.GitRevision, dirty, r.ConfigHash, r.ConfigJSON, r.MachineJSON, r.EnvJSON,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	return nil
}

// FinishRun stamps the run's finished_at.
func (s *Storage) FinishRun(id int64) error {
	_, err := s.db.Exec(`UPDATE runs SET finished_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// SaveExperiment inserts an experiment, writing back its ID.
func (s *Storage) SaveExperiment(e *experiment.Experiment) error {
	vectorJSON, _ := json.Marshal(e.Vector)
	var encJSON any
	if len(e.EncodedX) > 0 {
		b, _ := json.Marshal(e.EncodedX)
		encJSON = string(b)
	}
	res, err := s.db.Exec(
		`INSERT INTO experiments (run_id, vector, encoded_x, phase, manifest, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		e.RunID, string(vectorJSON), encJSON, e.Phase, e.Manifest, e.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	e.ID = id
	return nil
}

// AppendAttempt logs one execution attempt.
func (s *Storage) AppendAttempt(a Attempt) error {
	var telJSON any
	if a.Telemetry != nil {
		b, _ := json.Marshal(a.Telemetry)
		telJSON = string(b)
	}
	timedOut := 0
	if a.TimedOut {
		timedOut = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO attempts (experiment_id, attempt, started_at, duration_sec, exit_code, timed_out, error, stdout, stderr, telemetry_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ExperimentID, a.Attempt, a.StartedAt.UTC().Format(time.RFC3339Nano),
		a.DurationSec, a.ExitCode, timedOut, a.Error, a.Stdout, a.Stderr, telJSON,
	)
	return err
}

// SaveResult upserts the successful/result rollup for an experiment.
func (s *Storage) SaveResult(r *experiment.Result) error {
	metricsJSON, _ := json.Marshal(r.Metrics)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO results (experiment_id, attempt_count, metrics, duration_sec, exit_code, error, stdout, stderr, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ExperimentID, r.Attempts, string(metricsJSON), r.Duration, r.ExitCode, r.Error, truncate(r.Stdout, 4096), r.Stderr,
		r.Timestamp.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// SaveTelemetry upserts the resource snapshot for an experiment.
func (s *Storage) SaveTelemetry(expID int64, t *experiment.Telemetry) error {
	if t == nil {
		return nil
	}
	var rawJSON any
	if t.Raw != nil {
		b, _ := json.Marshal(t.Raw)
		rawJSON = string(b)
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO telemetry (experiment_id, cpu_pct, gc_pause_ms, syscalls, retransmits, rss_mb, goroutines, raw)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		expID, t.CPUpct, t.GCPauseMs, t.Syscalls, t.Retransmits, t.RSSmb, t.Goroutines, rawJSON,
	)
	return err
}

// Observations returns the analysis-oriented join of experiments and results.
// When includeFailures is false, only successful (exit_code = 0) runs are returned.
func (s *Storage) Observations(includeFailures bool) ([]experiment.Observation, error) {
	q := `SELECT e.id, e.vector, e.encoded_x, e.phase, r.metrics, r.duration_sec, r.exit_code, r.timestamp
	      FROM experiments e JOIN results r ON r.experiment_id = e.id`
	if !includeFailures {
		q += ` WHERE r.exit_code = 0`
	}
	q += ` ORDER BY e.id`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []experiment.Observation
	for rows.Next() {
		var o experiment.Observation
		var vectorJSON, metricsJSON string
		var encJSON sql.NullString
		var ts string
		if err := rows.Scan(&o.ExperimentID, &vectorJSON, &encJSON, &o.Phase, &metricsJSON, &o.Duration, &o.ExitCode, &ts); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(vectorJSON), &o.Vector)
		_ = json.Unmarshal([]byte(metricsJSON), &o.Metrics)
		if encJSON.Valid && encJSON.String != "" {
			_ = json.Unmarshal([]byte(encJSON.String), &o.EncodedX)
		}
		o.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, o)
	}
	return out, rows.Err()
}

// AllVectors returns the parameter vectors of every experiment (regardless of result).
func (s *Storage) AllVectors() ([]experiment.ParamVector, error) {
	rows, err := s.db.Query(`SELECT vector FROM experiments ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []experiment.ParamVector
	for rows.Next() {
		var vJSON string
		var v experiment.ParamVector
		if err := rows.Scan(&vJSON); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(vJSON), &v)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Close closes the database.
func (s *Storage) Close() error { return s.db.Close() }

func nilTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
