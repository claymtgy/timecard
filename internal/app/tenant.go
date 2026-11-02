package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tenantDB sends queries to the request's tenant transaction when the context has one,
// and to the pool otherwise. Handlers use it like a pool; isolation between organizations
// comes from row-level security (migrations/0003_organizations.sql), not from each query.
type tenantDB struct{ pool *pgxpool.Pool }

type tenantTxKey struct{}

func txFrom(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(tenantTxKey{}).(pgx.Tx)
	return tx
}

func (d *tenantDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx := txFrom(ctx); tx != nil {
		return tx.Query(ctx, sql, args...)
	}
	return d.pool.Query(ctx, sql, args...)
}

func (d *tenantDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx := txFrom(ctx); tx != nil {
		return tx.QueryRow(ctx, sql, args...)
	}
	return d.pool.QueryRow(ctx, sql, args...)
}

func (d *tenantDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx := txFrom(ctx); tx != nil {
		return tx.Exec(ctx, sql, args...)
	}
	return d.pool.Exec(ctx, sql, args...)
}

// Begin starts a transaction, or a savepoint inside the request's tenant transaction.
func (d *tenantDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if tx := txFrom(ctx); tx != nil {
		return tx.Begin(ctx)
	}
	return d.pool.Begin(ctx)
}

func (d *tenantDB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// bufferedResponse holds the response until the transaction has committed, so a client
// never sees success for a write that then fails, or reads before its own write is visible.
type bufferedResponse struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// send writes the buffered response; called after the transaction is finished.
func (b *bufferedResponse) send() {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	b.ResponseWriter.WriteHeader(b.status)
	b.ResponseWriter.Write(b.body.Bytes())
}

// withTenant runs an authenticated request in a transaction limited to the user's organization.
// It commits on success and rolls back on any error response.
func (s *Server) withTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		u := currentUser(r)
		tx, err := s.db.pool.Begin(ctx)
		if err != nil {
			slog.Error("begin tenant transaction", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database unavailable"})
			return
		}
		defer tx.Rollback(context.Background()) // no-op after commit

		if _, err := tx.Exec(ctx, `SET LOCAL ROLE timecard_tenant`); err == nil {
			_, err = tx.Exec(ctx, `SELECT set_config('app.org_id', $1, true)`, u.OrgID)
			if err != nil {
				slog.Error("set tenant organization", "err", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
		} else {
			slog.Error("set tenant role", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		buf := &bufferedResponse{ResponseWriter: w}
		next.ServeHTTP(buf, r.WithContext(context.WithValue(ctx, tenantTxKey{}, tx)))

		if buf.status >= 400 {
			tx.Rollback(context.Background())
		} else if err := tx.Commit(ctx); err != nil {
			slog.Error("commit tenant transaction", "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the change could not be saved"})
			return
		}
		buf.send()
	})
}
