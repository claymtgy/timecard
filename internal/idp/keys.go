package idp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5/pgxpool"
)

// loadSigningKeys returns all active keys, newest first, generating one if none exist.
func loadSigningKeys(ctx context.Context, db *pgxpool.Pool) ([]*signingKey, error) {
	keys, err := readSigningKeys(ctx, db)
	if err != nil || len(keys) > 0 {
		return keys, err
	}
	if err := generateSigningKey(ctx, db); err != nil {
		return nil, err
	}
	return readSigningKeys(ctx, db)
}

func readSigningKeys(ctx context.Context, db *pgxpool.Pool) ([]*signingKey, error) {
	rows, err := db.Query(ctx, `SELECT id, algorithm, private_pem FROM signing_keys WHERE active ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*signingKey
	for rows.Next() {
		var id, alg, pemStr string
		if err := rows.Scan(&id, &alg, &pemStr); err != nil {
			return nil, err
		}
		block, _ := pem.Decode([]byte(pemStr))
		if block == nil {
			return nil, fmt.Errorf("signing key %s: invalid PEM", id)
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("signing key %s: %w", id, err)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("signing key is not RSA")
		}
		out = append(out, &signingKey{id: id, alg: jose.SignatureAlgorithm(alg), key: rsaKey})
	}
	return out, rows.Err()
}

func generateSigningKey(ctx context.Context, db *pgxpool.Pool) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return err
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	_, err = db.Exec(ctx, `INSERT INTO signing_keys (id, algorithm, private_pem) VALUES ($1, $2, $3)`,
		hex.EncodeToString(idBytes), string(jose.RS256), pemStr)
	return err
}
