package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/claym/crm/internal/calc"
	"github.com/jackc/pgx/v5"
)

// Invoices are per client; each line bills one project (hours or a fixed bid) or is a manual extra.

type Invoice struct {
	ID          int64   `db:"id" json:"id"`
	Number      int64   `db:"number" json:"number"`
	ClientID    *int64  `db:"client_id" json:"client_id"`
	ClientName  *string `db:"client_name" json:"client_name"`
	ClientEmail *string `db:"client_email" json:"client_email"`
	PeriodStart *string `db:"period_start" json:"period_start"`
	PeriodEnd   *string `db:"period_end" json:"period_end"`
	IssueDate   string  `db:"issue_date" json:"issue_date"`
	DueDate     *string `db:"due_date" json:"due_date"`
	Status      string  `db:"status" json:"status"`
	PaidDate    *string `db:"paid_date" json:"paid_date"`
	SentAt      *string `db:"sent_at" json:"sent_at"`
	SentTo      string  `db:"sent_to" json:"sent_to"`
	Notes       string  `db:"notes" json:"notes"`
	TotalCents  int64   `db:"total_cents" json:"total_cents"`
	Minutes     int64   `db:"minutes" json:"minutes"`
	Projects    string  `db:"projects" json:"projects"` // comma-separated project names on the invoice
	// Per-worker hours on this invoice (the Overview sheet's "Justin Hrs / Abbey Hrs / Jake Hrs").
	Workers []WorkerMinutes `db:"-" json:"workers"`
}

type WorkerMinutes struct {
	WorkerID   int64  `db:"worker_id" json:"worker_id"`
	WorkerName string `db:"worker_name" json:"worker_name"`
	Minutes    int64  `db:"minutes" json:"minutes"`
	PayCents   int64  `db:"pay_cents" json:"pay_cents"`
}

type InvoiceLine struct {
	ID             int64   `db:"id" json:"id"`
	ProjectID      *int64  `db:"project_id" json:"project_id"`
	ProjectName    *string `db:"project_name" json:"project_name"`
	Kind           string  `db:"kind" json:"kind"`
	Description    string  `db:"description" json:"description"`
	Minutes        *int64  `db:"minutes" json:"minutes"`
	Quantity       float64 `db:"quantity" json:"quantity"`
	UnitPriceCents int64   `db:"unit_price_cents" json:"unit_price_cents"`
	AmountCents    int64   `db:"amount_cents" json:"amount_cents"`
}

const invoiceSelect = `
	SELECT i.id, i.number::bigint, i.client_id, c.name AS client_name, nullif(c.email, '') AS client_email,
	  i.period_start::text, i.period_end::text, i.issue_date::text, i.due_date::text, i.status, i.paid_date::text,
	  to_char(i.sent_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS sent_at, i.sent_to, i.notes,
	  (SELECT coalesce(sum(l.amount_cents), 0)::bigint FROM invoice_lines l WHERE l.invoice_id = i.id) AS total_cents,
	  (SELECT coalesce(sum(l.minutes), 0)::bigint FROM invoice_lines l WHERE l.invoice_id = i.id) AS minutes,
	  coalesce((SELECT string_agg(DISTINCT p.name, ', ') FROM invoice_lines l JOIN projects p ON p.id = l.project_id
	     WHERE l.invoice_id = i.id), '') AS projects
	FROM invoices i LEFT JOIN clients c ON c.id = i.client_id`

// attachInvoiceWorkers fills Invoice.Workers from the time entries linked to each invoice.
func (s *Server) attachInvoiceWorkers(ctx context.Context, q querier, invs []Invoice) error {
	if len(invs) == 0 {
		return nil
	}
	ids := make([]int64, len(invs))
	idx := map[int64]int{}
	for i, inv := range invs {
		ids[i] = inv.ID
		idx[inv.ID] = i
		invs[i].Workers = []WorkerMinutes{}
	}
	type row struct {
		InvoiceID int64 `db:"invoice_id"`
		WorkerMinutes
	}
	rows, err := collect[row](ctx, q, `
		SELECT t.invoice_id, t.worker_id, w.name AS worker_name, sum(t.minutes)::bigint AS minutes,
		  sum(`+entryPaySQL+`)::bigint AS pay_cents
		FROM time_entries t JOIN workers w ON w.id = t.worker_id
		WHERE t.invoice_id = ANY($1)
		GROUP BY t.invoice_id, t.worker_id, w.name ORDER BY w.name`, ids)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := idx[r.InvoiceID]
		invs[i].Workers = append(invs[i].Workers, r.WorkerMinutes)
	}
	return nil
}

// GET /api/invoices?status=&client_id=
func (s *Server) listInvoices(w http.ResponseWriter, r *http.Request) error {
	where, args := []string{}, []any{}
	if st := r.URL.Query().Get("status"); st != "" {
		args = append(args, st)
		where = append(where, "i.status = $"+itoa(len(args)))
	}
	if id, err := queryID(r, "client_id"); err != nil {
		return err
	} else if id != nil {
		args = append(args, *id)
		where = append(where, "i.client_id = $"+itoa(len(args)))
	}
	sql := invoiceSelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	list, err := collect[Invoice](r.Context(), s.db, sql+` ORDER BY i.number DESC`, args...)
	if err != nil {
		return err
	}
	if err := s.attachInvoiceWorkers(r.Context(), s.db, list); err != nil {
		return err
	}
	return ok(w, list)
}

type InvoiceDetail struct {
	Invoice Invoice       `json:"invoice"`
	Lines   []InvoiceLine `json:"lines"`
	Entries []TimeEntry   `json:"entries"`
}

func (s *Server) invoiceDetail(ctx context.Context, q querier, id int64) (*InvoiceDetail, error) {
	inv, err := one[Invoice](ctx, q, invoiceSelect+` WHERE i.id = $1`, id)
	if err != nil {
		return nil, err
	}
	list := []Invoice{inv}
	if err := s.attachInvoiceWorkers(ctx, q, list); err != nil {
		return nil, err
	}
	lines, err := collect[InvoiceLine](ctx, q, `
		SELECT l.id, l.project_id, p.name AS project_name, l.kind, l.description, l.minutes::bigint, l.quantity,
		  l.unit_price_cents, l.amount_cents
		FROM invoice_lines l LEFT JOIN projects p ON p.id = l.project_id
		WHERE l.invoice_id = $1 ORDER BY l.position, l.id`, id)
	if err != nil {
		return nil, err
	}
	entries, err := collect[TimeEntry](ctx, q, timeEntrySelect+` WHERE t.invoice_id = $1 ORDER BY t.work_date, w.name, t.id`, id)
	if err != nil {
		return nil, err
	}
	return &InvoiceDetail{Invoice: list[0], Lines: lines, Entries: entries}, nil
}

func (s *Server) getInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := s.invoiceDetail(r.Context(), s.db, id)
	if err != nil {
		return err
	}
	return ok(w, d)
}

type invoiceCreateInput struct {
	Number           *int64  `json:"number"`
	ClientID         int64   `json:"client_id"`
	PeriodStart      *string `json:"period_start"`
	PeriodEnd        *string `json:"period_end"`
	IssueDate        *string `json:"issue_date"`
	DueDate          *string `json:"due_date"`
	Notes            string  `json:"notes"`
	HourlyProjectIDs []int64 `json:"hourly_project_ids"` // bill unbilled hours of these projects within the period
	FixedProjects    []struct {
		ProjectID   int64  `json:"project_id"`
		AmountCents *int64 `json:"amount_cents"` // default: remaining bid
		Description string `json:"description"`
	} `json:"fixed_projects"`
}

// POST /api/invoices — builds a client invoice from unbilled hours and/or fixed-bid projects.
func (s *Server) createInvoice(w http.ResponseWriter, r *http.Request) error {
	var in invoiceCreateInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	var err error
	if in.ClientID <= 0 {
		return errBad("choose a client")
	}
	if in.PeriodStart, err = validDate("period start", in.PeriodStart); err != nil {
		return err
	}
	if in.PeriodEnd, err = validDate("period end", in.PeriodEnd); err != nil {
		return err
	}
	if in.IssueDate, err = validDate("issue date", in.IssueDate); err != nil {
		return err
	}
	if in.DueDate, err = validDate("due date", in.DueDate); err != nil {
		return err
	}
	if in.PeriodStart != nil && in.PeriodEnd != nil && *in.PeriodStart > *in.PeriodEnd {
		return errBad("period start must be on or before period end")
	}
	if len(in.HourlyProjectIDs) == 0 && len(in.FixedProjects) == 0 {
		return errBad("choose at least one project to invoice")
	}

	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE invoices IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	number := int64(0)
	if in.Number != nil {
		if *in.Number <= 0 {
			return errBad("invoice number must be positive")
		}
		number = *in.Number
	} else if err := tx.QueryRow(ctx, `SELECT coalesce(max(number), 0) + 1 FROM invoices`).Scan(&number); err != nil {
		return err
	}
	var invID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices (number, client_id, period_start, period_end, issue_date, due_date, notes)
		VALUES ($1, $2, $3::date, $4::date, coalesce($5::date, CURRENT_DATE),
		  coalesce($6::date, coalesce($5::date, CURRENT_DATE) + (SELECT default_due_days FROM company_settings WHERE id = 1)), $7)
		RETURNING id`,
		number, in.ClientID, in.PeriodStart, in.PeriodEnd, in.IssueDate, in.DueDate, in.Notes).Scan(&invID); err != nil {
		return err
	}

	// loadProject checks the project exists, belongs to the client and has the expected billing type.
	loadProject := func(id int64, want string) (name string, rate, bid int64, err error) {
		var billing string
		var clientID *int64
		err = tx.QueryRow(ctx, `SELECT name, billing_type, bill_rate_cents::bigint, bid_amount_cents, client_id FROM projects WHERE id = $1`, id).
			Scan(&name, &billing, &rate, &bid, &clientID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, 0, errBad("project %d not found", id)
		}
		if err != nil {
			return
		}
		if clientID == nil || *clientID != in.ClientID {
			return "", 0, 0, errBad("%s doesn't belong to this client", name)
		}
		if billing != want {
			return "", 0, 0, errBad("%s is billed %s, not %s", name, billing, want)
		}
		return
	}

	position := 0
	for _, pid := range in.HourlyProjectIDs {
		name, rate, _, err := loadProject(pid, "hourly")
		if err != nil {
			return err
		}
		var minutes int64
		if err := tx.QueryRow(ctx, `
			WITH billed AS (
			  UPDATE time_entries SET invoice_id = $1, updated_at = now()
			  WHERE project_id = $2 AND invoice_id IS NULL AND billable
			    AND ($3::date IS NULL OR work_date >= $3::date) AND ($4::date IS NULL OR work_date <= $4::date)
			  RETURNING minutes)
			SELECT coalesce(sum(minutes), 0)::bigint FROM billed`,
			invID, pid, in.PeriodStart, in.PeriodEnd).Scan(&minutes); err != nil {
			return err
		}
		if minutes == 0 {
			continue
		}
		hours := math.Round(calc.Hours(int(minutes))*100) / 100
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoice_lines (invoice_id, project_id, kind, description, minutes, quantity, unit_price_cents, amount_cents, position)
			VALUES ($1, $2, 'hours', $3, $4, $5, $6, $7, $8)`,
			invID, pid, name+" — labor", minutes, hours, rate, calc.Amount(int(minutes), rate), position); err != nil {
			return err
		}
		position++
	}

	for _, fp := range in.FixedProjects {
		name, _, bid, err := loadProject(fp.ProjectID, "fixed")
		if err != nil {
			return err
		}
		var invoiced int64
		if err := tx.QueryRow(ctx, `
			SELECT coalesce(sum(l.amount_cents), 0)::bigint FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id
			WHERE l.project_id = $1 AND i.status <> 'void'`, fp.ProjectID).Scan(&invoiced); err != nil {
			return err
		}
		amount := bid - invoiced
		if fp.AmountCents != nil {
			amount = *fp.AmountCents
		}
		if amount <= 0 {
			return errBad("%s has already been fully invoiced", name)
		}
		desc := strings.TrimSpace(fp.Description)
		if desc == "" {
			desc = name
			if amount != bid {
				desc = fmt.Sprintf("%s — progress billing (bid %s, previously billed %s)", name, formatCents(bid), formatCents(invoiced))
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoice_lines (invoice_id, project_id, kind, description, quantity, unit_price_cents, amount_cents, position)
			VALUES ($1, $2, 'fixed', $3, 1, $4, $4, $5)`,
			invID, fp.ProjectID, desc, amount, position); err != nil {
			return err
		}
		position++
	}
	if position == 0 {
		return errBad("nothing to invoice: the selected projects have no unbilled hours in that period")
	}
	d, err := s.invoiceDetail(ctx, tx, invID)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return created(w, d)
}

func formatCents(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	whole := fmt.Sprintf("%d", c/100)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return fmt.Sprintf("%s$%s.%02d", sign, whole, c%100)
}

type invoiceUpdateInput struct {
	Status    *string `json:"status"`
	Number    *int64  `json:"number"`
	ClientID  *int64  `json:"client_id"`
	IssueDate *string `json:"issue_date"`
	DueDate   *string `json:"due_date"`
	PaidDate  *string `json:"paid_date"`
	Notes     *string `json:"notes"`
}

var invoiceTransitions = map[string][]string{
	"draft": {"sent", "paid", "void"},
	"sent":  {"draft", "paid", "void"},
	"paid":  {"sent"},
	"void":  {},
}

// PATCH /api/invoices/{id}
func (s *Server) updateInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in invoiceUpdateInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
		return err
	}
	if status == "void" {
		return errConflict("void invoices can't be changed")
	}
	if in.Status != nil && *in.Status != status {
		allowed := false
		for _, to := range invoiceTransitions[status] {
			allowed = allowed || to == *in.Status
		}
		if !allowed {
			return errBad("can't change an invoice from %s to %s", status, *in.Status)
		}
		paidDate, err := validDate("paid date", in.PaidDate)
		if err != nil {
			return err
		}
		switch *in.Status {
		case "paid":
			if _, err := tx.Exec(ctx, `UPDATE invoices SET status='paid', paid_date=coalesce($2::date, CURRENT_DATE), updated_at=now() WHERE id=$1`, id, paidDate); err != nil {
				return err
			}
		case "void":
			// Release the hours so they can be billed again.
			if _, err := tx.Exec(ctx, `UPDATE time_entries SET invoice_id = NULL, updated_at = now() WHERE invoice_id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE invoices SET status='void', paid_date=NULL, updated_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
		default:
			if _, err := tx.Exec(ctx, `UPDATE invoices SET status=$2, paid_date=NULL, updated_at=now() WHERE id=$1`, id, *in.Status); err != nil {
				return err
			}
		}
	} else if in.PaidDate != nil && status == "paid" {
		paidDate, err := validDate("paid date", in.PaidDate)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET paid_date=coalesce($2::date, paid_date), updated_at=now() WHERE id=$1`, id, paidDate); err != nil {
			return err
		}
	}
	if in.Number != nil {
		if *in.Number <= 0 {
			return errBad("invoice number must be positive")
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET number=$2, updated_at=now() WHERE id=$1`, id, *in.Number); err != nil {
			return err
		}
	}
	if in.ClientID != nil {
		if _, err := tx.Exec(ctx, `UPDATE invoices SET client_id=$2, updated_at=now() WHERE id=$1`, id, *in.ClientID); err != nil {
			return err
		}
	}
	if in.IssueDate != nil {
		d, err := requireDate("issue date", *in.IssueDate)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET issue_date=$2::date, updated_at=now() WHERE id=$1`, id, d); err != nil {
			return err
		}
	}
	if in.DueDate != nil {
		d, err := validDate("due date", in.DueDate)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET due_date=$2::date, updated_at=now() WHERE id=$1`, id, d); err != nil {
			return err
		}
	}
	if in.Notes != nil {
		if _, err := tx.Exec(ctx, `UPDATE invoices SET notes=$2, updated_at=now() WHERE id=$1`, id, *in.Notes); err != nil {
			return err
		}
	}
	d, err := s.invoiceDetail(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return ok(w, d)
}

// DELETE /api/invoices/{id} — drafts and voided invoices only; linked hours are released.
func (s *Server) deleteInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var status string
	if err := s.db.QueryRow(r.Context(), `SELECT status FROM invoices WHERE id = $1`, id).Scan(&status); err != nil {
		return err
	}
	if status != "draft" && status != "void" {
		return errConflict("only draft or void invoices can be deleted; void it first")
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM invoices WHERE id = $1`, id); err != nil {
		return err
	}
	return noContent(w)
}

type invoiceLineInput struct {
	ProjectID      *int64  `json:"project_id"`
	Description    string  `json:"description"`
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int64   `json:"unit_price_cents"`
}

func (s *Server) requireDraft(ctx context.Context, q pgx.Tx, id int64) error {
	var status string
	if err := q.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
		return err
	}
	if status != "draft" {
		return errConflict("only draft invoices can be edited; set it back to draft first")
	}
	return nil
}

// POST /api/invoices/{id}/lines — add a manual line (materials, extras, adjustments).
func (s *Server) addInvoiceLine(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in invoiceLineInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	desc, err := requireText("description", in.Description, 500)
	if err != nil {
		return err
	}
	if in.Quantity == 0 {
		in.Quantity = 1
	}
	amount := int64(math.Round(in.Quantity * float64(in.UnitPriceCents)))
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := s.requireDraft(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_lines (invoice_id, project_id, kind, description, quantity, unit_price_cents, amount_cents, position)
		VALUES ($1, $2, 'other', $3, $4, $5, $6, (SELECT coalesce(max(position) + 1, 0) FROM invoice_lines WHERE invoice_id = $1))`,
		id, in.ProjectID, desc, in.Quantity, in.UnitPriceCents, amount); err != nil {
		return err
	}
	d, err := s.invoiceDetail(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return created(w, d)
}

// DELETE /api/invoices/{id}/lines/{lineID} — removing an hours line releases its time entries.
func (s *Server) deleteInvoiceLine(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	lineID, err := pathID(r, "lineID")
	if err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := s.requireDraft(ctx, tx, id); err != nil {
		return err
	}
	var kind string
	var projectID *int64
	if err := tx.QueryRow(ctx, `DELETE FROM invoice_lines WHERE id = $1 AND invoice_id = $2 RETURNING kind, project_id`, lineID, id).Scan(&kind, &projectID); err != nil {
		return err
	}
	if kind == "hours" && projectID != nil {
		if _, err := tx.Exec(ctx, `UPDATE time_entries SET invoice_id = NULL, updated_at = now() WHERE invoice_id = $1 AND project_id = $2`, id, *projectID); err != nil {
			return err
		}
	}
	d, err := s.invoiceDetail(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return ok(w, d)
}

type UnbilledProject struct {
	ProjectID      int64   `db:"project_id" json:"project_id"`
	ProjectName    string  `db:"project_name" json:"project_name"`
	ClientID       *int64  `db:"client_id" json:"client_id"`
	BillingType    string  `db:"billing_type" json:"billing_type"`
	Status         string  `db:"status" json:"status"`
	BillRateCents  int64   `db:"bill_rate_cents" json:"bill_rate_cents"`
	Minutes        int64   `db:"minutes" json:"minutes"`
	FirstDate      *string `db:"first_date" json:"first_date"`
	LastDate       *string `db:"last_date" json:"last_date"`
	AmountCents    int64   `db:"amount_cents" json:"amount_cents"`
	BidAmountCents int64   `db:"bid_amount_cents" json:"bid_amount_cents"`
	InvoicedCents  int64   `db:"invoiced_cents" json:"invoiced_cents"`
}

// GET /api/billing/unbilled?client_id=&from=&to= — what could go on a client's next invoice.
func (s *Server) unbilledSummary(w http.ResponseWriter, r *http.Request) error {
	cid, err := queryID(r, "client_id")
	if err != nil {
		return err
	}
	from, err := queryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := queryDate(r, "to")
	if err != nil {
		return err
	}
	list, err := collect[UnbilledProject](r.Context(), s.db, `
		SELECT p.id AS project_id, p.name AS project_name, p.client_id, p.billing_type, p.status,
		  p.bill_rate_cents::bigint, p.bid_amount_cents,
		  coalesce(u.minutes, 0)::bigint AS minutes, u.first_date::text, u.last_date::text,
		  coalesce(u.amount, 0)::bigint AS amount_cents,
		  coalesce(inv.total, 0)::bigint AS invoiced_cents
		FROM projects p
		LEFT JOIN LATERAL (
		  SELECT sum(t.minutes) AS minutes, min(t.work_date) AS first_date, max(t.work_date) AS last_date,
		         round(sum(t.minutes) * p.bill_rate_cents / 60.0) AS amount
		  FROM time_entries t
		  WHERE t.project_id = p.id AND t.invoice_id IS NULL AND t.billable
		    AND ($2::date IS NULL OR t.work_date >= $2::date) AND ($3::date IS NULL OR t.work_date <= $3::date)
		) u ON true
		LEFT JOIN LATERAL (
		  SELECT sum(l.amount_cents) AS total FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id
		  WHERE l.project_id = p.id AND i.status <> 'void'
		) inv ON true
		WHERE p.client_id IS NOT NULL AND ($1::bigint IS NULL OR p.client_id = $1)
		  AND ((p.billing_type = 'hourly' AND coalesce(u.minutes, 0) > 0)
		    OR (p.billing_type = 'fixed' AND p.status <> 'cancelled' AND p.bid_amount_cents > coalesce(inv.total, 0)))
		ORDER BY p.billing_type, lower(p.name)`, cid, from, to)
	if err != nil {
		return err
	}
	return ok(w, list)
}
