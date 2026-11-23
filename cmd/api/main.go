// Command api serves the FinCore payments API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"fincore/internal/httpapi"
	"fincore/internal/idempotency"
	"fincore/internal/ledger"
	"fincore/internal/operators"
	"fincore/internal/payments"
	"fincore/internal/platform"
	"fincore/internal/provider"
	"fincore/internal/provider/razorpay"
	"fincore/internal/ratelimit"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	shutdownTracing, err := platform.SetupTracing(ctx, "fincore-api")
	if err != nil {
		return err
	}
	defer shutdownTracing(context.Background())
	platform.ServeMetrics(ctx, cfg.MetricsAddr)
	pool, err := platform.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := platform.EnsureMode(ctx, pool, cfg.Mode); err != nil {
		return err
	}
	rdb, err := platform.NewDragonfly(ctx, cfg.DragonflyAddr, cfg.DragonflyPassword)
	if err != nil {
		return err
	}
	defer rdb.Close()

	// Every configured provider is registered; the default goes first.
	var rzp *razorpay.Client
	if cfg.RazorpayKeyID != "" {
		rzp = razorpay.New(cfg.RazorpayKeyID, cfg.RazorpayKeySecret, cfg.RazorpayWebhookSecret)
	}
	var providers []provider.Provider
	if cfg.Sandbox { // never in live mode
		providers = append(providers, provider.NewSandbox())
	}
	if rzp != nil {
		if cfg.PaymentProvider == "razorpay" {
			providers = append([]provider.Provider{rzp}, providers...)
		} else {
			providers = append(providers, rzp)
		}
	}
	if len(providers) == 0 {
		return fmt.Errorf("no payment provider configured")
	}

	l := ledger.New(pool)
	svc := payments.NewService(pool, l, providers...)
	svc.Mode = cfg.Mode
	srv := httpapi.NewServer(svc, l, idempotency.NewStore(rdb), map[string]httpapi.HealthCheck{
		"postgres":  pool.Ping,
		"dragonfly": func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
	})
	srv.Sandbox = cfg.Sandbox
	srv.Mode = cfg.Mode
	srv.Operators = operators.NewService(pool)
	srv.AdminToken = cfg.AdminToken
	srv.Razorpay = rzp
	srv.Redis = rdb
	srv.Limiter = ratelimit.New(rdb, cfg.RateLimitPerMinute, time.Minute)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           otelhttp.NewHandler(srv.Routes(), "fincore-api"),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.HTTPAddr, "mode", cfg.Mode, "sandbox", cfg.Sandbox,
			"default_provider", providers[0].Name(), "razorpay", rzp != nil)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
	return nil
}
