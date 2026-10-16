---
name: resolve-blocker
description: Resolve a TODO(VERIFY) marker or a docs/blockers.md item (B1-B19) in the Gluzo integration gateway once BCPL or EasyEcom have confirmed an answer — the sellable stock bucket, the committedQty quantity rule, an unconfirmed endpoint path, the ship* address field names, the shipment status enumeration, a duplicate-order code, a date format or a responseCode convention. Use when an answer arrives from a vendor, when a live API call settles an assumption, or when asked to confirm, settle, unblock, close out or sign off a flagged assumption.
---

# Resolving a flagged assumption

Every `TODO(VERIFY)` in this repo marks a place where the external contract
could not be confirmed and a deliberate assumption was made instead. Resolving
one is the only work left on this project — no code is outstanding, only
answers.

## First: is this actually resolved?

**A marker is resolved only by a named source.** One of:

- a message from BCPL or EasyEcom, quoted in the conversation;
- an observed request/response against their test environment, in this session
  or recorded in the conversation;
- the user stating the answer as fact.

**Never** resolve from plausibility, from a naming convention, from another
vendor's documentation, or from a web search. The marker exists *precisely
because the conventional reading is what is in doubt* — if the obvious answer
were trustworthy there would be no marker.

If the user says "I think it's X" or "it's probably X", that is not a
resolution. Change the assumption and its rationale comment if they want, say
plainly that you have done so, and **leave `TODO(VERIFY)` standing**.

If you cannot name the source, stop and ask for it. Refusing is the correct
outcome far more often than editing.

## Finding the code

Read the `docs/blockers.md` row **first**. Its *Blast radius* column names the
symbol and file.

**Do not grep for the B-number.** The ledger is one-directional: `blockers.md`
names the code site, the code does not name the blocker. Of 25 markers exactly
one carries a B-number (`app/integrations/vinculum/mapper/order.go`, "BCPL open
item B11"). The rest say "BCPL open item 1", "EasyEcom open item 9", or nothing
identifying at all.

So: blockers row → symbol name → `grep` that symbol.

## Check the promised blast radius still holds

`blockers.md` commits to a cost for each item — "one expression", "one
constant", "nine struct tags", "one environment variable — no code". The whole
strategy of building ahead of the answers rests on those being true.

**If your edit would exceed the promised radius, stop and report it.** That
means the isolation has broken, which is a design conversation, not an edit to
push through.

## What each item actually changes

| Item | The change |
| --- | --- |
| **B1** — does `committedQty` rise or fall | The body of `mapper.SellableQuantity` in `app/integrations/vinculum/mapper/inventory.go`. If `committedQty` is already the net figure it becomes `return committed`. One function, one caller, isolated on purpose. |
| **B2** — the sellable bucket | `VINCULUM_SELLABLE_BUCKET` in `.env` and `.env.example`. **No Go change.** `vinculum.NewVendor` already refuses to start when it is blank. |
| **B3** — a separate EasyEcom location for BCPL | **Architectural. Refuse to "resolve" this in code.** A "no" means the JWT-scoping guarantee degrades to a convention and needs an allowlist design. Escalate. |
| **B7** — `current_shipment_status_id` | `EASYECOM_SHIPMENT_STATUS_IDS` config, as `SHIPPED=3,DELIVERED=7`. Not a hardcoded map. |
| **B8** — `companyCarrierId` values | Operator data: `gatewayctl courier add`. No code. |
| **B10, B11** — `responseCode` success value, request date format | One constant each in `app/integrations/vinculum/dto/common.go`. |
| **B14** — the two unused EasyEcom reads | The answer may be "delete them". That is a phase decision, not a marker edit. |
| **B15, B19** — unconfirmed endpoint paths | Path constants in `app/integrations/easyecom/endpoint_inventory.go` and `endpoint_tracking.go`. |
| **B17** — the `ship*` address field names | Nine struct tags in `app/integrations/vinculum/dto/order/create_order.go`. **The only silent-failure item in the project.** Never mark it resolved without a recorded live test order — a wrong name means Vinculum accepts the order and ships it with a field missing. |
| **B18** — the duplicate-order rejection | `VINCULUM_DUPLICATE_ORDER_CODES` config, preferred over the keyword fallback. |

## Edit the marker, not the comment block

The prose above each `TODO(VERIFY)` is rationale that **outlives the question** —
it explains why the assumption was chosen and what the alternative was. Deleting
the whole block destroys the reasoning along with the marker.

Remove the `TODO(VERIFY):` line. Rewrite the surrounding "assumed" or "not
confirmed" wording into "confirmed by «source», «date»". Keep the explanation of
why it matters.

## Update the ledger in all its places

`docs/blockers.md` mentions most items more than once:

1. the row in the §2 table;
2. the dedicated subsection in §1, if it is B1, B2 or B3;
3. the "what to send" list in §5;
4. the ranking in §7.

Then the owning phase note's *Still blocked* / *Unverified* table —
`docs/phases/phase-4.md`, `-5` and `-6` each carry one.

Leaving a resolved item in the "what to send" list is how a team ends up asking
a vendor the same question twice.

## Verify and commit

```
gofmt -l .
go vet ./...
go test ./...
```

State plainly that `-race` was not run and why (no cgo on this machine).

Commit as `fix:` or `feat:`. The body says **what was confirmed, by whom, on
what date, and what would have shipped under the old assumption** — that last
part is what makes the commit worth reading in six months.
