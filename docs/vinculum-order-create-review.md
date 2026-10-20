# The rejected create-order request differed from Vinculum's v4 contract

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
