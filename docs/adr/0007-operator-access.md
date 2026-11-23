# ADR 0007: Invite-only operator accounts with admin and read-only roles

- **Status:** Accepted (supersedes the single env-configured operator)
- **Date:** 2026-11-23

## Context

The dashboard controls refunds, payouts and reconciliation. Teams need more
than one operator, some of whom should only look (finance, support), and
access must be revocable instantly and auditable.

## Decision

- **No public sign-up.** Accounts start as invites: an admin (or the
  `operator` CLI for the first admin) creates a one-time link. Only a SHA-256
  of the 256-bit token is stored; links expire after 72 hours, work once, and
  re-inviting revokes the previous link.
- **Two roles.** `admin` can change things; `viewer` (read-only) can see
  everything. Every mutating server action checks the role on the server.
- **Passwords** use bcrypt (cost 12); sign-in takes the same time for unknown
  emails; failed sign-ins are logged.
- **Sessions are re-validated** against the operator record on every request
  and carry a `session_version`, bumped on role change or disable, so changes
  apply immediately.
- **The last active admin is protected**: demotions and disables lock all
  active admin rows first, so concurrent changes cannot remove the last one.
- **Audit trail**: `operator_events` is append-only.
- Operator accounts live in the test-mode API by default
  (`FINCORE_AUTH_API_URL` overrides). Production would typically put SSO
  (OIDC) in front instead; the role model stays the same.

## Consequences

- Invite links are delivered by hand until an email provider is integrated.
- Password reset is an admin re-invite after disabling the old account; a
  self-service reset flow is a natural next step.
