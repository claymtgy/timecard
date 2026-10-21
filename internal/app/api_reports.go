package app

import (
	"context"
	"net/http"
	"time"

	"github.com/claym/crm/internal/calc"
)

// ProjectReport reproduces the "Bid Jobs" sheet for any project: hours by worker and task,
// revenue, labor cost, profit, profit share and what the share recipient earns.
type ProjectReport struct {
	ProjectID             int64   `db:"project_id" json:"project_id"`
	ProjectName           string  `db:"project_name" json:"project_name"`
	ClientID              *int64  `db:"client_id" json:"client_id"`
	ClientName            *string `db:"client_name" json:"client_name"`
	BillingType           string  `db:"billing_type" json:"billing_type"`
	Status                string  `db:"status" json:"status"`
	CompletedOn           *string `db:"completed_on" json:"completed_on"`
	BidAmountCents        int64   `db:"bid_amount_cents" json:"bid_amount_cents"`
	BillRateCents         int64   `db:"bill_rate_cents" json:"bill_rate_cents"`
	LaborCostRateCents    int64   `db:"labor_cost_rate_cents" json:"labor_cost_rate_cents"`
	ProfitShareBps        int64   `db:"profit_share_bps" json:"profit_share_bps"`
	ProfitShareWorkerID   *int64  `db:"profit_share_worker_id" json:"profit_share_worker_id"`
	ProfitShareWorkerName *string `db:"profit_share_worker_name" json:"profit_share_worker_name"`
	InvoicedCents         int64   `db:"invoiced_cents" json:"invoiced_cents"`
	PaidCents             int64   `db:"paid_cents" json:"paid_cents"`
	PostedShareCents      *int64  `db:"posted_share_cents" json:"posted_share_cents"`
	PostedSharePaid       bool    `db:"posted_share_paid" json:"posted_share_paid"`

	Minutes          int64           `db:"-" json:"minutes"`
	BillableMinutes  int64           `db:"-" json:"billable_minutes"`
	WagesCents       int64           `db:"-" json:"wages_cents"`
	Workers          []WorkerMinutes `db:"-" json:"workers"`
	Phases           []PhaseMinutes  `db:"-" json:"phases"`
	RevenueCents     int64           `db:"-" json:"revenue_cents"`
	LaborCostCents   int64           `db:"-" json:"labor_cost_cents"`
	ProfitCents      int64           `db:"-" json:"profit_cents"`
	ProfitShareCents int64           `db:"-" json:"profit_share_cents"`
	// The share recipient's hourly pay on this project plus the share ("Jake Pay").
	ShareWorkerPayCents int64 `db:"-" json:"share_worker_pay_cents"`
}

type PhaseMinutes struct {
	TaskID  *int64          `json:"task_id"`
	Title   string          `json:"title"`
	Minutes int64           `json:"minutes"`
	Workers []WorkerMinutes `json:"workers"`
}

func (s *Server) projectReportRows(ctx context.Context, where string, args ...any) ([]ProjectReport, error) {
	reports, err := collect[ProjectReport](ctx, s.db, `
		SELECT p.id AS project_id, p.name AS project_name, p.client_id, c.name AS client_name, p.billing_type, p.status,
		  p.completed_on::text, p.bid_amount_cents, p.bill_rate_cents::bigint, p.labor_cost_rate_cents::bigint,
		  p.profit_share_bps::bigint, p.profit_share_worker_id, sw.name AS profit_share_worker_name,
		  (SELECT coalesce(sum(l.amount_cents), 0)::bigint FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id
		     WHERE l.project_id = p.id AND i.status <> 'void') AS invoiced_cents,
		  (SELECT coalesce(sum(l.amount_cents), 0)::bigint FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id
		     WHERE l.project_id = p.id AND i.status = 'paid') AS paid_cents,
		  ps.amount_cents AS posted_share_cents, coalesce(pr.status = 'paid', false) AS posted_share_paid
		FROM projects p
		LEFT JOIN clients c ON c.id = p.client_id
		LEFT JOIN workers sw ON sw.id = p.profit_share_worker_id
		LEFT JOIN pay_adjustments ps ON ps.project_id = p.id AND ps.kind = 'profit_share' AND ps.worker_id = p.profit_share_worker_id
		LEFT JOIN pay_runs pr ON pr.id = ps.pay_run_id
		WHERE `+where+`
		ORDER BY lower(coalesce(c.name, '')), lower(p.name)`, args...)
	if err != nil || len(reports) == 0 {
		return reports, err
	}
	ids := make([]int64, len(reports))
	idx := map[int64]int{}
	for i, rep := range reports {
		ids[i] = rep.ProjectID
		idx[rep.ProjectID] = i
		reports[i].Workers = []WorkerMinutes{}
		reports[i].Phases = []PhaseMinutes{}
	}

	type workerRow struct {
		ProjectID int64 `db:"project_id"`
		Billable  int64 `db:"billable_minutes"`
		WorkerMinutes
	}
	workers, err := collect[workerRow](ctx, s.db, `
		SELECT t.project_id, t.worker_id, w.name AS worker_name, sum(t.minutes)::bigint AS minutes,
		  sum(`+entryPaySQL+`)::bigint AS pay_cents,
		  coalesce(sum(t.minutes) FILTER (WHERE t.billable), 0)::bigint AS billable_minutes
		FROM time_entries t JOIN workers w ON w.id = t.worker_id
		WHERE t.project_id = ANY($1) GROUP BY t.project_id, t.worker_id, w.name ORDER BY w.name`, ids)
	if err != nil {
		return nil, err
	}
	for _, wr := range workers {
		rep := &reports[idx[wr.ProjectID]]
		rep.Workers = append(rep.Workers, wr.WorkerMinutes)
		rep.Minutes += wr.Minutes
		rep.BillableMinutes += wr.Billable
		rep.WagesCents += wr.PayCents
	}

	type phaseRow struct {
		ProjectID  int64   `db:"project_id"`
		TaskID     *int64  `db:"task_id"`
		Title      *string `db:"title"`
		WorkerID   int64   `db:"worker_id"`
		WorkerName string  `db:"worker_name"`
		Minutes    int64   `db:"minutes"`
		PayCents   int64   `db:"pay_cents"`
	}
	phases, err := collect[phaseRow](ctx, s.db, `
		SELECT t.project_id, t.task_id, k.title, t.worker_id, w.name AS worker_name, sum(t.minutes)::bigint AS minutes,
		  sum(`+entryPaySQL+`)::bigint AS pay_cents
		FROM time_entries t JOIN workers w ON w.id = t.worker_id LEFT JOIN tasks k ON k.id = t.task_id
		WHERE t.project_id = ANY($1)
		GROUP BY t.project_id, t.task_id, k.title, k.position, t.worker_id, w.name
		ORDER BY t.project_id, k.position NULLS LAST, t.task_id NULLS LAST, w.name`, ids)
	if err != nil {
		return nil, err
	}
	for _, pr := range phases {
		rep := &reports[idx[pr.ProjectID]]
		n := len(rep.Phases)
		if n == 0 || !sameTask(rep.Phases[n-1].TaskID, pr.TaskID) {
			title := "(no task)"
			if pr.Title != nil {
				title = *pr.Title
			}
			rep.Phases = append(rep.Phases, PhaseMinutes{TaskID: pr.TaskID, Title: title, Workers: []WorkerMinutes{}})
			n++
		}
		ph := &rep.Phases[n-1]
		ph.Minutes += pr.Minutes
		ph.Workers = append(ph.Workers, WorkerMinutes{WorkerID: pr.WorkerID, WorkerName: pr.WorkerName, Minutes: pr.Minutes, PayCents: pr.PayCents})
	}

	for i := range reports {
		rep := &reports[i]
		if rep.BillingType == "fixed" {
			rep.RevenueCents = rep.BidAmountCents
			laborRate := rep.LaborCostRateCents
			p := calc.BidJobProfit(rep.BidAmountCents, int(rep.Minutes), laborRate, int(rep.ProfitShareBps))
			rep.LaborCostCents, rep.ProfitCents, rep.ProfitShareCents = p.LaborCostCents, p.ProfitCents, p.ProfitShareCents
			if laborRate == 0 { // no notional rate: use actual wages
				rep.LaborCostCents = rep.WagesCents
				rep.ProfitCents = rep.BidAmountCents - rep.WagesCents
				rep.ProfitShareCents = calc.ShareOf(rep.ProfitCents, int(rep.ProfitShareBps))
			}
		} else {
			rep.RevenueCents = calc.Amount(int(rep.BillableMinutes), rep.BillRateCents)
			rep.LaborCostCents = rep.WagesCents
			rep.ProfitCents = rep.RevenueCents - rep.WagesCents
		}
		if rep.ProfitShareWorkerID != nil {
			for _, wm := range rep.Workers {
				if wm.WorkerID == *rep.ProfitShareWorkerID {
					rep.ShareWorkerPayCents = wm.PayCents
				}
			}
			rep.ShareWorkerPayCents += rep.ProfitShareCents
		}
	}
	return reports, nil
}

func sameTask(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// GET /api/reports/projects?client_id=&billing_type=&status=
func (s *Server) projectProfitReport(w http.ResponseWriter, r *http.Request) error {
	cid, err := queryID(r, "client_id")
	if err != nil {
		return err
	}
	bt := r.URL.Query().Get("billing_type")
	st := r.URL.Query().Get("status")
	rows, err := s.projectReportRows(r.Context(), `($1::bigint IS NULL OR p.client_id = $1)
		AND ($2 = '' OR p.billing_type = $2) AND ($3 = '' OR p.status = $3)`, cid, bt, st)
	if err != nil {
		return err
	}
	return ok(w, rows)
}

// GET /api/reports/dashboard
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u := currentUser(r)
	weekStart := calc.WeekStart(time.Now().In(s.loc))
	from, to := weekStart.Format(calc.DateLayout), weekStart.AddDate(0, 0, 6).Format(calc.DateLayout)
	resp := map[string]any{"week_start": from, "week_end": to}

	type weekRow struct {
		WorkerID   int64  `db:"worker_id" json:"worker_id"`
		WorkerName string `db:"worker_name" json:"worker_name"`
		Minutes    int64  `db:"minutes" json:"minutes"`
	}
	var workerFilter *int64
	if !u.isManager() {
		if u.WorkerID == nil {
			resp["unlinked"] = true
			return ok(w, resp)
		}
		workerFilter = u.WorkerID
	}
	week, err := collect[weekRow](ctx, s.db, `
		SELECT t.worker_id, w.name AS worker_name, sum(t.minutes)::bigint AS minutes
		FROM time_entries t JOIN workers w ON w.id = t.worker_id
		WHERE t.work_date BETWEEN $1::date AND $2::date AND ($3::bigint IS NULL OR t.worker_id = $3)
		GROUP BY t.worker_id, w.name ORDER BY w.name`, from, to, workerFilter)
	if err != nil {
		return err
	}
	resp["week"] = week

	taskWhere := "k.status <> 'done' AND k.assignee_worker_id = $1"
	taskArg := any(workerFilter)
	if u.isManager() {
		taskWhere, taskArg = "k.status <> 'done' AND k.due_date IS NOT NULL AND k.due_date <= CURRENT_DATE + 7 AND $1::bigint IS NULL", nil
	}
	tasks, err := collect[Task](ctx, s.db, taskSelect+` WHERE `+taskWhere+` ORDER BY k.due_date NULLS LAST, k.id LIMIT 20`, taskArg)
	if err != nil {
		return err
	}
	resp["tasks"] = tasks

	if u.isManager() {
		var unbilledMinutes, unbilledCents, outstandingCents, openProjects int64
		if err := s.db.QueryRow(ctx, `
			SELECT coalesce(sum(t.minutes), 0)::bigint, coalesce(sum(round(t.minutes * p.bill_rate_cents / 60.0)), 0)::bigint
			FROM time_entries t JOIN projects p ON p.id = t.project_id
			WHERE t.invoice_id IS NULL AND t.billable AND p.billing_type = 'hourly'`).Scan(&unbilledMinutes, &unbilledCents); err != nil {
			return err
		}
		if err := s.db.QueryRow(ctx, `
			SELECT coalesce(sum(l.amount_cents), 0)::bigint FROM invoices i JOIN invoice_lines l ON l.invoice_id = i.id
			WHERE i.status = 'sent'`).Scan(&outstandingCents); err != nil {
			return err
		}
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM projects WHERE status IN ('planned', 'active')`).Scan(&openProjects); err != nil {
			return err
		}
		unpaid, err := s.unpaidRows(ctx)
		if err != nil {
			return err
		}
		var unpaidCents int64
		for _, u := range unpaid {
			unpaidCents += u.TotalCents
		}
		clocked, err := collect[ClockSession](ctx, s.db, clockSelect+` ORDER BY s.started_at`)
		if err != nil {
			return err
		}
		resp["unbilled_minutes"] = unbilledMinutes
		resp["unbilled_cents"] = unbilledCents
		resp["outstanding_cents"] = outstandingCents
		resp["open_projects"] = openProjects
		resp["unpaid_payroll_cents"] = unpaidCents
		resp["unpaid"] = unpaid
		resp["clocked_in"] = clocked
	}
	return ok(w, resp)
}
