package app

import (
	"net/http"
	"strings"
)

// ============================== workers ==============================

type Worker struct {
	ID           int64   `db:"id" json:"id"`
	Name         string  `db:"name" json:"name"`
	Email        string  `db:"email" json:"email"`
	Phone        string  `db:"phone" json:"phone"`
	PayRateCents int64   `db:"pay_rate_cents" json:"pay_rate_cents"`
	Active       bool    `db:"active" json:"active"`
	Notes        string  `db:"notes" json:"notes"`
	UserID       *int64  `db:"user_id" json:"user_id"`
	UserEmail    *string `db:"user_email" json:"user_email"`
}

const workerSelect = `
	SELECT w.id, w.name, w.email, w.phone, w.pay_rate_cents, w.active, w.notes, u.id AS user_id, u.email AS user_email
	FROM workers w LEFT JOIN users u ON u.worker_id = w.id`

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) error {
	list, err := collect[Worker](r.Context(), s.db, workerSelect+` ORDER BY w.active DESC, lower(w.name)`)
	if err != nil {
		return err
	}
	if !currentUser(r).isManager() {
		// Workers see names only (for task assignees etc.), not pay or contact details.
		for i := range list {
			list[i] = Worker{ID: list[i].ID, Name: list[i].Name, Active: list[i].Active}
		}
	}
	return ok(w, list)
}

type workerInput struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	PayRateCents int64  `json:"pay_rate_cents"`
	Active       *bool  `json:"active"`
	Notes        string `json:"notes"`
}

func (in *workerInput) validate() error {
	var err error
	if in.Name, err = requireText("name", in.Name, 200); err != nil {
		return err
	}
	in.Email = strings.TrimSpace(in.Email)
	if in.Active == nil {
		t := true
		in.Active = &t
	}
	return nonNegative("pay rate", in.PayRateCents)
}

func (s *Server) createWorker(w http.ResponseWriter, r *http.Request) error {
	var in workerInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	var id int64
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO workers (name, email, phone, pay_rate_cents, active, notes) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		in.Name, in.Email, in.Phone, in.PayRateCents, *in.Active, in.Notes).Scan(&id); err != nil {
		return err
	}
	wk, err := one[Worker](r.Context(), s.db, workerSelect+` WHERE w.id = $1`, id)
	if err != nil {
		return err
	}
	return created(w, wk)
}

func (s *Server) updateWorker(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in workerInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE workers SET name=$2, email=$3, phone=$4, pay_rate_cents=$5, active=$6, notes=$7, updated_at=now() WHERE id=$1`,
		id, in.Name, in.Email, in.Phone, in.PayRateCents, *in.Active, in.Notes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	wk, err := one[Worker](r.Context(), s.db, workerSelect+` WHERE w.id = $1`, id)
	if err != nil {
		return err
	}
	return ok(w, wk)
}

func (s *Server) deleteWorker(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var used bool
	if err := s.db.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM time_entries WHERE worker_id=$1) OR EXISTS (SELECT 1 FROM pay_runs WHERE worker_id=$1)`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return errConflict("this worker has time or pay records; mark them inactive instead")
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM workers WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return noContent(w)
}

// ============================== clients ==============================

type Client struct {
	ID          int64  `db:"id" json:"id"`
	Name        string `db:"name" json:"name"`
	ContactName string `db:"contact_name" json:"contact_name"`
	Email       string `db:"email" json:"email"`
	Phone       string `db:"phone" json:"phone"`
	Address     string `db:"address" json:"address"`
	Notes       string `db:"notes" json:"notes"`
	Active      bool   `db:"active" json:"active"`
	// Aggregates (list view)
	ProjectCount     int64 `db:"project_count" json:"project_count"`
	OutstandingCents int64 `db:"outstanding_cents" json:"outstanding_cents"`
}

const clientSelect = `
	SELECT c.id, c.name, c.contact_name, c.email, c.phone, c.address, c.notes, c.active,
	  (SELECT count(*) FROM projects p WHERE p.client_id = c.id) AS project_count,
	  (SELECT coalesce(sum(l.amount_cents), 0)::bigint FROM invoices i JOIN invoice_lines l ON l.invoice_id = i.id
	     WHERE i.client_id = c.id AND i.status = 'sent') AS outstanding_cents
	FROM clients c`

type clientInput struct {
	Name        string `json:"name"`
	ContactName string `json:"contact_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	Notes       string `json:"notes"`
	Active      *bool  `json:"active"`
}

func (in *clientInput) validate() error {
	var err error
	if in.Name, err = requireText("name", in.Name, 200); err != nil {
		return err
	}
	if in.Active == nil {
		t := true
		in.Active = &t
	}
	return nil
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) error {
	list, err := collect[Client](r.Context(), s.db, clientSelect+` ORDER BY c.active DESC, lower(c.name)`)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) getClient(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := one[Client](r.Context(), s.db, clientSelect+` WHERE c.id = $1`, id)
	if err != nil {
		return err
	}
	projects, err := collect[Project](r.Context(), s.db, projectSelect+` WHERE p.client_id = $1 ORDER BY p.created_at DESC`, id)
	if err != nil {
		return err
	}
	invoices, err := collect[Invoice](r.Context(), s.db, invoiceSelect+` WHERE i.client_id = $1 ORDER BY i.number DESC`, id)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"client": c, "projects": projects, "invoices": invoices})
}

func (s *Server) createClient(w http.ResponseWriter, r *http.Request) error {
	var in clientInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	var id int64
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO clients (name, contact_name, email, phone, address, notes, active) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		in.Name, in.ContactName, in.Email, in.Phone, in.Address, in.Notes, *in.Active).Scan(&id); err != nil {
		return err
	}
	c, err := one[Client](r.Context(), s.db, clientSelect+` WHERE c.id = $1`, id)
	if err != nil {
		return err
	}
	return created(w, c)
}

func (s *Server) updateClient(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in clientInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE clients SET name=$2, contact_name=$3, email=$4, phone=$5, address=$6, notes=$7, active=$8, updated_at=now() WHERE id=$1`,
		id, in.Name, in.ContactName, in.Email, in.Phone, in.Address, in.Notes, *in.Active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	c, err := one[Client](r.Context(), s.db, clientSelect+` WHERE c.id = $1`, id)
	if err != nil {
		return err
	}
	return ok(w, c)
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM clients WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return noContent(w)
}
