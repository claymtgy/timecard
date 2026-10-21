package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/claym/crm/internal/calc"
	"github.com/jackc/pgx/v5"
)

// Clock in / out. A worker has at most one open clock session; clocking out converts
// it into time entries (split at midnight in the company time zone).

type ClockSession struct {
	WorkerID    int64   `db:"worker_id" json:"worker_id"`
	WorkerName  string  `db:"worker_name" json:"worker_name"`
	ProjectID   int64   `db:"project_id" json:"project_id"`
	ProjectName string  `db:"project_name" json:"project_name"`
	ClientName  *string `db:"client_name" json:"client_name"`
	TaskID      *int64  `db:"task_id" json:"task_id"`
	TaskTitle   *string `db:"task_title" json:"task_title"`
	StartedAt   string  `db:"started_at" json:"started_at"` // RFC 3339, UTC
	Notes       string  `db:"notes" json:"notes"`
}

const clockSelect = `
	SELECT s.worker_id, w.name AS worker_name, s.project_id, p.name AS project_name, c.name AS client_name,
	  s.task_id, k.title AS task_title,
	  to_char(s.started_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS started_at, s.notes
	FROM clock_sessions s
	JOIN workers w ON w.id = s.worker_id
	JOIN projects p ON p.id = s.project_id
	LEFT JOIN clients c ON c.id = p.client_id
	LEFT JOIN tasks k ON k.id = s.task_id`

type lastClockChoice struct {
	ProjectID    int64  `db:"project_id" json:"project_id"`
	TaskID       *int64 `db:"task_id" json:"task_id"`
	BreakMinutes int64  `db:"break_minutes" json:"break_minutes"`
}

// clockWorker resolves whose clock is being used: the caller's own worker, or (managers) ?worker_id / body worker_id.
func clockWorker(u *User, requested *int64) (int64, error) {
	if requested != nil && *requested > 0 {
		if !u.isManager() && (u.WorkerID == nil || *u.WorkerID != *requested) {
			return 0, errForbidden
		}
		return *requested, nil
	}
	if u.WorkerID == nil {
		return 0, &apiError{http.StatusConflict, "your sign-in isn't linked to a worker yet; ask an admin to link it on the Users page"}
	}
	return *u.WorkerID, nil
}

// GET /api/clock[?worker_id=]
func (s *Server) getClock(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	req, err := queryID(r, "worker_id")
	if err != nil {
		return err
	}
	if req == nil && u.WorkerID == nil {
		return ok(w, map[string]any{"linked": false, "server_time": time.Now().UTC().Format(time.RFC3339)})
	}
	workerID, err := clockWorker(u, req)
	if err != nil {
		return err
	}
	ctx := r.Context()
	resp := map[string]any{"linked": true, "worker_id": workerID, "server_time": time.Now().UTC().Format(time.RFC3339)}
	sess, err := one[ClockSession](ctx, s.db, clockSelect+` WHERE s.worker_id = $1`, workerID)
	switch {
	case err == nil:
		resp["session"] = sess
	case errors.Is(err, pgx.ErrNoRows):
		resp["session"] = nil
	default:
		return err
	}
	// Prefill from the most recent entry (the previous clock-in).
	last, err := one[lastClockChoice](ctx, s.db, `
		SELECT project_id, task_id, break_minutes::bigint FROM time_entries
		WHERE worker_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, workerID)
	switch {
	case err == nil:
		resp["last"] = last
	case errors.Is(err, pgx.ErrNoRows):
		resp["last"] = nil
	default:
		return err
	}
	return ok(w, resp)
}

// GET /api/clock/active — who is on the clock right now (managers).
func (s *Server) activeClocks(w http.ResponseWriter, r *http.Request) error {
	list, err := collect[ClockSession](r.Context(), s.db, clockSelect+` ORDER BY s.started_at`)
	if err != nil {
		return err
	}
	return ok(w, list)
}

type clockInput struct {
	WorkerID     *int64 `json:"worker_id"`
	ProjectID    int64  `json:"project_id"`
	TaskID       *int64 `json:"task_id"`
	Notes        string `json:"notes"`
	BreakMinutes int    `json:"break_minutes"` // clock out / switch: break taken during the session
}

func (s *Server) validateClockTarget(ctx context.Context, tx pgx.Tx, in *clockInput) error {
	if in.ProjectID <= 0 {
		return errBad("choose a project")
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM projects WHERE id = $1`, in.ProjectID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errBad("unknown project")
		}
		return err
	}
	if status != "active" && status != "planned" {
		return errBad("that project is closed")
	}
	if in.TaskID != nil {
		var okTask bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND project_id = $2)`, *in.TaskID, in.ProjectID).Scan(&okTask); err != nil {
			return err
		}
		if !okTask {
			return errBad("that task belongs to a different project")
		}
	}
	return nil
}

func (s *Server) startClock(ctx context.Context, tx pgx.Tx, u *User, workerID int64, in *clockInput, at time.Time) error {
	if err := s.validateClockTarget(ctx, tx, in); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO clock_sessions (worker_id, project_id, task_id, started_at, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (worker_id) DO NOTHING`,
		workerID, in.ProjectID, in.TaskID, at, strings.TrimSpace(in.Notes), u.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errConflict("already clocked in; clock out or switch tasks instead")
	}
	return nil
}

// stopClock closes the worker's session at `at` and writes its time entries.
func (s *Server) stopClock(ctx context.Context, tx pgx.Tx, u *User, workerID int64, breakMinutes int, notes string, at time.Time) ([]int64, error) {
	var projectID int64
	var taskID *int64
	var started time.Time
	var sessNotes string
	err := tx.QueryRow(ctx, `
		DELETE FROM clock_sessions WHERE worker_id = $1 RETURNING project_id, task_id, started_at, notes`, workerID).
		Scan(&projectID, &taskID, &started, &sessNotes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errConflict("not clocked in")
	}
	if err != nil {
		return nil, err
	}
	segs, err := calc.SplitShift(started.In(s.loc), at.In(s.loc), breakMinutes)
	if err != nil {
		return nil, errBad("%v", err)
	}
	var rate int64
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(p.pay_rate_override_cents, w.pay_rate_cents)::bigint FROM projects p, workers w
		WHERE p.id = $1 AND w.id = $2`, projectID, workerID).Scan(&rate); err != nil {
		return nil, err
	}
	allNotes := strings.TrimSpace(strings.Join(nonEmpty(sessNotes, strings.TrimSpace(notes)), "\n"))
	var ids []int64
	for _, sg := range segs {
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO time_entries (worker_id, project_id, task_id, work_date, start_minute, end_minute, break_minutes,
			  minutes, pay_rate_cents, notes, created_by)
			VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
			workerID, projectID, taskID, sg.Date, sg.Start, sg.End, sg.Break, sg.End-sg.Start-sg.Break, rate, allNotes, u.ID).Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) clockAction(w http.ResponseWriter, r *http.Request, action string) error {
	u := currentUser(r)
	var in clockInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	workerID, err := clockWorker(u, in.WorkerID)
	if err != nil {
		return err
	}
	if in.BreakMinutes < 0 || in.BreakMinutes > 24*60 {
		return errBad("break must be between 0 and 1440 minutes")
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now().Truncate(time.Minute)
	var ids []int64
	switch action {
	case "in":
		err = s.startClock(ctx, tx, u, workerID, &in, now)
	case "out":
		ids, err = s.stopClock(ctx, tx, u, workerID, in.BreakMinutes, in.Notes, now)
	case "switch":
		if ids, err = s.stopClock(ctx, tx, u, workerID, in.BreakMinutes, "", now); err == nil {
			err = s.startClock(ctx, tx, u, workerID, &in, now)
		}
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	entries := []TimeEntry{}
	if len(ids) > 0 {
		if entries, err = collect[TimeEntry](ctx, s.db, timeEntrySelect+` WHERE t.id = ANY($1) ORDER BY t.work_date`, ids); err != nil {
			return err
		}
	}
	var sess *ClockSession
	if action != "out" {
		cs, err := one[ClockSession](ctx, s.db, clockSelect+` WHERE s.worker_id = $1`, workerID)
		if err != nil {
			return err
		}
		sess = &cs
	}
	return ok(w, map[string]any{"session": sess, "entries": entries})
}

func (s *Server) clockIn(w http.ResponseWriter, r *http.Request) error {
	return s.clockAction(w, r, "in")
}
func (s *Server) clockOut(w http.ResponseWriter, r *http.Request) error {
	return s.clockAction(w, r, "out")
}
func (s *Server) clockSwitch(w http.ResponseWriter, r *http.Request) error {
	return s.clockAction(w, r, "switch")
}

// DELETE /api/clock[?worker_id=] — discard an accidental clock-in without recording time.
func (s *Server) cancelClock(w http.ResponseWriter, r *http.Request) error {
	req, err := queryID(r, "worker_id")
	if err != nil {
		return err
	}
	workerID, err := clockWorker(currentUser(r), req)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM clock_sessions WHERE worker_id = $1`, workerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errConflict("not clocked in")
	}
	return noContent(w)
}
