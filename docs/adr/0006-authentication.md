# ADR 0006: Authentication and tenancy

- **Status:** Accepted
- **Date:** 2026-11-23

## Decision

- **Merchants** authenticate with API keys (`fc_test_` + 256 random bits).
  Only a SHA-256 hash is stored: the keys are high-entropy, so a slow
  password hash adds nothing and a fast one allows a single indexed lookup per
  request. Keys are shown once, can be rotated, and revoke immediately.
- The merchant comes **only** from the key, never from the request body;
  other merchants' resources return 404, not 403, to avoid leaking existence.
- Idempotency keys and rate limits are **scoped per merchant**.
- **Admin** endpoints use a separate bearer token (≥ 32 chars, constant-time
  comparison). The dashboard calls the API from the server (a backend-for-
  frontend), so the token never reaches the browser. This replaces the
  originally planned dashboard JWT: there is one operator role today, and
  SSO (OIDC) in front of the dashboard is the natural next step.
- `/v1/payments/{id}/confirm` needs no key: it is called from the customer's
  browser, and only a provider-signed proof — verified, then re-fetched from
  the provider — can move the payment.
- `/metrics` is served on a separate internal port, not the public API.
