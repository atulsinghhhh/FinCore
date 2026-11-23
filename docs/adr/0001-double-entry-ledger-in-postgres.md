# ADR 0001: Double-entry ledger enforced in PostgreSQL

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

FinCore moves money between customers, merchants and the platform. A bug that
creates, loses or double-counts money is the worst failure the system can
have. We need balances that can be audited, explained and recomputed at any
point in time.

## Decision

1. **Double-entry.** Every money movement is a ledger transaction of two or
   more postings whose debits equal credits per currency.
2. **Balances are derived, not stored.** `account_balances` is a cache
   maintained by a trigger in the same DB transaction as the postings. It can
   always be rebuilt from `postings`, and `CheckIntegrity` does exactly that.
3. **Invariants live in the database**, so a bug in application code, a
   manual SQL session or a future service cannot break them:
   - deferred constraint triggers reject unbalanced or empty transactions at COMMIT;
   - `UPDATE`/`DELETE`/`TRUNCATE` on ledger tables raise an error (append-only);
   - a composite foreign key ties each posting's currency to its account's;
   - a `CHECK` constraint prevents overdrafts on accounts that disallow negatives.
4. **Corrections are reversals.** A mistaken transaction is cancelled by a
   new, mirrored transaction linked via `reverses_id` (unique, so at most once).
5. **Money is `bigint` minor units** with an ISO 4217 currency code. Never floats.
6. **Idempotency at the ledger level.** Each transaction carries a unique
   idempotency key plus a hash of the request; a retry returns the original,
   and a different request with the same key is rejected.
7. **Concurrency via ordered row locks.** Balance rows are locked with
   `SELECT … FOR UPDATE ORDER BY account_id` before posting, so concurrent
   transfers serialise per account and opposing transfers cannot deadlock.

## Consequences

- Correctness does not depend on every caller being bug-free.
- Hot accounts (e.g. a single fee account) serialise writes; if that becomes a
  bottleneck we can shard them into sub-accounts or batch fee postings.
- The schema is harder to change casually, which is intended for financial data.
- Tests can prove the invariants directly: raw SQL that violates them must fail.
