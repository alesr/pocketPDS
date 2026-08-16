package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alesr/pocketPDS/internal/identity"
)

func (s *Store) SeedDevAccount(ctx context.Context, handle, password, serviceEndpoint string) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	keys, err := identity.CreateDidWeb(handle, serviceEndpoint)
	if err != nil {
		return err
	}
	didDocJSON, err := json.Marshal(keys.DidDoc)
	if err != nil {
		return err
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return err
	}
	signingKey, err := s.Box.Encrypt(keys.SigningKey.Bytes())
	if err != nil {
		return err
	}
	recoveryKey, err := s.Box.Encrypt(keys.RecoveryKey.Bytes())
	if err != nil {
		return err
	}

	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO accounts (did, handle, email, password_hash, recovery_key, signing_key, tid_clock, created_at, did_doc)
		 VALUES (?, ?, '', ?, ?, ?, 0, ?, ?)`,
		keys.Did, handle, passwordHash, recoveryKey, signingKey,
		time.Now().Format(time.RFC3339), string(didDocJSON),
	)
	if err != nil {
		return fmt.Errorf("seed dev account: %w", err)
	}
	return nil
}
