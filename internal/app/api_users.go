package app

import "net/http"

type AppUser struct {
	ID          int64   `db:"id" json:"id"`
	Email       string  `db:"email" json:"email"`
	Name        string  `db:"name" json:"name"`
	Role        string  `db:"role" json:"role"`
	WorkerID    *int64  `db:"worker_id" json:"worker_id"`
	WorkerName  *string `db:"worker_name" json:"worker_name"`
	LastLoginAt *string `db:"last_login_at" json:"last_login_at"`
}

const appUserSelect = `
	SELECT u.id, u.email, u.name, u.role, u.worker_id, w.name AS worker_name,
	  to_char(u.last_login_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS last_login_at
	FROM users u LEFT JOIN workers w ON w.id = u.worker_id`

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	list, err := collect[AppUser](r.Context(), s.db, appUserSelect+` ORDER BY lower(u.name), u.id`)
	if err != nil {
		return err
	}
	return ok(w, list)
}

type userUpdateInput struct {
	Role        *string `json:"role"`
	WorkerID    *int64  `json:"worker_id"`
	ClearWorker bool    `json:"clear_worker"`
}

// PATCH /api/users/{id} — change role or link the login to a worker.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in userUpdateInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if in.Role != nil {
		if err := oneOf("role", *in.Role, "admin", "manager", "worker"); err != nil {
			return err
		}
		if *in.Role != "admin" {
			var otherAdmins int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND id <> $1`, id).Scan(&otherAdmins); err != nil {
				return err
			}
			if otherAdmins == 0 {
				return errConflict("there must be at least one admin")
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, *in.Role)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFound
		}
	}
	if in.ClearWorker {
		if _, err := tx.Exec(ctx, `UPDATE users SET worker_id = NULL WHERE id = $1`, id); err != nil {
			return err
		}
	} else if in.WorkerID != nil {
		if _, err := tx.Exec(ctx, `UPDATE users SET worker_id = $2 WHERE id = $1`, id, *in.WorkerID); err != nil {
			return err
		}
	}
	u, err := one[AppUser](ctx, tx, appUserSelect+` WHERE u.id = $1`, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return ok(w, u)
}
