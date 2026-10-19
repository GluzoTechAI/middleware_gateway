# Gluzo Integration Gateway — working notes for Claude

A Go service that receives order webhooks from EasyEcom, converts them into
Gluzo's domain model, and synchronises orders, stock and dispatch with a
dropship fulfilment vendor. The vendor side is a role-based abstraction in
`app/vendor`; Vinculum eRetail (BCPL) is the implemented vendor.

## Where the truth lives

| For | Read |
| --- | --- |
| The current plan and its phases | `docs/vinculum-integration-plan.md`, `docs/phases/phase-0.md` … `phase-7.md` |
| Open questions blocking production | `docs/blockers.md` — the ledger, B1–B19 |
| Architecture and decision records | `docs/architecture.md` (ADR-001 … ADR-018) |
| Per-platform contracts | `docs/integrations.md` |
| Running it | `README.md`, then `go run ./cmd/devstack` |

**`implementation.md` is the original Cursor-era brief.** Its conventions
(§6–§35: DTO, mapper, client, endpoint, workflow, logging and error rules) are
still authoritative. Its §36 phase plan and **every reference to Dabur or
Uniware are historical and wrong** — that destination was removed in Phase 0
and replaced by the `app/vendor` role abstraction. Do not take architecture
from it.

**`git-guide.md` §41 is authoritative.** It is addressed to "Cursor Agent";
read that as "you".

## Verification

```
gofmt -l .        # listing only — must print nothing
go vet ./...
go test ./...     # ~20–40s; tests/ boots an embedded PostgreSQL
```

- **Do not use `make check` as a gate.** Its `fmt` target is `gofmt -l -w .`,
  which rewrites the tree — so `check` can never fail on formatting, and it
  dirties the diff you are about to review. `make verify` is the non-mutating
  gate; `make check` is the fix-it convenience. **`make` is not installed on
  this machine** — run the three commands above directly.
- **Never pass `-short`.** It skips the whole `tests/` package, which is where
  the database-backed behaviour is proven.
- **`go test -race` cannot run on this machine** (Windows, no cgo). Never
  report it as passing. It has never run on this codebase — see
  `docs/blockers.md` §4.

## Git

Conventional Commits, lowercase: `feat: … fix: … refactor: … test: … docs: …
chore: …`. One coherent, tested unit of work per commit — not per file, and
never merely because N files changed.

**Stage named paths. Never `git add .` or `git add -A`.** The repo has
untracked build output at the root; a blanket add has nearly committed a 32 MB
binary.

Before committing, work `git-guide.md` §41.11 — the twelve items, in order.
After committing, report in the §41.12 template. Forbidden without an explicit
instruction: `reset --hard`, `clean -fd`, `rebase`, `commit --amend`,
`push --force`.

## Boundaries that must hold

- A vendor's DTOs never leave its own package tree, and no vendor package
  imports another's. Data flows DTO → mapper → domain → mapper → DTO.
- Mappers are pure: no HTTP, no database, no clock. Anything time-dependent is
  passed in.
- Queue types never appear in a workflow action's signature.
- Webhook handlers call no vendor synchronously.
- No credential, and no raw payload body, reaches any log. Explicitly enabled
  detailed HTTP logging may record bounded, parsed and sanitized payloads.

Breaking one of these is a design conversation, not an edit. Stop and explain
the trade-off first.

## Unverified external contracts

25 `TODO(VERIFY)` markers across 16 files record assumptions about BCPL's and
EasyEcom's APIs that could not be confirmed. Every one is tabulated in
`docs/blockers.md` with what it costs to be wrong.

**Never resolve one from plausibility, naming convention or a web search** —
the marker exists precisely because the conventional reading is what is in
doubt. Resolution needs a named source. Use the `resolve-blocker` skill.

**B17 is the only one that fails silently** (Vinculum's `ship*` address field
names): a wrong name means the order is accepted and shipped with a field
missing. Every other unknown fails loudly on the first call.

## This machine

- Windows 10. No Docker, no local PostgreSQL, no local Redis.
- `tests/` starts an embedded PostgreSQL 16; Redis is `miniredis`.
- `go run ./cmd/devstack` runs the entire stack in-process with stub vendors
  and drives one order end to end — the fastest way to see the pipeline work.
  (`make` is not installed here, so use the `go run` form, not `make devstack`.)
- `.gitattributes` forces LF for Go, SQL and YAML.
- The Bash tool mangles backslash escapes inside heredocs. Prefer the Write
  tool for source files, and build patterns containing `\n` or `\t` with
  `chr(92)` rather than writing the escape literally.

## Writing docs in this repo

Sections are titled as **claims**, not labels: "The status enumeration is not
guessed", not "Status mapping". A reader should be able to skim the headings
and learn the decisions.

Phase notes follow one skeleton: `What changed` (file manifest) → narrative
sections, each a decision with its reasoning → `Exit criteria` (checkboxes
lifted from the plan) → `Unverified` → `Follow-ups` → `Next`. Model on
`docs/phases/phase-4.md`.

Record *why*, including the alternative rejected. The code says what it does;
the docs exist to say what it would otherwise be reasonable to do instead.
