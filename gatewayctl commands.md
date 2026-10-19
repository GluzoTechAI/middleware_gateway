# gatewayctl commands

Draft reference checked against `cmd/gatewayctl/main.go` on 19 October 2026.
Includes all 23 operational commands and help. Examples use the
`easyecom-vinculum` integration. Replace uppercase placeholders with your own
values before running a command.

## Commands operate directly on the configured database

Run from the repository root. `gatewayctl` loads `.env` from the current working
directory and requires `DATABASE_URL` for every operational command. It does not
need Redis, an admin login, or a running gateway HTTP server. Use the same database
as the gateway you intend to manage.

Variables already present in the process environment override values in `.env`.
To select another env file in PowerShell:

```powershell
$env:ENV_FILE = "C:\path\to\gateway.env"
go run ./cmd/gatewayctl help
```

`ENV_FILE` must be set before launching the command. Reset this temporary override
when finished:

```powershell
Remove-Item Env:ENV_FILE
```

Commands use a 60-second operation context. Success exits with code `0`; errors
exit with code `1`. Flags follow the command and subcommand. Quote values that
contain spaces.

## The same commands can run locally or inside Docker

All examples below use the source runner:

```text
go run ./cmd/gatewayctl COMMAND [FLAGS]
```

Alternatively, build a Windows executable once:

```powershell
go build -o gatewayctl.exe ./cmd/gatewayctl
.\gatewayctl.exe help
```

For the repository's running Compose `gateway` service:

```sh
docker compose exec gateway /app/gatewayctl help
docker compose exec gateway /app/gatewayctl skumap list --integration easyecom-vinculum
```

For a running container managed outside Compose:

```sh
docker exec CONTAINER_NAME /app/gatewayctl help
```

Replace `go run ./cmd/gatewayctl` in any example with the appropriate executable
or Docker prefix. Docker commands use the container's database environment. The
runtime image contains `/app/gatewayctl` and has no shell; invoke the executable
directly. These Docker prefixes follow the repository's Dockerfile and Compose
configuration; verify the deployed container or service name on your server.

## Help lists the available commands

```powershell
go run ./cmd/gatewayctl help
go run ./cmd/gatewayctl --help
go run ./cmd/gatewayctl -h
```

These are equivalent top-level help commands. Use them for the complete usage
listing; a subcommand's `--help` currently exits as a usage error.

## Migrations prepare or update the database schema

### `migrate`

```powershell
go run ./cmd/gatewayctl migrate
```

Applies pending embedded database migrations. Already-applied migrations are
retained. Success prints `migrations up to date`. The server also applies pending
migrations during startup. There is no CLI rollback command.

## Platform commands manage source and destination systems

A platform represents a system such as EasyEcom or Vinculum. Platform names are
used by integration creation; platform UUIDs are shown by `platform list`.

### `platform create`

Syntax: `platform create --name NAME [--type source|destination|both]`

```powershell
go run ./cmd/gatewayctl platform create --name easyecom --type source
go run ./cmd/gatewayctl platform create --name vinculum --type destination
```

`--name` is required. `--type` defaults to `source`. Source and `both` platforms
receive a generated API key for inbound `X-API-Key` authentication. A
destination-only platform is created without an inbound API key. Generated keys
are displayed once; save them immediately.

### `platform rotate-key`

Syntax: `platform rotate-key --name NAME`

```powershell
go run ./cmd/gatewayctl platform rotate-key --name easyecom
```

Replaces the inbound platform API key. The previous key stops working, and the
new key is displayed once. Update the webhook sender with the new key. This
does not rotate the external EasyEcom account key or Vinculum credentials.

### `platform set-status`

Syntax: `platform set-status --name NAME --status active|disabled`

```powershell
go run ./cmd/gatewayctl platform set-status --name easyecom --status disabled
go run ./cmd/gatewayctl platform set-status --name easyecom --status active
```

Disables or reactivates the platform. Disabling it affects integrations that
depend on that platform; it does not delete their configuration.

### `platform list`

```powershell
go run ./cmd/gatewayctl platform list
```

Columns: `ID`, `NAME`, `TYPE`, `STATUS`, `CREATED`. API keys are not returned.

## Integration commands manage each source-to-destination pipeline

### `integration create`

Syntax: `integration create --name NAME --source PLATFORM --destination PLATFORM`

```powershell
go run ./cmd/gatewayctl integration create --name easyecom-vinculum --source easyecom --destination vinculum
```

All three flags are required. The source and destination platforms must already
exist. This creates the pipeline record; tokens, warehouse routes, SKU mappings
and courier mappings are configured separately.

### `integration set-status`

Syntax: `integration set-status --name NAME --status active|disabled`

```powershell
go run ./cmd/gatewayctl integration set-status --name easyecom-vinculum --status disabled
go run ./cmd/gatewayctl integration set-status --name easyecom-vinculum --status active
```

Disables or reactivates the integration without deleting its configuration.
Disabling an integration blocks its credentials from admitting new webhooks.
It is not a cancellation command for work already queued or running.

### `integration list`

```powershell
go run ./cmd/gatewayctl integration list
```

Columns: `ID`, `NAME`, `SOURCE_PLATFORM_ID`, `DESTINATION_PLATFORM_ID`, `STATUS`,
`CREATED`. The `ID` is the integration UUID shown in workflow routing details.

## Token commands manage inbound integration credentials

These tokens authenticate webhooks to this gateway. They are separate from
EasyEcom JWTs, external vendor credentials and `ADMIN_LOG_VIEWER_TOKEN`.

### `token issue`

Syntax: `token issue --integration NAME --name LABEL [--ttl DURATION]`

```powershell
go run ./cmd/gatewayctl token issue --integration easyecom-vinculum --name "EasyEcom webhook" --ttl 720h
```

`--integration` and `--name` are required. `--ttl` defaults to `0`, meaning no
expiry. Use Go duration units, for example `24h` or `720h` (30 days); `30d` is not
supported. To issue without expiry:

```powershell
go run ./cmd/gatewayctl token issue --integration easyecom-vinculum --name "EasyEcom webhook"
```

The plaintext token is displayed once. Use it as `Authorization: Bearer TOKEN`
on webhook requests. Store the token and its UUID; list commands cannot recover
the plaintext.

### `token revoke`

Syntax: `token revoke --id TOKEN_ID`

```powershell
go run ./cmd/gatewayctl token revoke --id "TOKEN_UUID"
```

Use the token row's UUID from `token list`, not the plaintext token or integration
UUID. The revoked token no longer authenticates new requests. There is no
unrevoke command; issue a replacement token if needed.

### `token list`

Syntax: `token list --integration NAME`

```powershell
go run ./cmd/gatewayctl token list --integration easyecom-vinculum
```

Columns: `ID`, `NAME`, `EXPIRES`, `REVOKED`, `LAST_USED`, `CREATED`. A `-` means
the corresponding timestamp is absent. For `EXPIRES`, `-` means no expiry;
for `REVOKED`, it means not revoked. Secret token values are not returned.

## Route commands configure warehouse matching and scheduled locations

The server defaults to `ORDER_ROUTING_MODE=integration`. In that mode, incoming
orders use their authenticated integration and `VINCULUM_LOCATION`; warehouse
route rows are ignored for order routing. Disable the integration to block new
webhooks for that pipeline.

With `ORDER_ROUTING_MODE=warehouse`, incoming orders require an active matching
warehouse route. Stock and shipment schedules still enumerate configured route
locations in either mode. See [routing.md](docs/routing.md).

### `route add`

Syntax: `route add --integration NAME [--type TYPE] --value VALUE [--destination-ref REF] [--origin-ref REF]`

```powershell
go run ./cmd/gatewayctl route add --integration easyecom-vinculum --type warehouse_id --value "EASYECOM_WAREHOUSE_ID" --destination-ref "VINCULUM_LOCATION_CODE" --origin-ref "EASYECOM_LOCATION_KEY"
```

| Flag | Meaning |
| --- | --- |
| `--integration` | Required integration name. |
| `--type` | Defaults to `warehouse_id`. Must match the key emitted by the relevant workflow/parser. |
| `--value` | Required routing value, such as an EasyEcom warehouse ID. |
| `--destination-ref` | Optional vendor location reference; for Vinculum, the confirmed `orderLocation` code. Supply it for scheduled vendor-location work. |
| `--origin-ref` | Optional origin location reference; for EasyEcom, the `location_key` used for location-scoped calls. Empty uses the configured process default where supported. |

The two references identify locations in different systems. Do not interchange
an EasyEcom numeric warehouse ID, an EasyEcom location key and a Vinculum location
code. Other route types do not automatically add parser or workflow support.

### `route set-status`

Syntax: `route set-status --id ROUTE_ID --status active|disabled`

```powershell
go run ./cmd/gatewayctl route set-status --id "ROUTE_UUID" --status disabled
go run ./cmd/gatewayctl route set-status --id "ROUTE_UUID" --status active
```

Use the route UUID from `route list`. This affects consumers of route rows;
it does not pause incoming orders in integration routing mode.

### `route remove`

Syntax: `route remove --id ROUTE_ID`

```powershell
go run ./cmd/gatewayctl route remove --id "ROUTE_UUID"
```

Deletes the route row. To pause it while preserving its configuration, use
`route set-status --status disabled` instead.

### `route list`

Syntax: `route list --integration NAME`

```powershell
go run ./cmd/gatewayctl route list --integration easyecom-vinculum
```

Columns: `ID`, `TYPE`, `VALUE`, `VENDOR_REF`, `ORIGIN_REF`, `STATUS`, `CREATED`.
Includes disabled routes. `ORIGIN_REF` can contain an EasyEcom location key;
treat that output as private configuration.

## SKU commands translate EasyEcom SKUs into Vinculum item codes

`gluzo_sku` means the incoming SKU used by EasyEcom/the storefront. `vendor_sku`
means the outgoing item code used by Vinculum. Mappings belong to one integration;
both SKU directions are unique within that integration. Lookups ignore case and
surrounding spaces. An explicit active mapping is required even when both codes
are identical.

### `skumap add`

Syntax: `skumap add --integration NAME --gluzo-sku SKU --vendor-sku SKU [--safety-buffer N]`

```powershell
go run ./cmd/gatewayctl skumap add --integration easyecom-vinculum --gluzo-sku "142296" --vendor-sku "CONFIRMED_VINCULUM_SKU" --safety-buffer 0
```

The integration and both SKU flags are required. Confirm the Vinculum product
code before replacing the placeholder. If Vinculum has confirmed that the same
product uses `142296`, the vendor value can also be `142296`.

`--safety-buffer` defaults to `0` and must be nonnegative. It withholds units from
stock published to the origin; it does not reduce an order's quantity. A new
mapping is active. `add` creates a row; it does not update an existing mapping.

### `skumap set-status`

Syntax: `skumap set-status --id MAPPING_ID --status active|disabled`

```powershell
go run ./cmd/gatewayctl skumap set-status --id "SKU_MAPPING_UUID" --status disabled
go run ./cmd/gatewayctl skumap set-status --id "SKU_MAPPING_UUID" --status active
```

Use the mapping UUID from `skumap list`. Disabled mappings are excluded from
active SKU lookups. An order containing that SKU can therefore fail `MAP_ORDER`.

### `skumap remove`

Syntax: `skumap remove --id MAPPING_ID`

```powershell
go run ./cmd/gatewayctl skumap remove --id "SKU_MAPPING_UUID"
```

Deletes the mapping row. Orders still needing this mapping can fail until an
active replacement is configured. There is no CLI command to edit SKU codes or
the safety buffer on an existing row; changing them through this CLI requires
removal and recreation. Coordinate that replacement before deleting a live mapping.

### `skumap list`

Syntax: `skumap list --integration NAME`

```powershell
go run ./cmd/gatewayctl skumap list --integration easyecom-vinculum
```

Columns: `ID`, `GLUZO_SKU`, `VENDOR_SKU`, `BUFFER`, `STATUS`, `CREATED`. Includes
disabled mappings. For the reported `142296` failure, look for `GLUZO_SKU=142296`,
the confirmed `VENDOR_SKU`, and `STATUS=active` under this exact integration.

## Courier commands translate vendor carrier names into origin carrier IDs

These mappings are used when sending shipment information back to the origin.
Confirm the transporter string reported by Vinculum and the carrier identifier
configured in the relevant EasyEcom account.

### `courier add`

Syntax: `courier add --integration NAME --transporter NAME --carrier-id ID [--name NAME]`

```powershell
go run ./cmd/gatewayctl courier add --integration easyecom-vinculum --transporter "VINCULUM_TRANSPORTER_NAME" --carrier-id "EASYECOM_CARRIER_ID" --name "EASYECOM_CARRIER_NAME"
```

The integration, transporter and carrier ID are required. `--name` is the
optional name presented to the origin when it differs from the vendor's name;
when omitted, the transporter name is used. The carrier ID is assigned by the
origin account; the command does not register a carrier in EasyEcom. New mappings
are active.

### `courier set-status`

Syntax: `courier set-status --id MAPPING_ID --status active|disabled`

```powershell
go run ./cmd/gatewayctl courier set-status --id "COURIER_MAPPING_UUID" --status disabled
go run ./cmd/gatewayctl courier set-status --id "COURIER_MAPPING_UUID" --status active
```

Use the courier mapping UUID from `courier list`. A disabled mapping is excluded
from active carrier lookups.

### `courier remove`

Syntax: `courier remove --id MAPPING_ID`

```powershell
go run ./cmd/gatewayctl courier remove --id "COURIER_MAPPING_UUID"
```

Deletes the courier mapping. There is no CLI edit command for changing the
transporter, carrier ID or presented name; replacement requires removal and
recreation through this CLI.

### `courier list`

Syntax: `courier list --integration NAME`

```powershell
go run ./cmd/gatewayctl courier list --integration easyecom-vinculum
```

Columns: `ID`, `TRANSPORTER`, `CARRIER_ID`, `PRESENTED_AS`, `STATUS`, `CREATED`.
Includes disabled mappings.

## A new pipeline is provisioned in dependency order

For an empty environment, run `migrate`, create the source and destination
platforms, create the integration, then issue its webhook token. Configure the
confirmed SKU mappings and courier mappings. Add warehouse routes when needed
for warehouse-mode orders or scheduled location work. Existing environments
should be inspected with the list commands before creating records.

Platform API keys, integration access tokens, external account credentials and
the admin login token are separate values. Creating or rotating one does not
configure the others.

## Fixing a mapping does not itself resume a failed workflow

For the reported `MAP_ORDER` failure:

1. Run `skumap list --integration easyecom-vinculum`.
2. Verify the actual Vinculum product code, then add or activate the correct mapping.
3. Run the list command again to confirm the incoming SKU, vendor SKU and active status.
4. Check that the order's required customer and address fields are available.
5. Open its admin timeline and select **Resume from MAP_ORDER**.

There is no `gatewayctl resume`, log-search, external SKU-verification or
detailed-logging toggle command. Resume and log inspection are admin functions.
Detailed capture is configured through `DETAILED_LOG_ENABLED` and requires a
server restart; see [logging.md](docs/logging.md).

## The command source is the reference for this draft

- [CLI command implementation](cmd/gatewayctl/main.go)
- [Authentication and credential model](docs/authentication.md)
- [Order routing modes and route behavior](docs/routing.md)
- [Logging and the admin timeline](docs/logging.md)
- [Container executable paths](Dockerfile)
- [Compose service configuration](docker-compose.yml)

This draft documents the checked-out implementation. Drafting it does not run
database migrations, provision credentials, change mappings or resume workflows.
