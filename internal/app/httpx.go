package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/claym/crm/internal/calc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// apiError is an error with an HTTP status and a message safe to show users.
type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func errBad(format string, a ...any) error {
	return &apiError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}
func errConflict(format string, a ...any) error {
	return &apiError{http.StatusConflict, fmt.Sprintf(format, a...)}
}

var (
	errNotFound     = &apiError{http.StatusNotFound, "not found"}
	errForbidden    = &apiError{http.StatusForbidden, "you don't have permission to do that"}
	errUnauthorized = &apiError{http.StatusUnauthorized, "sign in required"}
)

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// h adapts an error-returning handler, mapping errors to JSON responses.
func h(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		var ae *apiError
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &ae):
			writeJSON(w, ae.Status, map[string]string{"error": ae.Msg})
		case errors.Is(err, pgx.ErrNoRows):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			writeJSON(w, http.StatusConflict, map[string]string{"error": "that already exists"})
		case errors.As(err, &pgErr) && pgErr.Code == "23503":
			writeJSON(w, http.StatusConflict, map[string]string{"error": "it is referenced by other records (or refers to a record that doesn't exist)"})
		case errors.As(err, &pgErr) && pgErr.Code == "23514":
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a value is out of the allowed range"})
		default:
			slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json", "err", err)
	}
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

func created(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusCreated, v)
	return nil
}

func noContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errBad("invalid JSON body: %v", err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errNotFound
	}
	return id, nil
}

// queryID returns the positive integer query parameter, or nil if absent.
func queryID(r *http.Request, name string) (*int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, errBad("%s must be a positive integer", name)
	}
	return &id, nil
}

// queryDate returns a validated YYYY-MM-DD query parameter, or nil if absent.
func queryDate(r *http.Request, name string) (*string, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	if _, err := calc.ParseDate(raw); err != nil {
		return nil, errBad("%s: %v", name, err)
	}
	return &raw, nil
}

// validDate checks an optional YYYY-MM-DD string, normalising "" to nil.
func validDate(field string, s *string) (*string, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	if _, err := calc.ParseDate(v); err != nil {
		return nil, errBad("%s: %v", field, err)
	}
	return &v, nil
}

func requireDate(field, s string) (string, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", errBad("%s is required", field)
	}
	if _, err := calc.ParseDate(v); err != nil {
		return "", errBad("%s: %v", field, err)
	}
	return v, nil
}

func requireText(field, s string, max int) (string, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", errBad("%s is required", field)
	}
	if len(v) > max {
		return "", errBad("%s is too long (max %d characters)", field, max)
	}
	return v, nil
}

func oneOf(field, v string, allowed ...string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return errBad("%s must be one of: %s", field, strings.Join(allowed, ", "))
}

func nonNegative(field string, v int64) error {
	if v < 0 {
		return errBad("%s cannot be negative", field)
	}
	return nil
}
