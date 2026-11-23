// Package platform holds process-level wiring: configuration and clients.
package platform

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Modes, as in Razorpay: test and live are separate deployments with
// separate databases and keys, never mixed.
const (
	ModeTest = "test"
	ModeLive = "live"
)

type Config struct {
	// Mode is "test" (default) or "live".
	Mode              string
	DatabaseURL       string
	DragonflyAddr     string
	DragonflyPassword string
	HTTPAddr          string
	// Sandbox enables simulated provider and bank endpoints. Never in production.
	Sandbox bool
	// PaymentProvider is the default provider for new payments.
	PaymentProvider       string
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string

	// Payments still awaiting the customer are checked with the provider
	// after PaymentCheckAfter and expired after PaymentExpireAfter.
	PaymentCheckAfter  time.Duration
	PaymentExpireAfter time.Duration
	// RateLimitPerMinute applies per API client.
	RateLimitPerMinute int
	// AdminToken authenticates /v1/admin; empty disables the admin API.
	AdminToken string
	// MetricsAddr serves /metrics on a separate, internal-only listener.
	MetricsAddr string
}

func LoadConfig() (Config, error) {
	c := Config{
		Mode:              getenv("FINCORE_MODE", ModeTest),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		DragonflyAddr:     getenv("DRAGONFLY_ADDR", "localhost:6379"),
		DragonflyPassword: os.Getenv("DRAGONFLY_PASSWORD"),
		HTTPAddr:          ":" + getenv("HTTP_PORT", "8080"),
		Sandbox:           getenv("SANDBOX", "true") == "true",
		PaymentProvider:   getenv("PAYMENT_PROVIDER", "sandbox"),

		RazorpayKeyID:         os.Getenv("RAZORPAY_KEY_ID"),
		RazorpayKeySecret:     os.Getenv("RAZORPAY_KEY_SECRET"),
		RazorpayWebhookSecret: os.Getenv("RAZORPAY_WEBHOOK_SECRET"),

		PaymentCheckAfter:  getduration("PAYMENT_CHECK_AFTER", 2*time.Minute),
		PaymentExpireAfter: getduration("PAYMENT_EXPIRE_AFTER", 30*time.Minute),
		RateLimitPerMinute: getint("RATE_LIMIT_PER_MINUTE", 600),
		AdminToken:         os.Getenv("ADMIN_API_TOKEN"),
		MetricsAddr:        getenv("METRICS_ADDR", ":9091"),
	}
	if c.AdminToken != "" && len(c.AdminToken) < 32 {
		return c, fmt.Errorf("ADMIN_API_TOKEN must be at least 32 characters")
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if err := c.validateMode(); err != nil {
		return c, err
	}
	if c.PaymentProvider == "razorpay" && (c.RazorpayKeyID == "" || c.RazorpayKeySecret == "") {
		return c, fmt.Errorf("PAYMENT_PROVIDER=razorpay needs RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET")
	}
	return c, nil
}

// validateMode enforces the hard separation between test and live.
func (c *Config) validateMode() error {
	switch c.Mode {
	case ModeTest:
		if c.RazorpayKeyID != "" && !strings.HasPrefix(c.RazorpayKeyID, "rzp_test_") {
			return fmt.Errorf("test mode needs Razorpay test keys (rzp_test_...), got a %s key", keyKind(c.RazorpayKeyID))
		}
	case ModeLive:
		if !strings.HasPrefix(c.RazorpayKeyID, "rzp_live_") || c.RazorpayKeySecret == "" {
			return fmt.Errorf("live mode needs Razorpay live keys (rzp_live_...)")
		}
		if c.RazorpayWebhookSecret == "" {
			return fmt.Errorf("live mode needs RAZORPAY_WEBHOOK_SECRET")
		}
		if c.AdminToken == "" {
			return fmt.Errorf("live mode needs ADMIN_API_TOKEN")
		}
		// Simulated providers and bank callbacks never run against real money.
		c.Sandbox = false
		c.PaymentProvider = "razorpay"
	default:
		return fmt.Errorf("FINCORE_MODE must be %q or %q, got %q", ModeTest, ModeLive, c.Mode)
	}
	return nil
}

func keyKind(id string) string {
	if strings.HasPrefix(id, "rzp_live_") {
		return "live"
	}
	return "non-test"
}

// EnsureMode stamps an empty database with this instance's mode, and refuses
// to run against a database stamped with the other mode.
func EnsureMode(ctx context.Context, pool *pgxpool.Pool, mode string) error {
	if _, err := pool.Exec(ctx, `INSERT INTO instance_settings (mode) VALUES ($1) ON CONFLICT (singleton) DO NOTHING`, mode); err != nil {
		return fmt.Errorf("stamp database mode: %w", err)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT mode FROM instance_settings`).Scan(&stored); err != nil {
		return err
	}
	if stored != mode {
		return fmt.Errorf("this database holds %s-mode data; refusing to start in %s mode (use a separate database per mode)", stored, mode)
	}
	return nil
}

func NewPostgres(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	cfg.ConnConfig.Tracer = otelpgx.NewTracer() // no-op unless tracing is enabled
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return pool, nil
}

// NewDragonfly connects to Dragonfly using the Redis protocol.
func NewDragonfly(ctx context.Context, addr, password string) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("dragonfly ping: %w", err)
	}
	return rdb, nil
}

func getduration(key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return d
	}
	return fallback
}

func getint(key string, fallback int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return fallback
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
