# EasyEcom to BCPL staging works with the accepted minimal request

This is the operational runbook and debugging record for the first successful
staging `ORDER_SYNC`. The field audit below preserves why the request changed.
For every operator command, see the [gatewayctl reference](<../gatewayctl commands.md>).

## The first complete staging run succeeded in one attempt

The user supplied a successful gateway timeline on 20 October 2026. It shows:

| Stage | Result | Attempts | Duration |
| --- | --- | --- | --- |
| `RESOLVE_INTEGRATION` | SUCCESS | 1 | 5 ms |
| `FETCH_ORDER` | SUCCESS | 1 | 587 ms |
| `MAP_ORDER` | SUCCESS | 1 | 5 ms |
| `SUBMIT_VENDOR_ORDER` | SUCCESS | 1 | 693 ms |

`WEBHOOK_RECEIVED` at `05:16:53.548 UTC` and `WORKFLOW_COMPLETED` at
`05:16:54.865 UTC` bracket approximately 1.3 seconds. Both outbound operations,
EasyEcom `GetOrderDetails` and Vinculum `CreateOrder`, were successful. This is
user-supplied staging evidence, distinct from our local tests against stubs.

This establishes the order-creation flow. The screenshot does not show recipient
read-back inside Vinculum, duplicate behaviour, stock synchronization, tracking
updates, cash-on-delivery orders, cancellation or production readiness. Those
remain separate acceptance checks; one green order does not close all blockers.

## Each failure belonged to a different boundary

| Symptom | Finding | Correction and lesson |
| --- | --- | --- |
| Only summary rows were visible | Webhook/API bodies and headers were not captured in the original timeline | Added opt-in detailed logging with copy controls, bounded bodies and sanitized headers/contact data. Enable it before reproducing an issue. |
| `MAP_ORDER`: no active mapping for source SKU `142296` | Fetch success does not establish a source-to-vendor SKU mapping | Add or activate an integration-scoped mapping after verifying the destination product. Never guess a vendor SKU or silently pass an unmapped SKU through. |
| `MAP_ORDER`: missing recipient name, address line 1 and phone | The supplied EasyEcom detail response had null contacts while the matching webhook had them | Fill only missing contact/address fields from a webhook whose identity matches the event and fetched order. Preserve current API business data and populated contacts. |
| `Unrecognized field orderNo` | Header fields were at the JSON root and the line array was incorrectly stored in `orderAmount` | Use `{"order":{...}}`, `order.items` for lines and a quoted scalar header total. Compare exact JSON paths, not only field names. |
| Shipping/billing and tenant constants differed from the accepted request | Guessed tags and `SALE` / `PREPAID` / `NEW` did not establish the tenant's contract | Use the v4 field names, distinct billing data, explicit tenant settings and the working Postman request as evidence. Keep actual source values dynamic. |
| Optional `mode` / `taxInclusive` mapping errors | Both fields accept true blanks; reported runtime settings included lowercase `yes`, which the original wire validator did not accept | Normalize these non-secret enums at config loading, retain blank omission and reject unknown settings by variable name before processing orders. Do not turn optional fields into required fields. |
| HTTP 200 with `Generic Error` | The body carried `responseCode=9` and nested `SKU [BPIN00043237] Primary vendor is not MarketPlace type` | `mode=dropship` selected a different processing path. Restore the accepted omission of mode. Surface nested `requestStatus.errorDesc` instead of hiding it behind the outer message. |
| Editing `.env` did not necessarily change a running container | Existing process variables override file values; Compose containers retain their environment across a plain restart | Recreate the gateway after changing its deployed environment and verify the new outgoing request. A local file or Git revision does not prove what a running container uses. |

The earlier mapping-error report did not prove that a truly empty value itself
was invalid. In particular, the exact non-empty mode value at that failure was
not captured. Six later regressions exercise unset, empty, whitespace,
comment-only, plain blank-file and originally commented blank-file values through
configuration, mapping and actual HTTP serialization. All pass and omit both
optional JSON fields. Inline-comment handling by external loaders is covered as
a regression risk, not claimed as the confirmed cause of the Compose incident.

## Keep the same request shape and change only each order's real data

Use the tenant host and the accepted staging settings:

```dotenv
VINCULUM_BASE_URL=https://beautyimpex.vineretail.com
ORDER_ROUTING_MODE=integration
VINCULUM_LOCATION=PBW
VINCULUM_ORDER_TYPE_PREPAID=Prepaid
VINCULUM_PAYMENT_TYPE_PREPAID=COD
VINCULUM_ORDER_STATUS=Confirmed
VINCULUM_ORDER_TIMEZONE=Asia/Kolkata
VINCULUM_ORDER_MODE=
VINCULUM_ORDER_TAX_INCLUSIVE=
```

`paymentType=COD` is the literal accepted for this tenant's prepaid request;
it does not change the source order's payment mode. Cash-on-delivery source
orders need their own confirmed settings. Authentication credentials and the
sellable stock bucket remain independently configured; the block above is not
a complete `.env` and contains no secrets.

The request retains the accepted `order` envelope, `items`, explicit shipping and
billing fields, stable `orderNo` / `uniqueKey`, and `reqType=create`. Dates use
`DD/MM/YYYY HH:mm:ss` in the configured staging timezone. Use actual source totals,
prices, currency, quantities, line identifiers and customer details. Do not copy
the Postman example's fixed amount, date, order ID or personal details into code.
Masked contacts copied from a log are not usable recipient data.

The accepted request omitted `mode` and `taxInclusive`. Omitted mode follows the
documented WMS path; the commercial fact that BCPL fulfils an order does not by
itself justify API `mode=dropship`. Keep the optional fields absent until a
specific tenant requirement is confirmed with a successful request.

## Inspect first, then change only the failing boundary

The commands below are a reference for the operator. They were not executed
against staging while preparing this summary. Run Compose commands on the
deployment host from the directory containing its Compose configuration.

### Inspect the deployment and existing integration

```sh
docker compose ps
docker compose logs --tail=100 gateway
docker compose exec gateway /app/gatewayctl integration list
docker compose exec gateway /app/gatewayctl token list --integration easyecom-vinculum
docker compose exec gateway /app/gatewayctl skumap list --integration easyecom-vinculum
docker compose exec gateway /app/gatewayctl route list --integration easyecom-vinculum
```

The runtime is distroless: invoke `/app/gatewayctl` directly rather than opening
an in-container shell. List existing rows before running creation commands.
Gateway inbound platform keys/tokens, outbound EasyEcom credentials and outbound
Vinculum `ApiOwner`/`ApiKey` credentials serve different purposes.

Orders in integration-routing mode use the authenticated integration and
`VINCULUM_LOCATION`; a warehouse route row is not required for that order path.
Stock/shipment schedules still use configured route locations. If deliberately
using warehouse mode, verify both the warehouse ID and the integration owning
the route. Do not interchange warehouse IDs, EasyEcom location keys and PBW.

For a genuinely missing mapping, replace both placeholders with verified codes
and run only after checking that the row does not already exist:

```sh
docker compose exec gateway /app/gatewayctl skumap add --integration easyecom-vinculum --gluzo-sku SOURCE_SKU --vendor-sku VERIFIED_VENDOR_SKU
```

This writes configuration to the connected database. It does not verify that
the product exists in Vinculum. A disabled existing row is handled with the
documented `skumap set-status` command rather than a duplicate `add`.

### Apply environment changes and inspect a fresh staging order

For request/response capture, set these before creating the diagnostic order:

```dotenv
DETAILED_LOG_ENABLED=true
DETAILED_LOG_MAX_BODY_BYTES=262144
```

Apply changed deployed environment values by recreating the gateway:

```sh
docker compose up -d --no-deps --force-recreate gateway
```

Deploy code through the normal build process first. When building directly from
this checkout, the corresponding command is:

```sh
docker compose up -d --no-deps --build --force-recreate gateway
```

Create a **new staging order**. This debugging session does not migrate old
prepared requests or require resuming old workflows. In the timeline, inspect
the first failed action, expand its request/response data, and check the full
response body even when HTTP is 200. Success requires all four actions and
`WORKFLOW_COMPLETED`, not just `WEBHOOK_RECEIVED` or an HTTP success row.

For an existing correlation ID, this PowerShell example downloads the protected
timeline using an admin token already present in the process environment:

```powershell
curl.exe --fail-with-body -H "Authorization: Bearer $env:ADMIN_LOG_VIEWER_TOKEN" "https://gateway.gluzone.dev/admin/logs/CORRELATION_ID?format=json"
```

Replace `CORRELATION_ID`; do not put credential values in a shared prompt or
committed command. A browser session can use the existing `/admin/login` flow.

### Verify local changes before deployment

Run from the repository root on the development machine:

```powershell
gofmt -l .
go vet ./...
go test ./...
```

The full suite includes embedded-PostgreSQL tests. Do not substitute `-short`.
The repository requires an environment that can start the embedded database.
Race tests are unavailable on this Windows machine because cgo is unavailable.

Focused regression checks and an isolated local smoke test are:

```powershell
go test ./app/integrations/vinculum -run 'TestBlankOptionalSettingsReachVendorWithoutMappingErrors|TestGenericCreateErrorIncludesNestedVendorRejection' -count=1 -v
go test ./app/integrations/easyecom ./app/workflow/ordersync ./app/config
go run ./cmd/devstack -once
```

The devstack uses local stub vendors and bypasses the real `.env`; it is not a
tenant acceptance test. Passing local tests and successful staging evidence
should always be reported separately.

## Reusable prompts keep future investigations evidence-based

**Diagnose a failed order:**

> Inspect ORDER_SYNC for source order ORDER_ID and correlation ID CORRELATION_ID.
> Start at the first failed action. Compare the webhook, fetched domain fields,
> active integration SKU mapping, outgoing request and full response, including
> nested requestStatus.errorDesc. Separate transport success from business
> acceptance. Use only sanitized evidence; propose the smallest correction.
> Prepare staging commands for me to run. Do not deploy or submit an order.

**Compare a working Postman request with the gateway:**

> Treat the supplied successful Postman request as the tenant baseline. Compare
> JSON paths, nesting, types and explicit settings with the failed request.
> Separate expected order/customer value differences from contract differences.
> Keep optional fields absent unless evidence requires them. Do not hardcode the
> sample's IDs, contacts, amounts or dates. Explain confirmed causes separately
> from hypotheses and provide a bounded regression check.

**Plan a cleanup without losing coverage:**

> Inventory tests, fixtures and docs before deleting anything. Map each test to
> the behaviour it protects and search references to each proposed removal.
> Identify exact duplication and stale wording. Preserve regressions for blank
> config, SKU mapping, same-order contact fallback, v4 request shape, nested errors,
> idempotency and supported recovery. Propose consolidation and link updates;
> do not delete files or runtime data until I approve the named list.

**Discuss a future integration before setup:**

> Help me set up SOURCE to DESTINATION in staging. First determine whether this
> is another account on supported adapters or a new platform requiring code.
> Inspect account credential scoping, worker/queue isolation, event support,
> minimal successful API examples, routing and mappings. Ask only for missing
> decisions. Prepare a setup plan and commands for me to execute. Do not create
> credentials, edit runtime settings, implement adapters, deploy or submit orders.

Current `buildWorker` creates one environment-configured EasyEcom client and one
Vinculum client per process. An additional integration database row does not
automatically provide a separate outbound credential set. Review account scoping
and worker/queue isolation before planning an additional account. A new platform
also needs supported adapters and registration; CLI row creation alone cannot
implement it. The future helper remains a discussion proposal, not an installed
agent or a new pipeline.

## The historical field audit explains the corrections

Reviewed on 19 October 2026 against the rendered official
[v4 create-order documentation](https://erp.vineretail.com/swagger/#/Orders/post_RestWS_api_eretail_v4_order_create),
including its parameter table, editable JSON example and response examples.
Vinculum's [ERP integration guide](https://vinculumhelpdesk.freshdesk.com/support/solutions/articles/9000204521-vin-eretail-erp-integration-guide)
links to the same Swagger documentation for order creation.

The supplied live response for order `652487720` is HTTP 200 with
`responseCode=1009` and `Unrecognized field orderNo`. That is an application
rejection. This work makes no authenticated API calls and does not submit an
order. The initial documentation review did not establish tenant acceptance;
the user's later successful staging test is recorded below. The review tables
describe the rejected pre-fix request.

## The user reported successful BCPL creation on 20 October 2026

The user supplied the accepted **request** for source order `652487720` and stated
that it created an order at the BCPL location. The request has an outer `order`,
`items`, quoted numeric values, and these confirmed staging settings:

| Attribute | Accepted value / decision |
| --- | --- |
| `orderLocation` | `PBW` |
| `orderType` for prepaid source orders | `Prepaid` |
| `paymentType` for prepaid source orders | `COD` (preserve the tenant's accepted literal) |
| Initial `status` | `Confirmed` |
| `uniqueKey` | The same source order identifier as `orderNo` |
| Date representation | `DD/MM/YYYY HH:mm:ss` |
| Staging timestamp timezone | `Asia/Kolkata`, explicitly confirmed by the user in a follow-up |
| `orderAmount` representation | Quoted monetary total, accepted as `"100"` in this test |

The local `.env` and checked-in `.env.example` now carry these staging settings.
Configuration remains explicit: no global defaults for other deployments are
inferred, and cash-on-delivery source-order settings remain blank until tested.
The optional `uniqueKey` is now emitted by the mapper for each new order and stays
stable across retries; no existing workflow is migrated.

The supplied request uses a header total of `100` and an item price of `1049`.
Acceptance establishes the request shape, not the commercial correctness of those
test amounts. The gateway sends each new source order's actual total, line prices,
items, contacts and date; it does not copy the sample's values or recalculate a
header total from incomplete line data. Customer contact details are omitted
from this evidence note and sanitized in regression tests.

The actual success-response body was not supplied. Creation is user-confirmed;
response-code conventions (B10), duplicate behaviour (B18) and address read-back
(B17) remain separate verification items. A successful create does not itself
prove that every shipping and billing attribute persisted.

The following review tables describe the earlier rejected request and explain
the implementation choices; the staging confirmation above supersedes their
previously open prepaid-master and amount-representation questions.

## The later rejection identified an incompatible dropship vendor

On 20 October 2026 the user supplied the failed request and complete response for
order `652797307`. The outer message was `Generic Error` (`responseCode=9`), while
`requestStatus.errorDesc` said `SKU [BPIN00043237] Primary vendor is not MarketPlace
type`. The request added item `mode=dropship` and `taxInclusive=Yes`, absent from
the previously accepted request. The rendered official v4 response documentation
distinguishes WMS creation (mode omitted, empty or `wms`) from dropship creation.

The response identifies the primary vendor configuration for the dropship path;
it does not identify billing, amount precision or SKU translation as the cause.
Restore both optional settings to blank to reproduce the accepted request shape.
BCPL must confirm its vendor configuration before enabling API dropship mode;
`taxInclusive` also remains omitted until the source price basis is established.
The local `.env` and template now leave both blank. No tenant master data is
changed, and no live create call is made by this correction.

The gateway now includes nested `errorDesc` in nonzero-envelope errors while
retaining the outer response code/message. Regression coverage reproduces the
reported response and confirms that unknown optional settings fail during config
loading, case variations are normalized, and absent fields remain omitted.

## The missing outer order object explains the first field rejection

The official v4 JSON example has this structure:

```text
root
  order
    orderNo and other header fields
    orderAmount: monetary total in the example
    items: array of order lines
    paymentItems: optional payment records
```

The gateway sends the header fields directly at the root and stores order lines
in `orderAmount`. `orderNo` is a documented required field inside `order`;
renaming it or switching API versions is not the correction indicated by the
published v4 example. The observed error is consistent with a root object that
does not recognize `orderNo` at that level.

## The method, JSON encoding and credential headers already match v4

| Attribute | Documented v4 request | Current request |
| --- | --- | --- |
| Method | `POST` | Matches. |
| Path | `/RestWS/api/eretail/v4/order/create` | Matches. |
| Body encoding | `application/json` | Matches; use a JSON object, not form fields. |
| Credentials | Required `ApiOwner` and `ApiKey` headers | Matches; header names are case-insensitive. |
| Host | Use the tenant's assigned host | Supplied host is `https://beautyimpex.vineretail.com`; no alternative host was tested. |

The SKU Detail endpoint uses a different parameter convention. Its form-field
authentication must not be copied to this v4 create-order endpoint.

## Required header fields belong inside order

| JSON path | Documented requirement | Current value or gap | Finding |
| --- | --- | --- | --- |
| `order.orderLocation` | Required string, up to 3 characters | `PBW`, currently at root | Shape changes. The parameter description calls this a channel/source code; confirm `PBW` is valid here for this tenant. A physical warehouse code is not sufficient evidence. |
| `order.orderNo` | Required external order identifier, up to 50 characters | `652487720`, currently at root | Preserve this identifier under `order`. |
| `order.orderType` | Required, up to 200 characters; examples `COD` / `Prepaid`, master `ODRTYPE` | `SALE` | Current constant is unsupported by the documented examples; obtain the tenant's configured value. |
| `order.paymentType` | Required, up to 50 characters; examples describe payment methods | `PREPAID` | Do not equate order payment status with payment method. Confirm the configured payment-method value. |
| `order.status` | Required, up to 50 characters; examples `Pending` / `Confirmed`, master `ODRSTS` | `NEW` | Confirm the tenant's accepted initial status. |
| `order.orderDate` | Required datetime; example uses day/month/year and time | `2026-10-15 10:00:00` | Use the documented example's `DD/MM/YYYY HH:mm:ss` representation. Confirm the create endpoint's timezone before converting the original timestamp. |
| `order.orderCurrency` | Required string, up to 10 characters | `INR` | Field/value are consistent with this INR order. Nest under `order`. |
| `order.orderAmount` | Required; description says order monetary total | Currently contains the line-item array | Use the actual order total, and move lines to `order.items`. The documentation's type conflict is recorded below. |

The current formatter calls `UTC()` before using a year-month-day layout. The
EasyEcom mapper interprets timestamps without a zone as India time. Changing the
date's text format alone does not settle whether the 5-hour-30-minute shift is
correct for order creation.

## Shipping field names must match exactly

The parameter table makes the following fields conditional: when a customer code
is supplied they become optional. This request does not supply `customerCode`,
so include the explicit customer and address data for the documented flow.

| Current field | Documented path | Requirement / limit |
| --- | --- | --- |
| `shipTo` | `order.customerName` | Customer name; conditional, up to 100 characters. |
| `shipAddress1` | `order.shipAddress1` | Primary street address; conditional, up to 1000 characters. |
| `shipAddress2` | `order.shipAddress2` | Optional second address line, up to 200 characters. |
| `shipCity` | `order.shipCity` | Conditional, up to 40 characters. |
| `shipState` | `order.shipState` | Conditional, up to 50 characters. |
| `shipCountry` | `order.shipCountry` | Conditional, up to 50 characters. |
| `shipPincode` | `order.shipPinCode` | Capital `C` in `Code`; conditional, up to 50 characters. |
| `shipMobile` | `order.shipPhone1` | Conditional primary phone, up to 20 characters. |
| `shipEmail` | `order.shipEmail1` | Conditional primary email, up to 255 characters. |

The email and phone in the supplied integration log are masked display values.
Use the original domain-order values when sending; a copied masked log is not a
usable recipient contact record.

## Billing fields are missing from the current request

Without `customerCode`, the table also marks these billing fields conditional:

| Documented path | Meaning | Maximum length |
| --- | --- | --- |
| `order.billName` | Billing recipient | 200 |
| `order.billAddress1` | Primary billing street | 1000 |
| `order.billCity` | Billing city | 40 |
| `order.billState` | Billing state | 50 |
| `order.billCountry` | Billing country | 50 |
| `order.billPinCode` | Billing postal code | 50 |
| `order.billPhone1` | Billing contact phone | 20 |
| `order.billEmail1` | Billing email | 255 |

`billAddress2` is optional, with a 200-character limit. Use the actual billing
address. The current EasyEcom domain mapper shares one address between shipping
and billing; confirm that this is correct for the order before reusing it.

## Order lines belong in items, with these mandatory attributes

| Documented path | Requirement | Review of the supplied line |
| --- | --- | --- |
| `order.items[].lineno` | Required text identifier, up to 255 characters | Use `"1"` to match the documented type; current request sends a number. |
| `order.items[].sku` | Required master SKU code, up to 50 characters | `3562700373145` fits the width; existence, activity and product identity still need tenant verification. |
| `order.items[].orderQty` | Required decimal quantity | Quantity `1`; example represents decimal fields as strings. |
| `order.items[].unitPrice` | Optional decimal unit price | `1049` is the supplied value; verify its tax and shipping basis. |
| `order.items[].mrp` | Optional actual MRP | Current mapper copies unit price rather than reading source MRP. Do not present that as verified MRP; omit until mapped correctly or confirmed. |
| `order.items[].taxPercentage` | Optional text value, up to 50 characters | Use `"18"` if applicable to this line. Current request sends a number. |
| `order.items[].udf1` | Optional text, up to 50 characters | Suborder reference `984352024` fits; preserve if this is the intended trace field. |

`discountAmt` and `vendor` are documented optional line fields. Neither is present
in this supplied request. If used, supply the actual discount and a configured
vendor master code; `vendor` is not a free-text company name.

Other conditional line fields apply only when their associated feature is used,
for example `bundlePrice` and `bundleQty` when `bundleSku` is supplied. The
submitted line does not use that feature.

## Tenant values and documentation inconsistencies remain open

1. Confirm the channel/source role of `orderLocation=PBW`. The docs separately
   describe `pickupLocation` as a routing location and line `locationCode` as a
   location/external-location code. Do not substitute one for another without
   confirming the intended tenant configuration.
2. Obtain configured order type (`ODRTYPE`), initial status (`ODRSTS`) and payment
   method. Documentation examples are not a complete tenant-specific enumeration.
3. Confirm order-creation timezone. The example establishes a date representation,
   not an explicit timezone policy for this endpoint.
4. The `orderAmount` table labels it an array, but its description calls it a total
   and the JSON example sends a decimal string. The example separately provides
   `items`. The implementation follows that example; the user-reported successful
   BCPL request on 20 October 2026 confirms acceptance of a decimal string.
5. Use the actual header total, unit price, tax basis, discounts and shipping
   charges. Do not derive the total by blindly summing this log's unit prices, or
   invent an MRP. Confirm tax-inclusive versus exclusive pricing before supplying
   the optional `taxInclusive` field.
6. An application's commercial dropship model does not itself establish whether
   Vinculum wants line `mode="dropship"` or its WMS processing flow. Confirm the
   mode/vendor configuration with BCPL. The draft omits this optional field.

## Successful response handling also needs correction

The published response examples return a `requestStatus` object. Check its
`status`, `errorCode` and `errorDesc` alongside `responseCode`. The returned
`outputKey` is the generated order identifier for WMS examples; dropship examples
can supply `dsOrderNo` with an empty `outputKey`.

The current DTO searches for a top-level `orderNo` or an entry in `response[]`,
then the adapter falls back to the identifier it sent. That does not parse the
documented create-order acknowledgement. Distinguish the source order number,
the generated WMS identifier and any dropship identifiers when correcting this.

## The draft is a review template, not a verified live request

See [vinculum-order-create-draft.json](vinculum-order-create-draft.json). Replace
every `CONFIRM_...` and `ACTUAL_...` value before any manual test. It uses the
documented outer structure, address names and item collection, retains the
supplied source order/line identifiers, and omits optional MRP and processing
mode rather than guessing them. It deliberately does not supply invented tenant
codes, totals, date/time policy or unmasked recipient contacts.

## The implementation corrects new orders and requires tenant verification

Implemented on 19 October 2026 after approval of the broader correction:

- Serialize the outer `order` object, use scalar header total and `items` lines,
  use documented contact names, and validate required fields and documented widths.
  Since 20 October, also emit the source order ID as `uniqueKey`, matching the
  user-reported successful request.
- Keep original source totals, prices, currency, line identifiers and distinct
  billing addresses; omit unprovided MRP. Matching-webhook fallback fills missing
  contact fields without replacing populated API data.
- Configure order types, payment methods, initial status and creation timezone;
  optional line mode and tax-inclusive setting are also configuration. There are
  no production defaults for tenant master values or timezone. Only the order's
  own prepaid/COD settings are required for that order.
- Read `requestStatus` and require a vendor-generated identifier on creation;
  retain the existing explicit duplicate-rejection handling (B18 remains open).

The staging validation path is to create a new order. Previously received
workflows will not be resumed or migrated as part of this correction.

The public [Sales guide](https://docs.vineretail.com/eretail-sales-section/)
states that order types are categories created in the customer's database, with
examples including COD and Prepaid. Its Sales > Order Create/Edit page exposes
Order Type and Payment Detail. These examples cannot confirm Beauty Impex's
configured values. The user-reported BCPL test now establishes
`Prepaid` / `COD` / `Confirmed` for prepaid source orders, and the user explicitly
selected `Asia/Kolkata` for staging. Generic test-fixture values such as `Credit
Card` and `Cash` do not establish tenant payment settings for other order types.

Before staging verification:

1. Use the confirmed prepaid settings above for staging. Before testing a
   cash-on-delivery source order, obtain and configure its own accepted order
   type and payment value. Do not infer those values from this prepaid example.
2. Populate the new variables listed in [.env.example](../.env.example) and
   [deployment guidance](deployment.md), then restart the worker after deployment.
3. Create a new staging order to trigger a new webhook and workflow using the
   corrected request contract.
4. For the first authorized tenant test, inspect the detailed outgoing request
   and acknowledgement. Verify order number, channel, all shipping/billing fields,
   totals, item quantities and generated WMS/dropship identifier in Vinculum.
5. Capture evidence of repeat submission behaviour before closing B18, and verify
   actual persisted addresses before closing B17. Capture the success response
   to verify B10 independently. Other deployments must select their own timezone.

Local regression coverage exercises exact HTTP shape, field/date validation,
acknowledgement failures and distinct billing for new orders. A separate regression
loads `.env.example` through the real configuration parser, prepares a prepaid
order and submits it to a local HTTP fixture. It verifies the accepted settings,
PBW, stable `uniqueKey`, Asia/Kolkata formatting and unchanged source amounts.
Local tests do not add live evidence beyond the user's reported creation.
