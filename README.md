# KNS Discovery

Local network discovery for the KNS simulator, implemented in Go using the
standard library. This is a separate repository from KNS.

The collector reads active interfaces, default routes and the operating system's
neighbor cache. It does not scan ports, capture traffic or change network settings.
By default it also resolves names for observed neighbors through the OS DNS
resolver. Up to eight queries run concurrently, with a 750 ms timeout per query
and a three-second budget per collection. Positive answers are cached for ten
minutes and negative answers for one minute in watch mode. Cache entries are
scoped by interface, IP and MAC, and removed when the device disappears.
Use `--resolve-names=false` to disable name lookups. Unresolved devices remain
in the snapshot with their IP labels; link-local addresses are not queried.

Recognizable hostnames (for example `desktop-*`, `iphone-*`, `office-printer`,
or `synology-*`) provide conservative type hints, recorded as `hostname_hint`
in `evidence`; resolved names are marked `reverse_dns`. These hints are not
hardware verification. Default-route evidence takes priority for routers, and
inventory labels/types always take priority over automatic identification.
An empty cache is not evidence that a network has no other devices. Hidden switches,
access points and device models cannot be reliably inferred from ARP/NDP alone.

## Implemented stages

1. Define the versioned snapshot contract and repository structure.
2. Implement Windows/Linux collection, deterministic topology generation, inventory
   overrides, atomic output and watch mode, with fixture tests.
3. Extend KNS with device metadata and identity-based live reconciliation.
4. Connect live snapshots to the desktop, verify interoperability and document usage.

## Build and run

From this repository on Windows:

```powershell
go build -o bin/kns-discovery.exe ./cmd/kns-discovery
.\bin\kns-discovery.exe --output output/network.json --watch 5s
```

After the first snapshot is published, launch the sibling KNS desktop from a
second terminal in this repository:

```powershell
..\KNS\build\app\Release\KNS.exe --watch-topology output/network.json
```

See [KNS live discovery usage](../KNS/docs/discovery.md) for build instructions,
desktop editing, inventory overrides and update behavior. On Linux, build with
`go build -o bin/kns-discovery ./cmd/kns-discovery` and run `./bin/kns-discovery`.
Omit `--watch` for a single snapshot. Use `--interface` to select an exact interface
name and `--inventory` for a JSON map of `external_id` to `label`/`type` overrides.
Inventory is reloaded on each collection, so edits apply without restarting watch
mode. Use `{}` to clear overrides. Invalid or missing inventory files retain the
last published snapshot until corrected. Stop watch mode with Ctrl+C.

Watch mode retries failed collections at the configured interval, including a
failure on the first attempt. An offline interface, collection timeout or invalid
inventory never replaces the last good snapshot. No output is published until a
collection succeeds. Invalid static options (such as negative bandwidth) still
fail immediately; single-collection mode reports collection failures and exits.

Verify with `go test ./...` and `go vet ./...`.

## Snapshot contract

Snapshots use `schema_version: "1.0"`, `name`, `nodes` and `links`. Legacy KNS files
with an integer `nodes` field remain valid. Discovery snapshots use a node array:

```json
{
  "schema_version": "1.0",
  "name": "Local network",
  "nodes": [
    {"id": 0, "external_id": "host:example", "label": "My computer", "type": "computer", "addresses": ["192.0.2.10"], "evidence": "local_interface"},
    {"id": 1, "external_id": "segment:ethernet:192.0.2.0/24", "label": "Ethernet 192.0.2.0/24", "type": "network_segment", "evidence": "interface_prefix"}
  ],
  "links": [
    {"from": 0, "to": 1, "bandwidth": 100, "delay": 1, "loss": 0, "inferred": true, "evidence": "shared_segment"}
  ]
}
```

Numeric IDs are snapshot-local. `external_id` is the reconciliation identity;
the running simulator preserves its own numeric IDs and never recycles removed
nodes. Unchanged links retain their identity and transmission state. Snapshots
are authoritative for the loaded discovery topology, including removals.

Device types: `unknown`, `computer`, `router`, `switch`, `access_point`, `server`,
`phone`, `printer`, `iot`, `network_segment`. Types describe devices; they do not
emulate a vendor operating system or change TCP behavior. A gateway's router type
is based on its routing role, not hardware fingerprinting.

Links to a network segment represent inferred adjacency, not verified cables.
Bandwidth, delay and loss are explicit simulation assumptions, not measurements.
Snapshots contain local network identifiers: generated output is ignored by Git.
