---
name: wire-event-workflow
description: Add or change an event type and its workflow in the Gluzo integration gateway — a new ORDER_* or *_SYNC_DUE event constant, a new workflow package under app/workflow, a new scheduled sweep and the queue jobs it publishes, or registering any of these in cmd/server/main.go. Use when adding an event constant, a scheduler.Job, a queue.Job producer, a workflow package, or a workflow registration — including wiring ORDER_CANCELLED.
---

# Adding an event type or a workflow

## The landmine, first

`app/event/event.go`'s `Validate` ends in:

```go
default:
    errs = append(errs, fmt.Errorf("unknown event type %q", e.EventType))
```

The worker calls `event.Validate` on every job and **discards** anything that
fails — logging it and acking it. So a new event constant that is not added to
that `switch` makes the worker silently drop every job of that type, **with the
queue reporting healthy**.

This has already happened once: `STOCK_SYNC_DUE` in Phase 4. Every scheduled
stock sweep would have been discarded before reaching the workflow — a total,
silent failure of the feature. It was caught only because a test asserted the
event the job carried would be *accepted*, not merely that the job was built.

**Adding an event type is two edits, not one:**

1. the `const` block, and
2. a `case` arm in `Validate` stating what that type requires.

Order events (`ORDER_CREATED`, `ORDER_CONFIRMED`, `ORDER_CANCELLED`) require
`ExternalOrderID` **and** a routing key. Scheduled sweeps (`*_SYNC_DUE`)
require a routing key and carry no order id — requiring the key is what stops a
malformed scheduled job from being read as "sweep everything".

## The test that catches it

Required, in `schedule_test.go`. Unmarshal the `queue.Job` payload back into an
`event.Event` and assert it would survive the worker:

```go
var ev event.Event
if err := json.Unmarshal(j.Payload, &ev); err != nil { /* fail */ }
if err := event.Validate(ev); err != nil {
    t.Fatalf("the worker would discard this event: %v", err)
}
```

Asserting only that the job was built is what let the bug through. Copy the
shape from `app/workflow/stocksync/schedule_test.go`.

## File manifest for a scheduled workflow

Model on `app/workflow/stocksync/` and `app/workflow/shipmentsync/`.

**`<name>.go`**
- `Sweep` — the job payload, with `Validate` and `Route() vendor.Route`
- `Name` const (the registry name, e.g. `STOCK_SYNC`)
- action-name consts, in execution order
- result-key consts
- `Policies` + `DefaultPolicies()` — remote calls retry, pure mapping does not
- `Dependencies` + `New(deps) (*workflow.Definition, error)`, rejecting each
  missing dependency by name

**`schedule.go`**
- `Job(...)` or `Jobs(...)` returning `[]scheduler.Job`
- one `queue.Job` per `routing.Location` from `ActiveLocations`
- idempotency key `scheduler:<job>:<unix>:<integrationID>:<vendorRef>`

**Tests** — the workflow against stubs, and the schedule as above.

## Two rules that look like style and are not

**Fan-out lives in `schedule.go`, not in the workflow.** One queue job per
location means one location's vendor outage fails only that location's sweep.
Fanning out inside the workflow makes every location share one failure.

**A job's name is its watermark key.** `scheduler_state` is keyed by job name,
so renaming a job starts a fresh watermark — and a fresh watermark means either
re-reading a period or skipping one. Two jobs that should fail independently
(like `stock-sync` and `stock-sync-full`) must have distinct names for exactly
this reason.

## Registration in `cmd/server/main.go`

Always behind a **role** guard, never merely "some vendor exists":

```go
if _, err := vendors.StockProvider(vinculum.PlatformName); err == nil {
    // build and register
} else {
    logger.Warn("no vendor provides stock: STOCK_SYNC is not available")
}
```

Registering a workflow that no vendor can serve means accepting jobs it can
only fail. Checking the role rather than the registry reports the problem once
at start-up instead of once per event.

Scheduled jobs are registered only when the workflow they feed is registered,
and only when a worker runs in this process — otherwise the queue fills with
work nothing can execute.

## Refuse

- No vendor DTO package imported into `app/workflow/*`. Workflows speak the
  domain model and the `app/vendor` roles.
- No I/O in a mapper — no HTTP, no database, no clock.
- No `queue.Job` in an action signature; queue types stay out of workflow logic.
- If a second vendor is in play, replace the hardcoded `vinculum.PlatformName`
  guard with a loop over `vendors.Platforms()` filtered by role. Do not add a
  second hardcoded branch.

## Verify

```
gofmt -l .
go vet ./...
go test ./...
```

`go run ./cmd/devstack` drives a real order through the whole pipeline
in-process and is the fastest way to see a new workflow actually run.
