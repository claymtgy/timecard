package idp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Organization is a company using the service.
type Organization struct {
	ID        string
	Name      string
	HasJoin   bool // an organization password is set
	Members   int64
	CreatedAt time.Time
}

var (
	ErrOrgNotFound      = errors.New("organization not found")
	ErrOrgNameTaken     = errors.New("an organization with that name already exists")
	ErrOrgPasswordTaken = errors.New("that password is already used by another organization")

	ErrOrgPasswordUnavailable = errors.New("organization password can't be recovered")
)

// Orgs is the organization repository.
//
// The organization password alone must identify the organization, so it is stored as an
// HMAC keyed with the server's crypto key rather than a salted hash: that allows a direct
// lookup, and a leaked database alone can't be used to guess passwords offline.
// Changing IDP_CRYPTO_KEY therefore invalidates organization passwords (`idp org passwd` resets them).
type Orgs struct {
	db  *pgxpool.Pool
	key []byte
}

func NewOrgs(db *pgxpool.Pool, cryptoKey string) *Orgs {
	return &Orgs{db: db, key: []byte(cryptoKey)}
}

// normalizeOrgPassword ignores case and surrounding space, since people retype these on phones.
func normalizeOrgPassword(p string) string { return strings.ToUpper(strings.TrimSpace(p)) }

func (o *Orgs) hash(password string) string {
	mac := hmac.New(sha256.New, o.key)
	mac.Write([]byte("org-password:" + normalizeOrgPassword(password)))
	return hex.EncodeToString(mac.Sum(nil))
}

// aead returns AES-256-GCM keyed from the server key (separately from the lookup hash).
func (o *Orgs) aead() (cipher.AEAD, error) {
	key := sha256.Sum256(append([]byte("org-password-encryption:"), o.key...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (o *Orgs) encrypt(password string) (string, error) {
	gcm, err := o.aead()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(strings.TrimSpace(password)), nil)), nil
}

func (o *Orgs) decrypt(encoded string) (string, error) {
	gcm, err := o.aead()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("malformed encrypted organization password")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Password returns the organization's password, or ErrOrgPasswordUnavailable if none is
// recoverable (never set, set before encryption existed, or IDP_CRYPTO_KEY has changed).
func (o *Orgs) Password(ctx context.Context, id string) (string, error) {
	var encoded *string
	err := o.db.QueryRow(ctx, `SELECT password_encrypted FROM organizations WHERE id::text = $1`, id).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrOrgNotFound
	}
	if err != nil {
		return "", err
	}
	if encoded == nil {
		return "", ErrOrgPasswordUnavailable
	}
	password, err := o.decrypt(*encoded)
	if err != nil {
		return "", ErrOrgPasswordUnavailable
	}
	return password, nil
}

// GenerateOrgPassword returns a random password like "K7QH-2MZT-9XWC-R4PB" (about 80 bits).
func GenerateOrgPassword() string {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ" // 32 symbols, no 0/O/1/I
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	var b strings.Builder
	for i, c := range buf {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(alphabet[int(c)%len(alphabet)])
	}
	return b.String()
}

func validateOrgPassword(p string) error {
	if len(normalizeOrgPassword(p)) < 10 {
		return errors.New("organization password must be at least 10 characters")
	}
	return nil
}

func mapOrgErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "organizations_name_key" {
			return ErrOrgNameTaken
		}
		return ErrOrgPasswordTaken
	}
	return err
}

// Create adds an organization that people can join with password.
func (o *Orgs) Create(ctx context.Context, name, password string) (*Organization, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("organization name is required")
	}
	if err := validateOrgPassword(password); err != nil {
		return nil, err
	}
	encrypted, err := o.encrypt(password)
	if err != nil {
		return nil, err
	}
	org := &Organization{Name: name, HasJoin: true}
	err = o.db.QueryRow(ctx, `
		INSERT INTO organizations (name, password_hash, password_encrypted) VALUES ($1, $2, $3) RETURNING id::text, created_at`,
		name, o.hash(password), encrypted).Scan(&org.ID, &org.CreatedAt)
	if err != nil {
		return nil, mapOrgErr(err)
	}
	return org, nil
}

// ByPassword finds the organization an organization password belongs to.
func (o *Orgs) ByPassword(ctx context.Context, password string) (*Organization, error) {
	org := &Organization{HasJoin: true}
	err := o.db.QueryRow(ctx, `SELECT id::text, name, created_at FROM organizations WHERE password_hash = $1`, o.hash(password)).
		Scan(&org.ID, &org.Name, &org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrgNotFound
	}
	if err != nil {
		return nil, err
	}
	return org, nil
}

// ByName finds an organization by case-insensitive name.
func (o *Orgs) ByName(ctx context.Context, name string) (*Organization, error) {
	org := &Organization{}
	err := o.db.QueryRow(ctx, `
		SELECT id::text, name, password_hash IS NOT NULL, created_at FROM organizations WHERE lower(name) = lower($1)`,
		strings.TrimSpace(name)).Scan(&org.ID, &org.Name, &org.HasJoin, &org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrgNotFound
	}
	if err != nil {
		return nil, err
	}
	return org, nil
}

func (o *Orgs) List(ctx context.Context) ([]*Organization, error) {
	rows, err := o.db.Query(ctx, `
		SELECT g.id::text, g.name, g.password_hash IS NOT NULL, g.created_at,
		  (SELECT count(*) FROM users u WHERE u.org_id = g.id)
		FROM organizations g ORDER BY lower(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Organization
	for rows.Next() {
		org := &Organization{}
		if err := rows.Scan(&org.ID, &org.Name, &org.HasJoin, &org.CreatedAt, &org.Members); err != nil {
			return nil, err
		}
		out = append(out, org)
	}
	return out, rows.Err()
}

// SetPassword replaces the organization password. Existing members are unaffected.
func (o *Orgs) SetPassword(ctx context.Context, id, password string) error {
	if err := validateOrgPassword(password); err != nil {
		return err
	}
	encrypted, err := o.encrypt(password)
	if err != nil {
		return err
	}
	_, err = o.db.Exec(ctx, `UPDATE organizations SET password_hash = $2, password_encrypted = $3, updated_at = now() WHERE id::text = $1`,
		id, o.hash(password), encrypted)
	return mapOrgErr(err)
}

// Rename changes the display name; the app picks it up at each member's next sign-in.
func (o *Orgs) Rename(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("organization name is required")
	}
	_, err := o.db.Exec(ctx, `UPDATE organizations SET name = $2, updated_at = now() WHERE id::text = $1`, id, name)
	return mapOrgErr(err)
}
