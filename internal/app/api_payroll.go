package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Per-entry wages are rounded to the cent, then summed.
const entryPaySQL = `round(t.minutes * t.pay_rate_cents / 60.0)`

type PayRun struct {
	ID               int64   `db:"id" json:"id"`
	WorkerID         int64   `db:"worker_id" json:"worker_id"`
	WorkerName       string  `db:"worker_name" json:"worker_name"`
	PeriodStart      string  `db:"period_start" json:"period_start"`
	PeriodEnd        string  `db:"period_end" json:"period_end"`
	Status           string  `db:"status" json:"status"`
	PaidDate         *string `db:"paid_date" json:"paid_date"`
	Notes            string  `db:"notes" json:"notes"`
	Minutes          int64   `db:"minutes" json:"minutes"`
	WagesCents       int64   `db:"wages_cents" json:"wages_cents"`
	AdjustmentsCents int64   `db:"adjustments_cents" json:"adjustments_cents"`
	TotalCents       int64   `db:"total_cents" json:"total_cents"`
}

const payRunSelect = `
	SELECT r.id, r.worker_id, w.name AS worker_name, r.period_start::text, r.period_end::text, r.status,
	  r.paid_date::text, r.notes, coalesce(e.minutes, 0)::bigint AS minutes, coalesce(e.wages, 0)::bigint AS wages_cents,
	  coalesce(a.total, 0)::bigint AS adjustments_cents, (coalesce(e.wages, 0) + coalesce(a.total, 0))::bigint AS total_cents
	FROM pay_runs r JOIN workers w ON w.id = r.worker_id
	LEFT JOIN LATERAL (SELECT sum(t.minutes) AS minutes, sum(` + entryPaySQL + `) AS wages
	                   FROM time_entries t WHERE t.pay_run_id = r.id) e ON true
	LEFT JOIN LATERAL (SELECT sum(x.amount_cents) AS total FROM pay_adjustments x WHERE x.pay_run_id = r.id) a ON true`

type Adjustment struct {
	ID          int64   `db:"id" json:"id"`
	WorkerID    int64   `db:"worker_id" json:"worker_id"`
	WorkerName  string  `db:"worker_name" json:"worker_name"`
	ProjectID   *int64  `db:"project_id" json:"project_id"`
	ProjectName *string `db:"project_name" json:"project_name"`
	Kind        string  `db:"kind" json:"kind"`
	Description string  `db:"description" json:"description"`
	AmountCents int64   `db:"amount_cents" json:"amount_cents"`
	AdjDate     string  `db:"adj_date" json:"adj_date"`
	PayRunID    *int64  `db:"pay_run_id" json:"pay_run_id"`
}

const adjustmentSelect = `
	SELECT x.id, x.worker_id, w.name AS worker_name, x.project_id, p.name AS project_name, x.kind, x.description,
	  x.amount_cents, x.adj_date::text, x.pay_run_id
	FROM pay_adjustments x JOIN workers w ON w.id = x.worker_id LEFT JOIN projects p ON p.id = x.project_id`

type UnpaidWorker struct {
	WorkerID         int64   `db:"worker_id" json:"worker_id"`
	WorkerName       string  `db:"worker_name" json:"worker_name"`
	PayRateCents     int64   `db:"pay_rate_cents" json:"pay_rate_cents"`
	Minutes          int64   `db:"minutes" json:"minutes"`
	WagesCents       int64   `db:"wages_cents" json:"wages_cents"`
	AdjustmentsCents int64   `db:"adjustments_cents" json:"adjustments_cents"`
	TotalCents       int64   `db:"total_cents" json:"total_cents"`
	FirstDate        *string `db:"first_date" json:"first_date"`
	LastDate         *string `db:"last_date" json:"last_date"`
}

// GET /api/payroll/unpaid — what each worker is owed but not yet paid.
func (s *Server) unpaidSummary(w http.ResponseWriter, r *http.Request) error {
	list, err := s.unpaidRows(r.Context())
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) unpaidRows(ctx context.Context) ([]UnpaidWorker, error) {
	return collect[UnpaidWorker](ctx, s.db, `
		SELECT w.id AS worker_id, w.name AS worker_name, w.pay_rate_cents::bigint,
		  coalesce(e.minutes, 0)::bigint AS minutes, coalesce(e.wages, 0)::bigint AS wages_cents,
		  coalesce(a.total, 0)::bigint AS adjustments_cents, (coalesce(e.wages, 0) + coalesce(a.total, 0))::bigint AS total_cents,
		  least(e.first_date, a.first_date)::text AS first_date, greatest(e.last_date, a.last_date)::text AS last_date
		FROM workers w
		LEFT JOIN LATERAL (SELECT sum(t.minutes) AS minutes, sum(`+entryPaySQL+`) AS wages,
		                          min(t.work_date) AS first_date, max(t.work_date) AS last_date
		                   FROM time_entries t WHERE t.worker_id = w.id AND t.pay_run_id IS NULL) e ON true
		LEFT JOIN LATERAL (SELECT sum(x.amount_cents) AS total, min(x.adj_date) AS first_date, max(x.adj_date) AS last_date
		                   FROM pay_adjustments x WHERE x.worker_id = w.id AND x.pay_run_id IS NULL) a ON true
		WHERE coalesce(e.minutes, 0) > 0 OR coalesce(a.total, 0) <> 0
		ORDER BY lower(w.name)`)
}

func (s *Server) listPayRuns(w http.ResponseWriter, r *http.Request) error {
	wid, err := queryID(r, "worker_id")
	if err != nil {
		return err
	}
	list, err := collect[PayRun](r.Context(), s.db, payRunSelect+`
		WHERE ($1::bigint IS NULL OR r.worker_id = $1) ORDER BY r.period_end DESC, r.id DESC LIMIT 500`, wid)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) payRunDetail(ctx context.Context, q querier, id int64) (map[string]any, error) {
	run, err := one[PayRun](ctx, q, payRunSelect+` WHERE r.id = $1`, id)
	if err != nil {
		return nil, err
	}
	entries, err := collect[TimeEntry](ctx, q, timeEntrySelect+` WHERE t.pay_run_id = $1 ORDER BY t.work_date, t.start_minute NULLS LAST, t.id`, id)
	if err != nil {
		return nil, err
	}
	adjustments, err := collect[Adjustment](ctx, q, adjustmentSelect+` WHERE x.pay_run_id = $1 ORDER BY x.adj_date, x.id`, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"pay_run": run, "entries": entries, "adjustments": adjustments}, nil
}

func (s *Server) getPayRun(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := s.payRunDetail(r.Context(), s.db, id)
	if err != nil {
		return err
	}
	return ok(w, d)
}

type payRunInput struct {
	WorkerID    int64  `json:"worker_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Notes       string `json:"notes"`
}

// POST /api/pay-runs — collects the worker's unpaid time in the period plus unpaid adjustments dated on or before the end.
func (s *Server) createPayRun(w http.ResponseWriter, r *http.Request) error {
	var in payRunInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	start, err := requireDate("period start", in.PeriodStart)
	if err != nil {
		return err
	}
	end, err := requireDate("period end", in.PeriodEnd)
	if err != nil {
		return err
	}
	if start > end {
		return errBad("period start must be on or before period end")
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO pay_runs (worker_id, period_start, period_end, notes) VALUES ($1, $2::date, $3::date, $4) RETURNING id`,
		in.WorkerID, start, end, strings.TrimSpace(in.Notes)).Scan(&id); err != nil {
		return err
	}
	t1, err := tx.Exec(ctx, `
		UPDATE time_entries SET pay_run_id = $1, updated_at = now()
		WHERE worker_id = $2 AND pay_run_id IS NULL AND work_date BETWEEN $3::date AND $4::date`, id, in.WorkerID, start, end)
	if err != nil {
		return err
	}
	t2, err := tx.Exec(ctx, `
		UPDATE pay_adjustments SET pay_run_id = $1
		WHERE worker_id = $2 AND pay_run_id IS NULL AND adj_date <= $3::date`, id, in.WorkerID, end)
	if err != nil {
		return err
	}
	if t1.RowsAffected()+t2.RowsAffected() == 0 {
		return errBad("nothing unpaid for this worker in that period")
	}
	d, err := s.payRunDetail(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return created(w, d)
}

type payRunUpdateInput struct {
	Status   *string `json:"status"`
	PaidDate *string `json:"paid_date"`
	Notes    *string `json:"notes"`
}

func (s *Server) updatePayRun(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in payRunUpdateInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "draft", "paid"); err != nil {
			return err
		}
		paid, err := validDate("paid date", in.PaidDate)
		if err != nil {
			return err
		}
		tag, err := s.db.Exec(ctx, `
			UPDATE pay_runs SET status = $2,
			  paid_date = CASE WHEN $2 = 'paid' THEN coalesce($3::date, paid_date, CURRENT_DATE) END, updated_at = now()
			WHERE id = $1`, id, *in.Status, paid)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFound
		}
	}
	if in.Notes != nil {
		if _, err := s.db.Exec(ctx, `UPDATE pay_runs SET notes = $2, updated_at = now() WHERE id = $1`, id, *in.Notes); err != nil {
			return err
		}
	}
	d, err := s.payRunDetail(ctx, s.db, id)
	if err != nil {
		return err
	}
	return ok(w, d)
}

// DELETE /api/pay-runs/{id} — draft only; entries and adjustments become unpaid again.
func (s *Server) deletePayRun(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var status string
	if err := s.db.QueryRow(r.Context(), `SELECT status FROM pay_runs WHERE id = $1`, id).Scan(&status); err != nil {
		return err
	}
	if status != "draft" {
		return errConflict("paid pay runs can't be deleted; mark it as draft first")
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM pay_runs WHERE id = $1`, id); err != nil {
		return err
	}
	return noContent(w)
}

// GET /api/adjustments?worker_id=&unpaid=1
func (s *Server) listAdjustments(w http.ResponseWriter, r *http.Request) error {
	wid, err := queryID(r, "worker_id")
	if err != nil {
		return err
	}
	unpaid := r.URL.Query().Get("unpaid") == "1"
	list, err := collect[Adjustment](r.Context(), s.db, adjustmentSelect+`
		WHERE ($1::bigint IS NULL OR x.worker_id = $1) AND (NOT $2 OR x.pay_run_id IS NULL)
		ORDER BY x.adj_date DESC, x.id DESC LIMIT 1000`, wid, unpaid)
	if err != nil {
		return err
	}
	return ok(w, list)
}

type adjustmentInput struct {
	WorkerID    int64  `json:"worker_id"`
	ProjectID   *int64 `json:"project_id"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	AmountCents int64  `json:"amount_cents"`
	AdjDate     string `json:"adj_date"`
}

func (s *Server) createAdjustment(w http.ResponseWriter, r *http.Request) error {
	var in adjustmentInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := oneOf("kind", in.Kind, "bonus", "reimbursement", "deduction", "other"); err != nil {
		return err // profit shares are posted from the project
	}
	date, err := requireDate("date", in.AdjDate)
	if err != nil {
		return err
	}
	if in.AmountCents == 0 {
		return errBad("amount is required")
	}
	if in.Kind == "deduction" && in.AmountCents > 0 {
		in.AmountCents = -in.AmountCents
	}
	var id int64
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO pay_adjustments (worker_id, project_id, kind, description, amount_cents, adj_date)
		VALUES ($1, $2, $3, $4, $5, $6::date) RETURNING id`,
		in.WorkerID, in.ProjectID, in.Kind, strings.TrimSpace(in.Description), in.AmountCents, date).Scan(&id); err != nil {
		return err
	}
	a, err := one[Adjustment](r.Context(), s.db, adjustmentSelect+` WHERE x.id = $1`, id)
	if err != nil {
		return err
	}
	return created(w, a)
}

func (s *Server) deleteAdjustment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM pay_adjustments WHERE id = $1 AND pay_run_id IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errConflict("adjustment not found or already in a pay run")
	}
	return noContent(w)
}

// POST /api/projects/{id}/profit-share — posts (or refreshes) the profit-share pay item for a fixed-bid project.
func (s *Server) postProfitShare(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.projectReportRows(ctx, "p.id = $1", id)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return errNotFound
	}
	rep := rows[0]
	if rep.BillingType != "fixed" || rep.ProfitShareWorkerID == nil || rep.ProfitShareBps == 0 {
		return errBad("this project has no profit share configured")
	}
	date := todayString()
	if rep.CompletedOn != nil {
		date = *rep.CompletedOn
	}
	desc := "Profit share: " + rep.ProjectName
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existingID int64
	var runID *int64
	err = tx.QueryRow(ctx, `
		SELECT id, pay_run_id FROM pay_adjustments WHERE project_id = $1 AND worker_id = $2 AND kind = 'profit_share' FOR UPDATE`,
		id, *rep.ProfitShareWorkerID).Scan(&existingID, &runID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	switch {
	case err == nil && runID != nil:
		return errConflict("the profit share for this project is already in a pay run")
	case err == nil:
		_, err = tx.Exec(ctx, `UPDATE pay_adjustments SET amount_cents = $2, adj_date = $3::date, description = $4 WHERE id = $1`,
			existingID, rep.ProfitShareCents, date, desc)
	default:
		_, err = tx.Exec(ctx, `
			INSERT INTO pay_adjustments (worker_id, project_id, kind, description, amount_cents, adj_date)
			VALUES ($1, $2, 'profit_share', $3, $4, $5::date)`, *rep.ProfitShareWorkerID, id, desc, rep.ProfitShareCents, date)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	rows, err = s.projectReportRows(ctx, "p.id = $1", id)
	if err != nil {
		return err
	}
	return ok(w, rows[0])
}
