# ADR 0003: Asynchronous providers, confirm + webhooks, ledger before provider

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

With Razorpay the customer authorizes in Checkout, in the browser. We learn
the outcome two ways that can arrive in any order, late, or more than once:
the browser forwards Checkout's signed result, and Razorpay sends webhooks.
Refunds can also settle or fail after we create them.

## Decision

1. **Provider interface** returns an outcome: `authorized`, `declined`, or
   `requires_action` with a `next_action` for the client. The sandbox
   authorizes synchronously; Razorpay always requires action. Every
   provider is registered; each payment records which one it uses.
2. **One idempotent entry point** (`applyAttempt`) handles both confirm and
   webhooks. It acts only while the payment is `created`; repeats are no-ops.
   Captures reported by the provider are recorded even for manual-capture
   payments, because the money has moved.
3. **Never trust the client.** Signatures are verified, then the payment's
   status and amount are fetched from Razorpay; the amount must match.
4. **Failed attempts don't fail the payment**: Razorpay orders accept
   retries, so the payment stays `created` with the last `failure_reason`.
5. **Operation order inside one DB transaction:** lock row → post ledger →
   call provider → update state → commit. Local checks (e.g. insufficient
   funds for a refund) run before money moves at the provider; a provider
   error rolls the ledger back.
6. **Webhooks** are HMAC-verified, deduplicated by event ID (Dragonfly fast
   path, Postgres `webhook_events` as truth), and answered 500 on transient
   failure so the provider retries.

## Consequences

- Confirm and webhooks can race freely; tests cover both orders and duplicates.
- A provider call can succeed while our commit fails (crash, DB loss). That
  window is small but real; Phase 5 reconciliation against Razorpay's
  settlement reports detects and repairs it.
- Razorpay has no void API; canceled authorizations are auto-refunded by
  Razorpay after the window configured in the Dashboard.
