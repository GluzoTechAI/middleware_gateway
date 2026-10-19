# Logging

Two logs exist with different jobs.

| Log | Purpose | Where | Format |
| --- | --- | --- | --- |
| Application log | Operational events: startup, HTTP requests, failures | stdout | JSON via `log/slog` |
| Integration log | What happened to each event: every action attempt and outcome | `LOG_DIRECTORY/<YYYY-MM-DD>/` | JSONL, append-only |

## Integration log

```text
storage/logs/
├── 2026-09-12/
│   ├── integration.jsonl   every entry
│   └── error.jsonl         failed entries only
└── 2026-09-13/
    ├── integration.jsonl
    └── error.jsonl
```

Each line is one JSON document:

```json
{"timestamp":"2026-09-12T10:30:01Z","correlation_id":"INT-1001","workflow":"ORDER_SYNC","platform":"easyecom","integration":"vinculum","external_order_id":"9876543","action":"FETCH_ORDER","status":"SUCCESS","attempt":1,"duration_ms":420}
```

| Field | Meaning |
| --- | --- |
| `correlation_id` | Ties the entry to the webhook, job, workflow state and every other entry of the same event |
| `workflow`, `action`, `status`, `attempt`, `duration_ms` | What ran and how it went |
| `platform` / `integration` | Source platform / destination platform |
| `external_order_id` / `order_id` | Source order identifier / destination order identifier once known |
| `error` | Category, message, retryability and external code/message when the attempt failed |
| `details`, `request`, `response` | Sanitised metadata |

Actions are the workflow's action names plus `WEBHOOK_RECEIVED`,
`WORKFLOW_STARTED`, `WORKFLOW_RESUMED`, `WORKFLOW_COMPLETED`,
`WORKFLOW_FAILED` and `WORKFLOW_SKIPPED`.

Days are partitioned in UTC, so retention and search are independent of the
host time zone.

### Append-only

Entries are appended with `O_APPEND`; nothing rewrites a line. A failed
attempt and its later success are two lines:

```json
{"action":"FETCH_INVENTORY","status":"FAILED","attempt":1,"error":{"category":"timeout_error",...}}
{"action":"FETCH_INVENTORY","status":"SUCCESS","attempt":2}
```

Workflow state (`storage/workflows/`) is the mutable, current view; the
integration log is the immutable history. The separation is deliberate.

### Sanitisation

Every entry passes through `intlog.Sanitizer` before it is written:

- Map keys containing `authorization`, `x-api-key`, `access-token`,
  `access_token`, `refresh_token`, `jwt_token`, `api_key`, `password`,
  `secret`, `token`, `cookie`, `card_number`, `cvv` (and more) are replaced
  with `***REDACTED***` at any depth.
- Free text (error messages, details) has `Bearer …`, `password=…`,
  `token: …` fragments masked.
- Personal data under keys such as `email`, `phone`, `contact_num` is masked
  (`a***@example.com`, `******1234`).

The HTTP client's application log excludes headers, bodies and query strings.
Transport errors have query strings and URL user information removed.

### Detailed HTTP capture is disabled by default

Set the following in the server environment (or `.env`), then restart the gateway:

```dotenv
DETAILED_LOG_ENABLED=true
DETAILED_LOG_MAX_BODY_BYTES=262144
```

With `false` (the default), the existing summary entries and timeline remain.
With `true`, new webhook entries include the received request and the returned
acknowledgement, including headers and bodies. A batched webhook appears under
each event's correlation ID; duplicate deliveries remain attached to the original
run. Authenticated rejections are captured too. Requests rejected by authentication
middleware never reach the webhook handler and are not captured here.

Every outbound API attempt through the shared HTTP client produces a separate
`HTTP_CALL` entry, including login/token calls, retries, failures and successful
calls. Each entry includes the API name, operation, method, URL without credentials,
sanitized query parameters, request headers/body, response status/headers/body,
duration and error. Transport failures show the request and error without a response.
`workflow_action` and `workflow_attempt` link calls to the enclosing action;
the entry's `attempt` is the HTTP attempt number. HTTP success describes the
transport status; decoding errors or vendor business rejections are reported by
the subsequent workflow action outcome.

Capture parses JSON and form data before masking secret keys and personal fields.
JSON embedded inside form fields or JSON strings is parsed for masking too.
Authorization, API keys, API owner, location keys, cookies, signatures and tokens
are masked before passing the entry to the recorder. The file recorder sanitizes
again before persistence. No raw payload is written to logs.

The per-body limit is configurable from 1 to 262144 bytes (256 KB). Larger bodies
are omitted as a whole with `body_omitted` and `body_bytes`; partial JSON could
expose credentials. Invalid JSON and unsupported encodings also show an omission
reason. Headers and query parameters each have a 16 KB capture limit. API request
and response processing is unchanged by these capture limits. Detailed entries use
the same UTC day files, admin authentication and retention as summary logs.

On the timeline, expand **Request and response data** to inspect formatted JSON.
**Copy request** and **Copy response** copy the displayed metadata, headers and body
together. Clipboard failures show a message and allow manual text selection.
The JSON timeline endpoint exposes the same `request` and `response` fields.
Enabling capture affects future traffic only; historical payloads cannot be restored.

### Retention

`LOG_RETENTION_DAYS` (default 30) day directories are kept. Every
`LOG_RETENTION_INTERVAL` the retention job:

1. deletes log day directories older than the window (never today's, never
   anything whose name is not a date);
2. deletes idempotency records older than the window;
3. deletes completed workflow state files older than the window.

Active workflow state is never deleted. The job is idempotent and safe to run
at any time.

## Admin log viewer

Mounted under `/admin` only when `ADMIN_LOG_VIEWER_TOKEN` is set. Every
request needs an authenticated browser session or `Authorization: Bearer
<token>` (or `X-Admin-Token`). Open `/admin/login` over HTTPS and enter the
admin token to start an eight-hour Redis-backed session. After sign-in,
`/admin` shows the homepage. Cookie-authenticated POST requests require the
CSRF token included in the page's forms, or the `X-CSRF-Token` header.
Header-authenticated API requests remain compatible. Responses are never cached.

| Route | Purpose |
| --- | --- |
| `GET /admin/login` | Browser sign-in page |
| `POST /admin/login` | Sign in and redirect to `/admin` |
| `GET /admin` | Authenticated homepage |
| `POST /admin/logout` | Revoke the browser session and return to sign-in |
| `GET /admin/logs` | Search page (HTML); add `format=json` for JSON |
| `GET /admin/logs/search` | JSON search |
| `GET /admin/logs/:correlationId` | Chronological timeline with the workflow state (HTML, or JSON with `format=json`) |
| `GET /admin/workflows/:correlationId` | Persisted workflow state |
| `POST /admin/workflows/:correlationId/resume` | Resume a failed run from its failed action (202, runs in the background) |

Search filters: `correlation_id`, `order_id`, `external_order_id`,
`integration`, `platform`, `workflow`, `action`, `status`, `date_from`,
`date_to`, `errors_only`, `limit`. A search without dates covers the last
seven days; a correlation ID lookup covers every day.

The timeline shows the execution flow the way the operations team reads it:

```text
✓ WEBHOOK_RECEIVED
▶ WORKFLOW_STARTED
✓ RESOLVE_INTEGRATION
✓ FETCH_ORDER
✓ MAP_ORDER
✗ UPDATE_DESTINATION_ORDER   attempt 1   external_api_error: unexpected status 503
✓ UPDATE_DESTINATION_ORDER   attempt 2
✓ FETCH_INVENTORY
✓ UPDATE_INVENTORY
– FETCH_TRACKING              skipped: no shipment yet
✓ WORKFLOW_COMPLETED
```

Searches read the JSONL files directly, newest day first, and stop at the
limit. That is adequate for 30 days of a single integration; an index can be
added behind the same reader interface if volume grows.
