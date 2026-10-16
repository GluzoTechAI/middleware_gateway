# Admin panel delivery

## Phase 1: browser login and homepage

Status: implemented; targeted tests, `go test -short ./...`, and targeted
`go vet` passed. Browser visual verification remains pending because the
browser automation runtime could not initialize in this environment.

Scope: a browser can sign in, land on `/admin`, navigate the existing admin
pages, and sign out. Uses the existing server binary and embedded Go templates.

- `GET /admin/login`: password form for `ADMIN_LOG_VIEWER_TOKEN`.
- `POST /admin/login`: validate the form's CSRF token and admin credential;
  rotate the anonymous session and redirect to `/admin` on success.
- `GET /admin` and `/admin/`: authenticated homepage linking to events,
  failed actions, and stuck workflows. These links use the existing views.
- `POST /admin/logout`: revoke the Redis session and clear its cookie.
- Redis holds anonymous sessions for ten minutes and authenticated sessions
  for eight hours, with absolute expiry. Only opaque random session IDs are
  sent to the browser. Redis keys contain a hash of the ID; values contain a
  CSRF token and a credential fingerprint, never the admin credential.
- Cookies are host-only, Secure, HttpOnly, SameSite=Strict, and use the
  `__Host-` prefix. HTTPS is required, including for local browser testing.
- Every cookie-authenticated mutation, including the existing Resume form,
  requires a CSRF token. API clients retain Bearer and X-Admin-Token access.
- Browser requests for protected pages redirect to login; API requests
  without credentials continue to receive 401. Wrong explicit API credentials
  cannot fall back to a browser session.
- Login is limited to ten POST attempts per client IP per five minutes,
  shared across replicas through Redis. The router currently trusts no
  forwarded client-IP headers, so users behind one reverse proxy share its
  limit. Do not trust arbitrary forwarded headers to bypass this limit.
- Admin-token rotation invalidates authenticated sessions on instances using
  the new configuration. Redis failure denies session authentication with
  503; existing header authentication remains independent of Redis.

Validation: HTTP integration tests cover login/home/logout, cookie settings,
session rotation, shared sessions, expiry, credential rotation, CSRF on login
and existing actions, rate limits, API compatibility, and Redis failure.
Production validation requires opening the deployed HTTPS login page and
confirming navigation and logout against its Redis instance.

## Phase 2: complete workflow investigation

Planned separately; not part of Phase 1:

- Recent events displayed by default and clearer run summaries and filters.
- Queue enqueue, worker pickup, delivery/retry, and outcome visibility.
- Sanitized incoming payloads, mapped vendor requests, and API response
  summaries. The current log schema's request/response fields still need
  instrumentation before they can provide this evidence.
- A single run view joining receipt, routing, actions, errors, retries,
  destination identifiers, and recovery history.
- Related order, stock, and shipment runs linked only where recorded
  identifiers support the relationship; unavailable evidence stays explicit.
- Confirmation and audit history for manual workflow recovery.

The order workflow currently resolves the integration, fetches the order,
maps it, and submits the vendor order. Stock and shipment updates are
separate scheduled workflows.
