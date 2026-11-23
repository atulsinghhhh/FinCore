# ADR 0005: Settlement reconciliation

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

Every operation posts to the ledger before calling the provider and commits
after, which leaves one gap: the provider succeeds and our commit fails. Also,
a customer can pay after we expired a payment, and processors deduct fees and
taxes we only learn about at settlement.

## Decision

- Import the processor's settlement report (CSV, provider-neutral columns).
  The whole file is processed in one transaction and identified by its
  SHA-256: re-importing is a no-op.
- Match each line by provider ID:
  - **matched** → post `Dr bank (net) / Dr processor_fees / Cr psp_clearing`
    (refunds reverse it), dated at the settlement time;
  - **missing_in_ledger** — the provider moved money we never recorded;
  - **amount_mismatch**, **status_mismatch** — records disagree;
  - **duplicate** — already settled (a partial unique index makes this
    impossible to double-post);
  - **unsupported** — adjustments etc., for manual review.
- Report captures older than 3 days that no settlement has matched.
- Lines are append-only; exceptions are resolved by new ledger entries, never
  by editing history.

## Consequences

- `psp_clearing` trends to zero as settlements arrive; a persistent balance
  there is money the processor owes us (or we failed to record).
- Exceptions surface in the dashboard and in `/v1/admin/stats`.
