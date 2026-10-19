# Restore warehouse based order routing

Prepared 19 October 2026 for the Gluzo middleware maintainers.

## The temporary mode uses the token and configured BCPL location

The existing Logix connection will use integration-based order routing until
our own Logix system can meet the warehouse-routing contract. We retain both
modes so that restoring warehouse matching requires a configuration change
and process restart, rather than deleting the temporary implementation or
reverting unrelated fixes.

Current settings:

```text
ORDER_ROUTING_MODE=integration
VINCULUM_LOCATION=PBW
```

Integration is the default when ORDER_ROUTING_MODE is absent or blank. The
platform API key and integration access token must still pass authentication.
The token's integration ID selects an active EasyEcom-to-Vinculum integration;
the worker also checks that both platforms remain active. The destination is
VINCULUM_LOCATION, shared by eligible integrations in that deployment.
No warehouse route row or webhook location attribute selects the destination.

Missing warehouse IDs are accepted in this mode. Provided warehouse fields
still undergo normal DTO decoding, including rejection of conflicting
warehouse_id and warehouseId values. Order identifiers, customer/address data,
items and SKU mappings must still satisfy the existing order-processing rules.

EASYECOM_LOCATION_KEY is an origin-side location setting. It is separate from
Vinculum's PBW code. Stock and shipment schedules still use active configured
route locations; this order-routing change does not provision those schedules.
Disabled warehouse route rows do not block orders in integration mode. Disable
the integration when its order pipeline must be paused.

## Restore warehouse matching when our Logix payloads are ready

1. Confirm the future Logix parser emits warehouse_id as its routing-key type
   and the correct assigned warehouse ID as its value. The current EasyEcom
   parser accepts warehouse_id or warehouseId. A future source platform needs
   its own adapter; this switch does not implement a new Logix adapter.
   Do not substitute import_warehouse_id for the assigned warehouse.
2. Identify the integration name authenticated by the sender's token. Routes
   belong to that integration; a row under another integration will not match.
   Keep the platform key and access token out of this guide and source control.
3. List and preserve the existing routes, then create any missing active route
   for every warehouse intended for synchronization. Verify the destination
   code and origin location key before using the example below.

```text
go run ./cmd/gatewayctl integration list
go run ./cmd/gatewayctl route list --integration <integration-name>
go run ./cmd/gatewayctl route add --integration <integration-name> --type warehouse_id --value 304788 --destination-ref PBW --origin-ref <bcpl-easyecom-location-key>
```

The warehouse ID 304788 was observed in the BCPL callback. Confirm it against
the future setup instead of copying it automatically. destination_reference
must be the Vinculum orderLocation code. origin_reference is EasyEcom's
location_key for the matching warehouse. If the route already exists, inspect
it rather than issuing a duplicate add. An existing disabled route can be
reactivated after its references have been checked:

```text
go run ./cmd/gatewayctl route set-status --id <route-id> --status active
```

4. Pause the sender, let accepted jobs drain, and review failed or resumable
   workflows before the switch. Events already queued in integration mode
   carry integration_id routing keys. Warehouse mode will not reinterpret
   these keys. Workflows that already resolved a route retain that persisted
   destination when resumed. Do not edit queue messages or state files to
   force a different destination.
5. Set the following value in the environment actually used by every intake
   and worker instance. Rebuild and deploy this change first if the running
   binary does not yet support the setting. Restart or recreate every instance
   with the updated environment before re-enabling the sender.

```text
ORDER_ROUTING_MODE=warehouse
```

Retain VINCULUM_LOCATION for other calls that use the client's default.
Warehouse-mode orders use the matched route's destination_reference; they do
not fall back to that environment setting. There is no schema migration, and
the existing token does not need to be reissued for this mode change.

## Verify both the matching and rejection cases

Before production cutover, run these repository checks:

```text
gofmt -l .
go vet ./...
go test ./...
```

Use new controlled orders for the following staging checks:

- An authenticated order with a configured warehouse resolves the expected
  integration, completes ORDER_SYNC, and reaches Vinculum with orderLocation
  from destination_reference.
- An unconfigured warehouse is SKIPPED at RESOLVE_INTEGRATION. It must not
  silently use PBW from the environment.
- A route under another integration does not match. Disabled routes and
  disabled integrations do not resolve through warehouse matching.
- Missing warehouse_id and warehouseId are rejected at intake in warehouse
  mode. Invalid or revoked credentials still fail authentication.
- A duplicate delivery does not create a second vendor order. Check the
  vendor order number and location as well as the middleware timeline.

Historical SKIPPED workflows do not automatically restart after a mode change.
Simply redelivering an old webhook may return duplicate because its idempotency
claim remains. Use a new controlled order for verification. Recover a historical
order only through a separately reviewed process that checks whether Vinculum
already has it; do not delete claims or rewrite workflow state casually.

## Return to integration mode if warehouse cutover fails

Pause intake and drain or review work again. Restore
ORDER_ROUTING_MODE=integration and confirm VINCULUM_LOCATION=PBW in the BCPL
deployment. Restart every intake and worker instance, verify a new controlled
order, and then resume the sender. Leave warehouse route rows intact so that
the next cutover does not require rebuilding configuration.

The switch changes order routing only. Continue to manage stock and shipment
locations through their existing route configuration in both modes.

## The original implementation remains in the repository

- app/routing/store.go retains the warehouse-based Resolve method and adds
  ResolveIntegration for the temporary mode.
- app/routing/integration.go supplies the configured destination for the
  authenticated integration and enforces the supported platform pair.
- app/config/config.go loads and validates ORDER_ROUTING_MODE.
- cmd/server/main.go selects the matching parser, handler and order resolver.
- app/integrations/easyecom/webhook.go and app/webhook/handler.go accept missing
  warehouses only when integration routing is explicitly enabled by the server.
- docs/routing.md describes both modes. This guide is also delivered as the
  root routing-revert.doc, in Word-compatible RTF format.

Keep the warehouse path and its tests while our own Logix integration is being
built. A Git revert is not required to restore it. Production acceptance still
requires the controlled order checks above; repository tests use stub external
APIs and do not prove live BCPL acceptance.
