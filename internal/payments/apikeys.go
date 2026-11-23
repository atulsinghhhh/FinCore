package payments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fincore/internal/store"
)

var (
	ErrUnauthenticated = errors.New("payments: invalid or revoked API key")
	// ErrWrongMode is a valid-looking key for the other mode, e.g. a test
	// key sent to the live API.
	ErrWrongMode = errors.New("payments: API key is for a different mode")
)

// KeyPrefix is fc_test_ or fc_live_, so a key's mode is visible at a glance
// and a key can never authenticate against the other mode's server.
func KeyPrefix(mode string) string { return "fc_" + mode + "_" }

type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	MerchantID uuid.UUID  `json:"merchant_id"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	// Secret is only set when the key is created; it is never stored.
	Secret string `json:"secret,omitempty"`
}

// CreateAPIKey issues a new key for a merchant. The secret is returned once.
func (s *Service) CreateAPIKey(ctx context.Context, merchantID uuid.UUID) (APIKey, error) {
	if _, err := s.getMerchant(ctx, s.q, merchantID); err != nil {
		return APIKey{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return APIKey{}, err
	}
	prefix := KeyPrefix(s.Mode)
	secret := prefix + hex.EncodeToString(raw)
	row, err := s.q.InsertAPIKey(ctx, store.InsertAPIKeyParams{
		MerchantID: merchantID, Prefix: secret[:len(prefix)+6], KeyHash: hashKey(secret),
	})
	if err != nil {
		return APIKey{}, err
	}
	k := apiKeyFromRow(row)
	k.Secret = secret
	return k, nil
}

// Authenticate resolves an API key to its merchant.
func (s *Service) Authenticate(ctx context.Context, secret string) (uuid.UUID, error) {
	if !strings.HasPrefix(secret, KeyPrefix(s.Mode)) {
		for _, other := range []string{"test", "live"} {
			if other != s.Mode && strings.HasPrefix(secret, KeyPrefix(other)) {
				return uuid.Nil, fmt.Errorf("%w: a %s key cannot be used with the %s API", ErrWrongMode, other, s.Mode)
			}
		}
		return uuid.Nil, ErrUnauthenticated
	}
	row, err := s.q.GetActiveAPIKeyByHash(ctx, hashKey(secret))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUnauthenticated
	}
	if err != nil {
		return uuid.Nil, err
	}
	_ = s.q.TouchAPIKey(ctx, row.ID) // best effort
	return row.MerchantID, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, merchantID uuid.UUID) ([]APIKey, error) {
	rows, err := s.q.ListAPIKeys(ctx, merchantID)
	if err != nil {
		return nil, err
	}
	out := make([]APIKey, 0, len(rows))
	for _, r := range rows {
		out = append(out, apiKeyFromRow(r))
	}
	return out, nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, merchantID, keyID uuid.UUID) (APIKey, error) {
	row, err := s.q.RevokeAPIKey(ctx, store.RevokeAPIKeyParams{ID: keyID, MerchantID: merchantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, fmt.Errorf("%w: active api key %s", ErrNotFound, keyID)
	}
	if err != nil {
		return APIKey{}, err
	}
	return apiKeyFromRow(row), nil
}

func hashKey(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func apiKeyFromRow(r store.ApiKey) APIKey {
	return APIKey{ID: r.ID, MerchantID: r.MerchantID, Prefix: r.Prefix, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt}
}
