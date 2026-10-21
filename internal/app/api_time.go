package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/claym/crm/internal/calc"
	"github.com/jackc/pgx/v5"
)

func itoa(i int) string { return strconv.Itoa(i) }

func todayString() string { return time.Now().Format(calc.DateLayout) }

type TimeEntry struct {
	ID            int64   `db:"id" json:"id"`
	WorkerID      int64   `db:"worker_id" json:"worker_id"`
	WorkerName    string  `db:"worker_name" json:"worker_name"`
	ProjectID     int64   `db:"project_id" json:"project_id"`
	ProjectName   string  `db:"project_name" json:"project_name"`
	ClientName    *string `db:"client_name" json:"client_name"`
	BillingType   string  `db:"billing_type" json:"billing_type"`
	TaskID        *int64  `db:"task_id" json:"task_id"`
	TaskTitle     *string `db:"task_title" json:"task_title"`
	WorkDate      string  `db:"work_date" json:"work_date"`
	StartMinute   *int64  `db:"start_minute" json:"start_minute"`
	EndMinute     *int64  `db:"end_minute" json:"end_minute"`
	BreakMinutes  int64   `db:"break_minutes" json:"break_minutes"`
	Minutes       int64   `db:"minutes" json:"minutes"`
	PayRateCents  int64   `db:"pay_rate_cents" json:"pay_rate_cents"`
	Billable      bool    `db:"billable" json:"billable"`
	Notes         string  `db:"notes" json:"notes"`
	InvoiceID     *int64  `db:"invoice_id" json:"invoice_id"`
	InvoiceNumber *int64  `db:"invoice_number" json:"invoice_number"`
	PayRunID      *int64  `db:"pay_run_id" json:"pay_run_id"`
	PayRunStatus  *string `db:"pay_run_status" json:"pay_run_status"`
}

// Locked entries are on an invoice or pay run and can't be edited until released.
func (t *TimeEntry) Locked() bool { return t.InvoiceID != nil || t.PayRunID != nil }

const timeEntrySelect = `
	SELECT t.id, t.worker_id, w.name AS worker_name, t.project_id, p.name AS project_name, c.name AS client_name,
	  p.billing_type, t.task_id, k.title AS task_title, t.work_date::text,
	  t.start_minute::bigint, t.end_minute::bigint, t.break_minutes::bigint, t.minutes::bigint, t.pay_rate_cents::bigint,
	  t.billable, t.notes, t.invoice_id, i.number::bigint AS invoice_number, t.pay_run_id, pr.status AS pay_run_status
	FROM time_entries t
	JOIN workers w ON w.id = t.worker_id
	JOIN projects p ON p.id = t.project_id
	LEFT JOIN clients c ON c.id = p.client_id
	LEFT JOIN tasks k ON k.id = t.task_id
	LEFT JOIN invoices i ON i.id = t.invoice_id
	LEFT JOIN pay_runs pr ON pr.id = t.pay_run_id`

// GET /api/time-entries?worker_id=&project_id=&client_id=&from=&to=&unbilled=1&unpaid=1
func (s *Server) listTimeEntries(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	where, args := []string{}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	workerID, err := queryID(r, "worker_id")
	if err != nil {
		return err
	}
	if !u.isManager() {
		if u.WorkerID == nil {
			return ok(w, []TimeEntry{})
		}
		workerID = u.WorkerID // workers only ever see their own time
	}
	if workerID != nil {
		add("t.worker_id = ?", *workerID)
	}
	for _, f := range []struct{ param, col string }{{"project_id", "t.project_id"}, {"client_id", "p.client_id"}, {"invoice_id", "t.invoice_id"}, {"pay_run_id", "t.pay_run_id"}} {
		id, err := queryID(r, f.param)
		if err != nil {
			return err
		}
		if id != nil {
			add(f.col+" = ?", *id)
		}
	}
	if from, err := queryDate(r, "from"); err != nil {
		return err
	} else if from != nil {
		add("t.work_date >= ?::date", *from)
	}
	if to, err := queryDate(r, "to"); err != nil {
		return err
	} else if to != nil {
		add("t.work_date <= ?::date", *to)
	}
	if r.URL.Query().Get("unbilled") == "1" {
		where = append(where, "t.invoice_id IS NULL AND t.billable AND p.billing_type = 'hourly'")
	}
	if r.URL.Query().Get("unpaid") == "1" {
		where = append(where, "t.pay_run_id IS NULL")
	}
	sql := timeEntrySelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	list, err := collect[TimeEntry](r.Context(), s.db, sql+` ORDER BY t.work_date, t.start_minute NULLS LAST, t.id LIMIT 5000`, args...)
	if err != nil {
		return err
	}
	return ok(w, list)
}

type timeEntryInput struct {
	WorkerID     int64  `json:"worker_id"`
	ProjectID    int64  `json:"project_id"`
	TaskID       *int64 `json:"task_id"`
	WorkDate     string `json:"work_date"`
	Start        string `json:"start"` // "7:00", "7:00am", "15:30" — or empty for a manual duration
	End          string `json:"end"`
	BreakMinutes int    `json:"break_minutes"`
	Minutes      *int   `json:"minutes"` // used only when start/end are empty
	Billable     *bool  `json:"billable"`
	Notes        string `json:"notes"`
	PayRateCents *int64 `json:"pay_rate_cents"` // managers only; default from project override or worker rate
}

type resolvedEntry struct {
	workDate     string
	start, end   *int
	breakMinutes int
	minutes      int
	billable     bool
}

func (in *timeEntryInput) resolve() (*resolvedEntry, error) {
	var err error
	out := &resolvedEntry{breakMinutes: in.BreakMinutes, billable: true}
	if in.Billable != nil {
		out.billable = *in.Billable
	}
	if in.WorkerID <= 0 {
		return nil, errBad("choose a worker")
	}
	if in.ProjectID <= 0 {
		return nil, errBad("choose a project")
	}
	if out.workDate, err = requireDate("date", in.WorkDate); err != nil {
		return nil, err
	}
	if len(in.Notes) > 2000 {
		return nil, errBad("notes are too long")
	}
	start, end := strings.TrimSpace(in.Start), strings.TrimSpace(in.End)
	switch {
	case start != "" || end != "":
		if start == "" || end == "" {
			return nil, errBad("enter both a start and an end time (or neither, with a duration)")
		}
		sm, err := calc.ParseClock(start)
		if err != nil {
			return nil, errBad("start: %v", err)
		}
		em, err := calc.ParseClock(end)
		if err != nil {
			return nil, errBad("end: %v", err)
		}
		mins, err := calc.WorkedMinutes(sm, em, in.BreakMinutes)
		if err != nil {
			return nil, errBad("%v", err)
		}
		out.start, out.end, out.minutes = &sm, &em, mins
	case in.Minutes != nil:
		if *in.Minutes <= 0 || *in.Minutes > 24*60 {
			return nil, errBad("duration must be between 1 minute and 24 hours")
		}
		out.minutes, out.breakMinutes = *in.Minutes, 0
	default:
		return nil, errBad("enter start and end times, or a duration")
	}
	return out, nil
}

// checkEntryRefs validates the task belongs to the project and returns the default pay rate.
func (s *Server) checkEntryRefs(ctx context.Context, q pgx.Tx, in *timeEntryInput) (int64, error) {
	var rate int64
	var active bool
	err := q.QueryRow(ctx, `
		SELECT coalesce(j.pay_rate_override_cents, w.pay_rate_cents)::bigint, w.active
		FROM projects j, workers w WHERE j.id = $1 AND w.id = $2`, in.ProjectID, in.WorkerID).Scan(&rate, &active)
	if err == pgx.ErrNoRows {
		return 0, errBad("unknown worker or project")
	}
	if err != nil {
		return 0, err
	}
	if in.TaskID != nil {
		var okTask bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND project_id = $2)`, *in.TaskID, in.ProjectID).Scan(&okTask); err != nil {
			return 0, err
		}
		if !okTask {
			return 0, errBad("that task belongs to a different project")
		}
	}
	return rate, nil
}

func (s *Server) authorizeEntryWorker(u *User, workerID int64) error {
	if u.isManager() {
		return nil
	}
	if u.WorkerID == nil || *u.WorkerID != workerID {
		return errForbidden
	}
	return nil
}

func (s *Server) createTimeEntry(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	var in timeEntryInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if !u.isManager() && u.WorkerID != nil && in.WorkerID == 0 {
		in.WorkerID = *u.WorkerID
	}
	if err := s.authorizeEntryWorker(u, in.WorkerID); err != nil {
		return err
	}
	res, err := in.resolve()
	if err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rate, err := s.checkEntryRefs(ctx, tx, &in)
	if err != nil {
		return err
	}
	if in.PayRateCents != nil && u.isManager() {
		if *in.PayRateCents < 0 {
			return errBad("pay rate cannot be negative")
		}
		rate = *in.PayRateCents
	}
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO time_entries (worker_id, project_id, task_id, work_date, start_minute, end_minute, break_minutes,
		  minutes, pay_rate_cents, billable, notes, created_by)
		VALUES ($1,$2,$3,$4::date,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		in.WorkerID, in.ProjectID, in.TaskID, res.workDate, res.start, res.end, res.breakMinutes, res.minutes,
		rate, res.billable, strings.TrimSpace(in.Notes), u.ID).Scan(&id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e, err := one[TimeEntry](ctx, s.db, timeEntrySelect+` WHERE t.id = $1`, id)
	if err != nil {
		return err
	}
	return created(w, e)
}

func (s *Server) loadEntryForWrite(ctx context.Context, tx pgx.Tx, u *User, id int64) (*TimeEntry, error) {
	e, err := one[TimeEntry](ctx, tx, timeEntrySelect+` WHERE t.id = $1 FOR UPDATE OF t`, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeEntryWorker(u, e.WorkerID); err != nil {
		return nil, err
	}
	if e.InvoiceID != nil {
		return nil, errConflict("this entry is on invoice #%d; remove it from the invoice first", derefInt(e.InvoiceNumber))
	}
	if e.PayRunID != nil {
		return nil, errConflict("this entry is included in a pay run; delete the draft pay run first")
	}
	return &e, nil
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func (s *Server) updateTimeEntry(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in timeEntryInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.authorizeEntryWorker(u, in.WorkerID); err != nil {
		return err
	}
	res, err := in.resolve()
	if err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cur, err := s.loadEntryForWrite(ctx, tx, u, id)
	if err != nil {
		return err
	}
	defaultRate, err := s.checkEntryRefs(ctx, tx, &in)
	if err != nil {
		return err
	}
	// Keep the original rate snapshot unless the worker/project changed or a manager overrides it.
	rate := cur.PayRateCents
	if cur.WorkerID != in.WorkerID || cur.ProjectID != in.ProjectID {
		rate = defaultRate
	}
	if in.PayRateCents != nil && u.isManager() {
		if *in.PayRateCents < 0 {
			return errBad("pay rate cannot be negative")
		}
		rate = *in.PayRateCents
	}
	if _, err := tx.Exec(ctx, `
		UPDATE time_entries SET worker_id=$2, project_id=$3, task_id=$4, work_date=$5::date, start_minute=$6, end_minute=$7,
		  break_minutes=$8, minutes=$9, pay_rate_cents=$10, billable=$11, notes=$12, updated_at=now()
		WHERE id=$1`,
		id, in.WorkerID, in.ProjectID, in.TaskID, res.workDate, res.start, res.end, res.breakMinutes, res.minutes,
		rate, res.billable, strings.TrimSpace(in.Notes)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e, err := one[TimeEntry](ctx, s.db, timeEntrySelect+` WHERE t.id = $1`, id)
	if err != nil {
		return err
	}
	return ok(w, e)
}

func (s *Server) deleteTimeEntry(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := s.loadEntryForWrite(ctx, tx, u, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM time_entries WHERE id = $1`, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return noContent(w)
}
