// Package main — storage: SQLite backend for experiments and results.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Storage wraps a SQLite database for experiment persistence.
type Storage struct {
	db *sql.DB
}

// storedResult is a Result joined with its Experiment's vector for analysis.
type storedResult struct {
	ExperimentID int64
	Vector       ParamVector
	Metrics      MetricSet
	Duration     float64
	ExitCode     int
	Phase        string
	Timestamp    time.Time
}

func OpenStorage(path string) (*Storage, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Storage{db: db}
	if err := s.init(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return s, nil
}

func (s *Storage) init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS experiments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		vector TEXT NOT NULL,       -- JSON ParamVector
		phase TEXT NOT NULL,        -- "lhs"|"sobol"|"adaptive"
		manifest TEXT,              -- reproducible manifest
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS results (
		experiment_id INTEGER PRIMARY KEY,
		metrics TEXT NOT NULL,      -- JSON MetricSet
		duration_sec REAL,
		exit_code INTEGER,
		error TEXT,
		stdout TEXT,
		timestamp TEXT NOT NULL,
		FOREIGN KEY(experiment_id) REFERENCES experiments(id)
	);
	CREATE INDEX IF NOT EXISTS idx_results ON results(experiment_id);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Storage) SaveExperiment(exp *Experiment) error {
	vectorJSON, _ := json.Marshal(exp.Vector)
	_, err := s.db.Exec(
		`INSERT INTO experiments (vector, phase, manifest, created_at) VALUES (?, ?, ?, ?)`,
		string(vectorJSON), exp.Phase, exp.Manifest, exp.CreatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return err
	}
	return s.db.QueryRow("SELECT last_insert_rowid()").Scan(&exp.ID)
}

func (s *Storage) SaveResult(r *Result) error {
	metricsJSON, _ := json.Marshal(r.Metrics)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO results (experiment_id, metrics, duration_sec, exit_code, error, stdout, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ExperimentID, string(metricsJSON), r.Duration, r.ExitCode, r.Error, r.Stdout,
		r.Timestamp.Format(time.RFC3339),
	)
	return err
}

func (s *Storage) AllResults() ([]storedResult, error) {
	rows, err := s.db.Query(`
		SELECT e.id, e.vector, e.phase, r.metrics, r.duration_sec, r.exit_code, r.timestamp
		FROM experiments e
		JOIN results r ON r.experiment_id = e.id
		WHERE r.exit_code = 0
		ORDER BY e.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []storedResult
	for rows.Next() {
		var sr storedResult
		var vectorJSON, metricsJSON string
		var ts string
		if err := rows.Scan(&sr.ExperimentID, &vectorJSON, &sr.Phase, &metricsJSON, &sr.Duration, &sr.ExitCode, &ts); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(vectorJSON), &sr.Vector)
		json.Unmarshal([]byte(metricsJSON), &sr.Metrics)
		sr.Timestamp, _ = time.Parse(time.RFC3339, ts)
		results = append(results, sr)
	}
	return results, rows.Err()
}

func (s *Storage) AllVectors() ([]ParamVector, error) {
	rows, err := s.db.Query(`SELECT vector FROM experiments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vectors []ParamVector
	for rows.Next() {
		var vJSON string
		var v ParamVector
		if err := rows.Scan(&vJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(vJSON), &v)
		vectors = append(vectors, v)
	}
	return vectors, rows.Err()
}

func (s *Storage) Close() error { return s.db.Close() }
