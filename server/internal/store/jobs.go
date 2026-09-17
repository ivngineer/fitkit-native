package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

type ImportJob struct {
	ID                string
	UserID            string
	PinterestUsername string
	Status            JobStatus
	Discovered        int
	Imported          int
	Reused            int
	Failed            int
	ErrorCode         string
	ErrorMessage      string
	Retryable         bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	FinishedAt        *time.Time
}

func (j ImportJob) Active() bool { return j.Status == JobQueued || j.Status == JobRunning }

const jobColumns = `id, user_id, pinterest_username, status, discovered, imported, reused, failed, error_code, error_message, retryable, created_at, updated_at, finished_at`

func scanJob(row interface{ Scan(...any) error }) (ImportJob, error) {
	var j ImportJob
	var created, updated int64
	var finished sql.NullInt64
	err := row.Scan(&j.ID, &j.UserID, &j.PinterestUsername, &j.Status, &j.Discovered, &j.Imported, &j.Reused,
		&j.Failed, &j.ErrorCode, &j.ErrorMessage, &j.Retryable, &created, &updated, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportJob{}, ErrNotFound
	}
	j.CreatedAt, j.UpdatedAt = fromMS(created), fromMS(updated)
	if finished.Valid {
		t := fromMS(finished.Int64)
		j.FinishedAt = &t
	}
	return j, err
}

// CreateOrReuseImportJob returns the user's active job if one exists,
// otherwise it creates a queued job. created reports which happened.
func (s *Store) CreateOrReuseImportJob(ctx context.Context, userID, username string) (job ImportJob, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportJob{}, false, err
	}
	defer tx.Rollback()
	job, err = scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM import_jobs
		WHERE user_id = ? AND status IN ('queued', 'running') ORDER BY created_at DESC LIMIT 1`, userID))
	if err == nil {
		return job, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ImportJob{}, false, err
	}
	now := s.now().UTC()
	job = ImportJob{ID: NewID(), UserID: userID, PinterestUsername: username, Status: JobQueued, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, `INSERT INTO import_jobs (id, user_id, pinterest_username, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, job.ID, userID, username, job.Status, ms(now), ms(now)); err != nil {
		return ImportJob{}, false, err
	}
	return job, true, tx.Commit()
}

func (s *Store) ImportJob(ctx context.Context, id string) (ImportJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM import_jobs WHERE id = ?`, id))
}

func (s *Store) LatestImportJob(ctx context.Context, userID string) (ImportJob, error) {
	return scanJob(s.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM import_jobs WHERE user_id = ? ORDER BY created_at DESC LIMIT 1`, userID))
}

// ActiveImportJobIDs lists jobs interrupted by a restart so they can resume.
func (s *Store) ActiveImportJobIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM import_jobs WHERE status IN ('queued', 'running') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) MarkImportRunning(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE import_jobs SET status = 'running', updated_at = ? WHERE id = ?`, ms(s.now()), id)
	return err
}

func (s *Store) UpdateImportProgress(ctx context.Context, id string, discovered, imported, reused, failed int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE import_jobs SET discovered = ?, imported = ?, reused = ?, failed = ?, updated_at = ?
		WHERE id = ?`, discovered, imported, reused, failed, ms(s.now()), id)
	return err
}

func (s *Store) FinishImportJob(ctx context.Context, id string, status JobStatus, code, message string, retryable bool) error {
	now := ms(s.now())
	_, err := s.db.ExecContext(ctx, `UPDATE import_jobs SET status = ?, error_code = ?, error_message = ?, retryable = ?,
		updated_at = ?, finished_at = ? WHERE id = ?`, status, code, message, retryable, now, now, id)
	return err
}

type Analysis struct {
	PinID        string
	Status       JobStatus
	ResultJSON   string
	ErrorCode    string
	ErrorMessage string
	UpdatedAt    time.Time
}

func (s *Store) Analysis(ctx context.Context, pinID string) (Analysis, error) {
	var a Analysis
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT pin_id, status, result_json, error_code, error_message, updated_at
		FROM pin_analyses WHERE pin_id = ?`, pinID).Scan(&a.PinID, &a.Status, &a.ResultJSON, &a.ErrorCode, &a.ErrorMessage, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Analysis{}, ErrNotFound
	}
	a.UpdatedAt = fromMS(updated)
	return a, err
}

func (s *Store) PutAnalysis(ctx context.Context, a Analysis) error {
	now := ms(s.now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO pin_analyses (pin_id, status, result_json, error_code, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pin_id) DO UPDATE SET status = excluded.status, result_json = excluded.result_json,
		error_code = excluded.error_code, error_message = excluded.error_message, updated_at = excluded.updated_at`,
		a.PinID, a.Status, a.ResultJSON, a.ErrorCode, a.ErrorMessage, now, now)
	return err
}

// ResetStaleAnalyses fails analyses left running by a previous process.
func (s *Store) ResetStaleAnalyses(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pin_analyses SET status = 'failed', error_code = 'interrupted',
		error_message = 'Analysis was interrupted. Try again.' WHERE status IN ('queued', 'running')`)
	return err
}
