package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ============================== projects ==============================
// A project is the billable unit: hourly (bill rate x hours) or a fixed bid.

type Project struct {
	ID                   int64   `db:"id" json:"id"`
	ClientID             *int64  `db:"client_id" json:"client_id"`
	ClientName           *string `db:"client_name" json:"client_name"`
	Name                 string  `db:"name" json:"name"`
	Description          string  `db:"description" json:"description"`
	BillingType          string  `db:"billing_type" json:"billing_type"`
	BillRateCents        *int64  `db:"bill_rate_cents" json:"bill_rate_cents,omitempty"`
	BidAmountCents       *int64  `db:"bid_amount_cents" json:"bid_amount_cents,omitempty"`
	LaborCostRateCents   *int64  `db:"labor_cost_rate_cents" json:"labor_cost_rate_cents,omitempty"`
	PayRateOverrideCents *int64  `db:"pay_rate_override_cents" json:"pay_rate_override_cents,omitempty"`
	ProfitShareBps       *int64  `db:"profit_share_bps" json:"profit_share_bps,omitempty"`
	ProfitShareWorkerID  *int64  `db:"profit_share_worker_id" json:"profit_share_worker_id,omitempty"`
	Status               string  `db:"status" json:"status"`
	StartDate            *string `db:"start_date" json:"start_date"`
	CompletedOn          *string `db:"completed_on" json:"completed_on"`
	// Aggregates
	Minutes         int64  `db:"minutes" json:"minutes"`
	UnbilledMinutes int64  `db:"unbilled_minutes" json:"unbilled_minutes"`
	TaskCount       int64  `db:"task_count" json:"task_count"`
	OpenTasks       int64  `db:"open_tasks" json:"open_tasks"`
	InvoicedCents   *int64 `db:"invoiced_cents" json:"invoiced_cents,omitempty"`
}

func (p *Project) hideFinancials() {
	p.BillRateCents, p.BidAmountCents, p.LaborCostRateCents, p.PayRateOverrideCents = nil, nil, nil, nil
	p.ProfitShareBps, p.ProfitShareWorkerID, p.InvoicedCents = nil, nil, nil
}

const projectSelect = `
	SELECT p.id, p.client_id, c.name AS client_name, p.name, p.description, p.billing_type,
	  p.bill_rate_cents::bigint, p.bid_amount_cents, p.labor_cost_rate_cents::bigint, p.pay_rate_override_cents::bigint,
	  p.profit_share_bps::bigint, p.profit_share_worker_id, p.status, p.start_date::text, p.completed_on::text,
	  (SELECT coalesce(sum(t.minutes), 0)::bigint FROM time_entries t WHERE t.project_id = p.id) AS minutes,
	  (SELECT coalesce(sum(t.minutes), 0)::bigint FROM time_entries t
	     WHERE t.project_id = p.id AND t.billable AND t.invoice_id IS NULL) AS unbilled_minutes,
	  (SELECT count(*) FROM tasks k WHERE k.project_id = p.id) AS task_count,
	  (SELECT count(*) FROM tasks k WHERE k.project_id = p.id AND k.status <> 'done') AS open_tasks,
	  (SELECT coalesce(sum(l.amount_cents), 0)::bigint FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id
	     WHERE l.project_id = p.id AND i.status <> 'void') AS invoiced_cents
	FROM projects p LEFT JOIN clients c ON c.id = p.client_id`

type projectInput struct {
	ClientID             *int64  `json:"client_id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	BillingType          string  `json:"billing_type"`
	BillRateCents        int64   `json:"bill_rate_cents"`
	BidAmountCents       int64   `json:"bid_amount_cents"`
	LaborCostRateCents   int64   `json:"labor_cost_rate_cents"`
	PayRateOverrideCents *int64  `json:"pay_rate_override_cents"`
	ProfitShareBps       int64   `json:"profit_share_bps"`
	ProfitShareWorkerID  *int64  `json:"profit_share_worker_id"`
	Status               string  `json:"status"`
	StartDate            *string `json:"start_date"`
	CompletedOn          *string `json:"completed_on"`
	TemplateID           *int64  `json:"template_id"` // create only: copy this template's tasks
}

func (in *projectInput) validate() error {
	var err error
	if in.Name, err = requireText("name", in.Name, 200); err != nil {
		return err
	}
	if err := oneOf("billing type", in.BillingType, "hourly", "fixed"); err != nil {
		return err
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if err := oneOf("status", in.Status, "planned", "active", "done", "cancelled"); err != nil {
		return err
	}
	for name, v := range map[string]int64{"bill rate": in.BillRateCents, "bid amount": in.BidAmountCents, "labor cost rate": in.LaborCostRateCents} {
		if err := nonNegative(name, v); err != nil {
			return err
		}
	}
	if in.BillingType == "hourly" && in.BillRateCents == 0 {
		return errBad("enter the hourly bill rate")
	}
	if in.BillingType == "fixed" && in.BidAmountCents == 0 {
		return errBad("enter the bid amount")
	}
	if in.PayRateOverrideCents != nil && *in.PayRateOverrideCents < 0 {
		return errBad("pay rate override cannot be negative")
	}
	if in.ProfitShareBps < 0 || in.ProfitShareBps > 10000 {
		return errBad("profit share must be between 0%% and 100%%")
	}
	if in.ProfitShareBps > 0 && in.ProfitShareWorkerID == nil {
		return errBad("choose who receives the profit share")
	}
	if in.StartDate, err = validDate("start date", in.StartDate); err != nil {
		return err
	}
	if in.CompletedOn, err = validDate("completed on", in.CompletedOn); err != nil {
		return err
	}
	if in.Status == "done" && in.CompletedOn == nil {
		today := todayString()
		in.CompletedOn = &today
	}
	return nil
}

// GET /api/projects?client_id=&billing_type=&open=1
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	where, args := []string{}, []any{}
	if cid, err := queryID(r, "client_id"); err != nil {
		return err
	} else if cid != nil {
		args = append(args, *cid)
		where = append(where, "p.client_id = $"+itoa(len(args)))
	}
	if bt := r.URL.Query().Get("billing_type"); bt != "" {
		args = append(args, bt)
		where = append(where, "p.billing_type = $"+itoa(len(args)))
	}
	if r.URL.Query().Get("open") == "1" {
		where = append(where, "p.status IN ('planned','active')")
	}
	sql := projectSelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	list, err := collect[Project](r.Context(), s.db, sql+` ORDER BY p.status IN ('done','cancelled'), lower(coalesce(c.name, '')), lower(p.name)`, args...)
	if err != nil {
		return err
	}
	if !currentUser(r).isManager() {
		for i := range list {
			list[i].hideFinancials()
		}
	}
	return ok(w, list)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := one[Project](r.Context(), s.db, projectSelect+` WHERE p.id = $1`, id)
	if err != nil {
		return err
	}
	tasks, err := collect[Task](r.Context(), s.db, taskSelect+` WHERE k.project_id = $1 ORDER BY k.position, k.id`, id)
	if err != nil {
		return err
	}
	resp := map[string]any{"project": p, "tasks": tasks}
	if currentUser(r).isManager() {
		rows, err := s.projectReportRows(r.Context(), "p.id = $1", id)
		if err != nil {
			return err
		}
		if len(rows) == 1 {
			resp["report"] = rows[0]
		}
	} else {
		p.hideFinancials()
		resp["project"] = p
	}
	return ok(w, resp)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) error {
	var in projectInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO projects (client_id, name, description, billing_type, bill_rate_cents, bid_amount_cents,
		  labor_cost_rate_cents, pay_rate_override_cents, profit_share_bps, profit_share_worker_id, status, start_date, completed_on)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13::date) RETURNING id`,
		in.ClientID, in.Name, in.Description, in.BillingType, in.BillRateCents, in.BidAmountCents, in.LaborCostRateCents,
		in.PayRateOverrideCents, in.ProfitShareBps, in.ProfitShareWorkerID, in.Status, in.StartDate, in.CompletedOn).Scan(&id); err != nil {
		return err
	}
	if in.TemplateID != nil {
		if _, err := applyTemplate(ctx, tx, id, *in.TemplateID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	p, err := one[Project](ctx, s.db, projectSelect+` WHERE p.id = $1`, id)
	if err != nil {
		return err
	}
	return created(w, p)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in projectInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE projects SET client_id=$2, name=$3, description=$4, billing_type=$5, bill_rate_cents=$6, bid_amount_cents=$7,
		  labor_cost_rate_cents=$8, pay_rate_override_cents=$9, profit_share_bps=$10, profit_share_worker_id=$11,
		  status=$12, start_date=$13::date, completed_on=$14::date, updated_at=now()
		WHERE id=$1`,
		id, in.ClientID, in.Name, in.Description, in.BillingType, in.BillRateCents, in.BidAmountCents, in.LaborCostRateCents,
		in.PayRateOverrideCents, in.ProfitShareBps, in.ProfitShareWorkerID, in.Status, in.StartDate, in.CompletedOn)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	p, err := one[Project](r.Context(), s.db, projectSelect+` WHERE p.id = $1`, id)
	if err != nil {
		return err
	}
	return ok(w, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var used bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM time_entries WHERE project_id = $1)`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return errConflict("this project has time entries; mark it done or cancelled instead")
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM projects WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return noContent(w)
}

// ============================== tasks ==============================

type Task struct {
	ID               int64   `db:"id" json:"id"`
	ProjectID        int64   `db:"project_id" json:"project_id"`
	ProjectName      string  `db:"project_name" json:"project_name"`
	Title            string  `db:"title" json:"title"`
	Description      string  `db:"description" json:"description"`
	Status           string  `db:"status" json:"status"`
	AssigneeWorkerID *int64  `db:"assignee_worker_id" json:"assignee_worker_id"`
	AssigneeName     *string `db:"assignee_name" json:"assignee_name"`
	DueDate          *string `db:"due_date" json:"due_date"`
	EstimateMinutes  *int64  `db:"estimate_minutes" json:"estimate_minutes"`
	Position         int64   `db:"position" json:"position"`
	Minutes          int64   `db:"minutes" json:"minutes"`
}

const taskSelect = `
	SELECT k.id, k.project_id, p.name AS project_name, k.title, k.description, k.status,
	  k.assignee_worker_id, w.name AS assignee_name, k.due_date::text, k.estimate_minutes::bigint, k.position::bigint,
	  (SELECT coalesce(sum(t.minutes), 0)::bigint FROM time_entries t WHERE t.task_id = k.id) AS minutes
	FROM tasks k JOIN projects p ON p.id = k.project_id
	LEFT JOIN workers w ON w.id = k.assignee_worker_id`

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	where, args := []string{}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if pid, err := queryID(r, "project_id"); err != nil {
		return err
	} else if pid != nil {
		add("k.project_id = ?", *pid)
	}
	switch a := r.URL.Query().Get("assignee"); a {
	case "":
	case "me":
		if u.WorkerID == nil {
			return ok(w, []Task{})
		}
		add("k.assignee_worker_id = ?", *u.WorkerID)
	default:
		aid, err := queryID(r, "assignee")
		if err != nil {
			return err
		}
		add("k.assignee_worker_id = ?", *aid)
	}
	if st := r.URL.Query().Get("status"); st == "open" {
		where = append(where, "k.status <> 'done'")
	} else if st != "" {
		add("k.status = ?", st)
	}
	sql := taskSelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	list, err := collect[Task](r.Context(), s.db, sql+` ORDER BY k.due_date NULLS LAST, k.project_id, k.position, k.id LIMIT 1000`, args...)
	if err != nil {
		return err
	}
	return ok(w, list)
}

type taskInput struct {
	Title            *string `json:"title"`
	Description      *string `json:"description"`
	Status           *string `json:"status"`
	AssigneeWorkerID *int64  `json:"assignee_worker_id"`
	ClearAssignee    bool    `json:"clear_assignee"`
	DueDate          *string `json:"due_date"`
	EstimateMinutes  *int64  `json:"estimate_minutes"`
	Position         *int64  `json:"position"`
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in taskInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Title == nil {
		return errBad("title is required")
	}
	title, err := requireText("title", *in.Title, 300)
	if err != nil {
		return err
	}
	status := "todo"
	if in.Status != nil {
		status = *in.Status
	}
	if err := oneOf("status", status, "todo", "in_progress", "blocked", "done"); err != nil {
		return err
	}
	due, err := validDate("due date", in.DueDate)
	if err != nil {
		return err
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	if in.EstimateMinutes != nil && *in.EstimateMinutes < 0 {
		in.EstimateMinutes = nil
	}
	var id int64
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO tasks (project_id, title, description, status, assignee_worker_id, due_date, estimate_minutes, position, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6::date, $7,
		  coalesce((SELECT max(position) + 1 FROM tasks WHERE project_id = $1), 0),
		  CASE WHEN $4 = 'done' THEN now() END)
		RETURNING id`,
		projectID, title, desc, status, in.AssigneeWorkerID, due, in.EstimateMinutes).Scan(&id); err != nil {
		return err
	}
	t, err := one[Task](r.Context(), s.db, taskSelect+` WHERE k.id = $1`, id)
	if err != nil {
		return err
	}
	return created(w, t)
}

// PATCH /api/tasks/{id}: managers may change anything; workers may only move their
// own (assigned) tasks between statuses.
func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in taskInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u := currentUser(r)
	cur, err := one[Task](r.Context(), s.db, taskSelect+` WHERE k.id = $1`, id)
	if err != nil {
		return err
	}
	if !u.isManager() {
		mine := u.WorkerID != nil && cur.AssigneeWorkerID != nil && *u.WorkerID == *cur.AssigneeWorkerID
		onlyStatus := in.Title == nil && in.Description == nil && in.AssigneeWorkerID == nil && !in.ClearAssignee &&
			in.DueDate == nil && in.EstimateMinutes == nil && in.Position == nil
		if !mine || !onlyStatus {
			return errForbidden
		}
	}
	if in.Title != nil {
		t, err := requireText("title", *in.Title, 300)
		if err != nil {
			return err
		}
		cur.Title = t
	}
	if in.Description != nil {
		cur.Description = *in.Description
	}
	if in.Status != nil {
		if err := oneOf("status", *in.Status, "todo", "in_progress", "blocked", "done"); err != nil {
			return err
		}
		cur.Status = *in.Status
	}
	if in.ClearAssignee {
		cur.AssigneeWorkerID = nil
	} else if in.AssigneeWorkerID != nil {
		cur.AssigneeWorkerID = in.AssigneeWorkerID
	}
	if in.DueDate != nil {
		if cur.DueDate, err = validDate("due date", in.DueDate); err != nil {
			return err
		}
	}
	if in.EstimateMinutes != nil {
		if *in.EstimateMinutes < 0 {
			cur.EstimateMinutes = nil
		} else {
			cur.EstimateMinutes = in.EstimateMinutes
		}
	}
	if in.Position != nil {
		cur.Position = *in.Position
	}
	if _, err := s.db.Exec(r.Context(), `
		UPDATE tasks SET title=$2, description=$3, status=$4, assignee_worker_id=$5, due_date=$6::date,
		  estimate_minutes=$7, position=$8, updated_at=now(),
		  completed_at = CASE WHEN $4 = 'done' THEN coalesce(completed_at, now()) END
		WHERE id=$1`,
		id, cur.Title, cur.Description, cur.Status, cur.AssigneeWorkerID, cur.DueDate, cur.EstimateMinutes, cur.Position); err != nil {
		return err
	}
	t, err := one[Task](r.Context(), s.db, taskSelect+` WHERE k.id = $1`, id)
	if err != nil {
		return err
	}
	return ok(w, t)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM tasks WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return noContent(w)
}

// ============================== task templates ==============================

type TaskTemplate struct {
	ID          int64              `db:"id" json:"id"`
	Name        string             `db:"name" json:"name"`
	Description string             `db:"description" json:"description"`
	Items       []TaskTemplateItem `db:"-" json:"items"`
}

type TaskTemplateItem struct {
	ID              int64  `db:"id" json:"id"`
	TemplateID      int64  `db:"template_id" json:"-"`
	Title           string `db:"title" json:"title"`
	Description     string `db:"description" json:"description"`
	EstimateMinutes *int64 `db:"estimate_minutes" json:"estimate_minutes"`
	Position        int64  `db:"position" json:"position"`
}

func (s *Server) loadTemplates(ctx context.Context, q querier, where string, args ...any) ([]TaskTemplate, error) {
	tpls, err := collect[TaskTemplate](ctx, q, `SELECT id, name, description FROM task_templates `+where+` ORDER BY lower(name)`, args...)
	if err != nil || len(tpls) == 0 {
		return tpls, err
	}
	ids := make([]int64, len(tpls))
	idx := map[int64]int{}
	for i, t := range tpls {
		ids[i], idx[t.ID] = t.ID, i
		tpls[i].Items = []TaskTemplateItem{}
	}
	items, err := collect[TaskTemplateItem](ctx, q, `
		SELECT id, template_id, title, description, estimate_minutes::bigint, position::bigint
		FROM task_template_items WHERE template_id = ANY($1) ORDER BY position, id`, ids)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		i := idx[it.TemplateID]
		tpls[i].Items = append(tpls[i].Items, it)
	}
	return tpls, nil
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) error {
	tpls, err := s.loadTemplates(r.Context(), s.db, "")
	if err != nil {
		return err
	}
	return ok(w, tpls)
}

type templateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Items       []struct {
		Title           string `json:"title"`
		Description     string `json:"description"`
		EstimateMinutes *int64 `json:"estimate_minutes"`
	} `json:"items"`
}

// saveTemplate creates (id == 0) or fully replaces a template and its ordered task list.
func (s *Server) saveTemplate(ctx context.Context, id int64, in templateInput) (TaskTemplate, error) {
	name, err := requireText("name", in.Name, 200)
	if err != nil {
		return TaskTemplate{}, err
	}
	if len(in.Items) > 200 {
		return TaskTemplate{}, errBad("a template can have at most 200 tasks")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TaskTemplate{}, err
	}
	defer tx.Rollback(ctx)
	if id == 0 {
		if err := tx.QueryRow(ctx, `INSERT INTO task_templates (name, description) VALUES ($1, $2) RETURNING id`, name, in.Description).Scan(&id); err != nil {
			return TaskTemplate{}, err
		}
	} else {
		tag, err := tx.Exec(ctx, `UPDATE task_templates SET name = $2, description = $3, updated_at = now() WHERE id = $1`, id, name, in.Description)
		if err != nil {
			return TaskTemplate{}, err
		}
		if tag.RowsAffected() == 0 {
			return TaskTemplate{}, errNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM task_template_items WHERE template_id = $1`, id); err != nil {
			return TaskTemplate{}, err
		}
	}
	pos := 0
	for _, it := range in.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		if it.EstimateMinutes != nil && *it.EstimateMinutes < 0 {
			it.EstimateMinutes = nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_template_items (template_id, title, description, estimate_minutes, position) VALUES ($1, $2, $3, $4, $5)`,
			id, title, it.Description, it.EstimateMinutes, pos); err != nil {
			return TaskTemplate{}, err
		}
		pos++
	}
	tpls, err := s.loadTemplates(ctx, tx, "WHERE id = $1", id)
	if err != nil {
		return TaskTemplate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TaskTemplate{}, err
	}
	return tpls[0], nil
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) error {
	var in templateInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	t, err := s.saveTemplate(r.Context(), 0, in)
	if err != nil {
		return err
	}
	return created(w, t)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in templateInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	t, err := s.saveTemplate(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, t)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM task_templates WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return noContent(w)
}

// applyTemplate appends the template's tasks to the project, skipping titles the project already has.
func applyTemplate(ctx context.Context, tx pgx.Tx, projectID, templateID int64) (int64, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM task_templates WHERE id = $1)`, templateID).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, errBad("template not found")
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO tasks (project_id, title, description, estimate_minutes, position)
		SELECT $1, i.title, i.description, i.estimate_minutes,
		  coalesce((SELECT max(position) + 1 FROM tasks WHERE project_id = $1), 0) + row_number() OVER (ORDER BY i.position, i.id) - 1
		FROM task_template_items i
		WHERE i.template_id = $2
		  AND NOT EXISTS (SELECT 1 FROM tasks k WHERE k.project_id = $1 AND lower(k.title) = lower(i.title))`,
		projectID, templateID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// POST /api/projects/{id}/apply-template {template_id}
func (s *Server) applyTemplateToProject(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		TemplateID int64 `json:"template_id"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	n, err := applyTemplate(ctx, tx, projectID, in.TemplateID)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	tasks, err := collect[Task](ctx, s.db, taskSelect+` WHERE k.project_id = $1 ORDER BY k.position, k.id`, projectID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"added": n, "tasks": tasks})
}

// POST /api/projects/{id}/save-as-template {name} — turn a project's task list into a reusable template.
func (s *Server) saveProjectAsTemplate(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	tasks, err := collect[Task](r.Context(), s.db, taskSelect+` WHERE k.project_id = $1 ORDER BY k.position, k.id`, projectID)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return errBad("this project has no tasks to save")
	}
	var tin templateInput
	tin.Name = in.Name
	for _, t := range tasks {
		tin.Items = append(tin.Items, struct {
			Title           string `json:"title"`
			Description     string `json:"description"`
			EstimateMinutes *int64 `json:"estimate_minutes"`
		}{t.Title, t.Description, t.EstimateMinutes})
	}
	tpl, err := s.saveTemplate(r.Context(), 0, tin)
	if err != nil {
		return err
	}
	return created(w, tpl)
}
