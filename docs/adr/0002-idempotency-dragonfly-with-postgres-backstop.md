# ADR 0002: Idempotency in Dragonfly, backstopped by Postgres

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

Clients retry: mobile networks drop, users double-click "Pay", load balancers
time out. A retried payment request must never charge twice, and the client
should get the same answer it would have got the first time.

## Decision

Two layers:

1. **Dragonfly (fast path).** HTTP middleware requires an `Idempotency-Key` on
   every POST. The first request takes a short lock (`SET NX`, 60s); the
   response is then stored for 24h and replayed byte-for-byte with
   `Idempotent-Replayed: true`. A different body or endpoint with the same key
   returns 422; a concurrent duplicate returns 409 with `Retry-After`. 5xx
   responses are not stored, and lock updates use compare-and-set Lua scripts
   so an expired request can never overwrite whoever took over.
2. **Postgres (durable backstop).** Payments, refunds and payouts have
   `UNIQUE (merchant_id, idempotency_key)` plus a stored request hash; ledger
   transactions have their own unique keys derived from the business object
   (`payment:<id>:capture`, `refund:<id>`, ...); state transitions are guarded
   by row locks and a trigger.

If Dragonfly is unavailable, the middleware **fails open** and logs a warning:
the database layer alone still guarantees no duplicate money movement.

## Consequences

- Duplicate requests are cheap (one Dragonfly round trip, no DB work).
- Dragonfly can be flushed or lost without financial impact.
- Without Dragonfly, a duplicate capture returns 409 `invalid_state` rather
  than a replay: correct, but a slightly worse client experience.
