package idp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

// User is an account at the identity provider.
type User struct {
	ID            string
	Email         string
	Name          string
	GivenName     string
	FamilyName    string
	PasswordHash  string
	EmailVerified bool
	Disabled      bool
	CreatedAt     time.Time
}

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrEmailTaken         = errors.New("an account with that email already exists")
	ErrInvalidCredentials = errors.New("incorrect email or password")
)

// Users is the account repository.
type Users struct{ db *pgxpool.Pool }

func NewUsers(db *pgxpool.Pool) *Users { return &Users{db: db} }

const userCols = `id::text, email, name, given_name, family_name, password_hash, email_verified, disabled, created_at`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.GivenName, &u.FamilyName, &u.PasswordHash, &u.EmailVerified, &u.Disabled, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Users) ByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id::text = $1`, id))
}

func (s *Users) ByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, strings.TrimSpace(email)))
}

func (s *Users) List(ctx context.Context) ([]*User, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY lower(email)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Create validates input, hashes the password and inserts the user.
func (s *Users) Create(ctx context.Context, email, name, password string) (*User, error) {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	if !strings.Contains(email, "@") || len(email) > 254 {
		return nil, errors.New("enter a valid email address")
	}
	if name == "" {
		return nil, errors.New("enter your name")
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	given, family := splitName(name)
	u, err := scanUser(s.db.QueryRow(ctx, `
		INSERT INTO users (email, name, given_name, family_name, password_hash)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+userCols, email, name, given, family, hash))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrEmailTaken
	}
	return u, err
}

func (s *Users) SetPassword(ctx context.Context, id, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id::text = $1`, id, hash)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return err
}

// SetDisabled enables/disables an account. Disabling also ends its SSO sessions and tokens.
func (s *Users) SetDisabled(ctx context.Context, id string, disabled bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET disabled = $2, updated_at = now() WHERE id::text = $1`, id, disabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	if disabled {
		for _, q := range []string{
			`DELETE FROM sessions WHERE user_id::text = $1`,
			`DELETE FROM access_tokens WHERE user_id::text = $1`,
			`DELETE FROM refresh_tokens WHERE user_id::text = $1`,
		} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// Authenticate checks email/password. It always runs a hash comparison so response
// time doesn't reveal whether the email exists.
func (s *Users) Authenticate(ctx context.Context, email, password string) (*User, error) {
	u, err := s.ByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		VerifyPassword(dummyHash, password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(u.PasswordHash, password) || u.Disabled {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

func splitName(name string) (given, family string) {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	default:
		return strings.Join(parts[:len(parts)-1], " "), parts[len(parts)-1]
	}
}

// --- password hashing (argon2id, OWASP-recommended parameters) --------------

const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
)

var dummyHash, _ = HashPassword("timing-equalizer-password")

func ValidatePassword(p string) error {
	if len(p) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(p) > 256 {
		return errors.New("password is too long")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
