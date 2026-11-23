// Command worker runs FinCore's background jobs:
//
//   - outbox: delivers events to merchant webhooks (retries, dead letters)
//   - webhook-retry: re-applies provider webhooks that failed transiently
//   - sweeper: recovers payments whose callbacks were lost, expires the rest
//
// Every job is safe to run in several worker processes at once.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"fincore/internal/ledger"
	"fincore/internal/metrics"
	"fincore/internal/outbox"
	"fincore/internal/payments"
	"fincore/internal/platform"
	"fincore/internal/provider"
	"fincore/internal/provider/razorpay"
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
	shutdownTracing, err := platform.SetupTracing(ctx, "fincore-worker")
	if err != nil {
		return err
	}
	defer shutdownTracing(context.Background())
	metricsAddr := os.Getenv("WORKER_METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":9092"
	}
	platform.ServeMetrics(ctx, metricsAddr)
	pool, err := platform.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := platform.EnsureMode(ctx, pool, cfg.Mode); err != nil {
		return err
	}

	var providers []provider.Provider
	if cfg.Sandbox { // never in live mode
		providers = append(providers, provider.NewSandbox())
	}
	if cfg.RazorpayKeyID != "" {
		providers = append(providers, razorpay.New(cfg.RazorpayKeyID, cfg.RazorpayKeySecret, cfg.RazorpayWebhookSecret))
	}
	l := ledger.New(pool)
	svc := payments.NewService(pool, l, providers...)
	svc.Mode = cfg.Mode
	dispatcher := outbox.NewDispatcher(pool)
	parsers := map[string]func(string, []byte) (provider.Event, error){"razorpay": razorpay.ParseWebhook}

	var wg sync.WaitGroup
	every := func(name string, interval time.Duration, job func(context.Context) (int, error)) {
		wg.Go(func() {
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				// Drain: keep going while there is work, then wait.
				for {
					n, err := job(ctx)
					if err != nil && ctx.Err() == nil {
						slog.Error("job failed", "job", name, "err", err)
					}
					if n > 0 {
						slog.Info("job ran", "job", name, "items", n)
					}
					if n == 0 || err != nil || ctx.Err() != nil {
						break
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		})
	}

	every("outbox", time.Second, dispatcher.DispatchOnce)
	every("webhook-retry", 5*time.Second, func(ctx context.Context) (int, error) {
		found, err := svc.RetryWebhook(ctx, parsers)
		if found {
			return 1, err
		}
		return 0, err
	})
	every("sweeper", 30*time.Second, func(ctx context.Context) (int, error) {
		res, err := svc.SweepStalePayments(ctx, cfg.PaymentCheckAfter, cfg.PaymentExpireAfter)
		if res.Recovered+res.Expired > 0 {
			slog.Info("sweeper", "recovered", res.Recovered, "expired", res.Expired)
		}
		return 0, err // one pass per tick
	})

	// Operational gauges for dashboards and alerts.
	every("outbox-stats", 15*time.Second, func(ctx context.Context) (int, error) {
		stats, err := svc.OutboxStats(ctx)
		if err != nil {
			return 0, err
		}
		for _, st := range []string{outbox.StatusPending, outbox.StatusDelivered, outbox.StatusSkipped, outbox.StatusDead} {
			metrics.OutboxEvents.WithLabelValues(st).Set(0)
		}
		metrics.OutboxOldestPending.Set(0)
		for _, st := range stats {
			metrics.OutboxEvents.WithLabelValues(st.Status).Set(float64(st.Count))
			if st.Status == outbox.StatusPending {
				metrics.OutboxOldestPending.Set(time.Since(st.OldestPending).Seconds())
			}
		}
		return 0, nil
	})
	// Re-derive every balance from raw postings. A failure here means money
	// state is wrong somewhere: alert on fincore_ledger_integrity_ok == 0.
	every("ledger-integrity", 5*time.Minute, func(ctx context.Context) (int, error) {
		report, err := l.CheckIntegrity(ctx)
		if err != nil {
			return 0, err
		}
		metrics.LedgerIntegrityChecked.SetToCurrentTime()
		if report.OK() {
			metrics.LedgerIntegrityOK.Set(1)
		} else {
			metrics.LedgerIntegrityOK.Set(0)
			slog.Error("LEDGER INTEGRITY CHECK FAILED", "trial_balance", report.TrialBalance, "mismatches", report.Mismatches)
		}
		return 0, nil
	})

	slog.Info("worker started", "check_after", cfg.PaymentCheckAfter, "expire_after", cfg.PaymentExpireAfter)
	<-ctx.Done()
	slog.Info("worker stopping")
	wg.Wait()
	return nil
}
