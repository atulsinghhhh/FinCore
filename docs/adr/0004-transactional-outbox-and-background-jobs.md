# ADR 0004: Transactional outbox and background jobs

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

Merchants need to hear about state changes (payment captured, refund failed,
payout paid). Sending the HTTP call inside the request is unsafe: if we commit
and then crash, the event is lost; if we send and then roll back, the merchant
hears about something that never happened. Separately, real-world failures
(lost webhooks, closed browser tabs, provider outages) leave work half-done.

## Decision

1. **Outbox.** Every state change writes an `outbox_events` row in the same
   DB transaction. Events exist if and only if the change committed.
2. **Dispatcher** (worker) claims due rows with `FOR UPDATE SKIP LOCKED`, so
   any number of workers can run. Deliveries are signed
   (`FinCore-Signature: t=..,v1=HMAC(secret, "t.body")`), retried with
   exponential backoff + jitter, and dead-lettered after 12 attempts
   (≈ 1 day). Operators requeue dead events from the dashboard.
3. **Ordering is best-effort.** One dispatcher delivers a merchant's events in
   order and holds later ones behind a failure; retries and parallel workers
   can still reorder. Events carry the full object so merchants act on state.
4. **Sweeper.** Payments stuck in `created` are checked with the provider
   (`StatusChecker`) after 2 minutes — recovering payments whose callbacks were
   lost — and expired after 30 minutes if unpaid. It never expires a payment
   whose real state could not be checked.
5. **Inbound webhook retries.** Failed provider webhooks are retried by the
   worker with backoff, in addition to the provider's own redelivery.
6. **Integrity job.** Every 5 minutes the worker re-derives all balances from
   postings and exports `fincore_ledger_integrity_ok`; alerting pages on 0.
7. **All retry times use the database clock** (`now() + interval`), the same
   clock the claim queries compare against, so app/DB clock skew cannot hide
   or delay work.

## Consequences

- At-least-once delivery: merchants must dedupe by `FinCore-Event-Id`.
- The worker is stateless and horizontally scalable.
