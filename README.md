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
Temporary DNS failures retain an existing name for at most thirty minutes after
its last successful lookup, retrying after one minute. A definitive missing-name
answer clears it immediately; transient failures never extend the retention limit.
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


## Snapshot diffs

Watch mode can optionally publish a second JSON file describing the structural
difference between the current collection and the previously published
snapshot:

```bash
go run ./cmd/kns-discovery \
  --watch 5s \
  --output output/network.json \
  --diff-output output/network.diff.json
```

The diff is deterministic and keyed by stable `external_id` values rather than
the snapshot-local numeric node IDs. Numeric ID reordering alone therefore does
not create false changes.

The diff schema reports:

- `baseline_available`;
- added, removed, and changed node identities;
- added, removed, and changed links using normalized endpoint identities.

On the first collection, the baseline is unavailable and the current nodes and
links are reported as additions. When a collection is identical to the previous
snapshot, the diff is empty. Both topology and diff files use atomic replacement.


## Topology Hub synchronization

Discovery can optionally synchronize each successful snapshot into an existing
Topology Hub topology while continuing to publish the local snapshot normally.

Create a revocable Topology Hub desktop token with the `topologies` scope and
keep it out of command-line arguments:

```powershell
$env:KNS_TOPOLOGY_HUB_TOKEN = "knsh_..."
.\bin\kns-discovery.exe `
  --watch 5s `
  --output output/network.json `
  --diff-output output/network.diff.json `
  --hub-url http://localhost:3001 `
  --hub-topology <topology-id>
```

On Linux/macOS, export the same environment variable before running the binary.

The target topology must already exist and belong to the token user (or be
editable by an administrator). Discovery preserves its title, description and
visibility. Each actual remote topology change is sent with the Hub's current
optimistic-lock version and creates an immutable revision with the canonical
Discovery diff attached.

Remote diffs are calculated against the graph currently stored in Topology Hub,
not against the local snapshot file. This is intentional: if a Hub request fails
after the local snapshot was written, the next watch cycle can still reconstruct
the complete remote change and catch the Hub up. An unchanged Hub graph is not
updated, so watch mode does not create empty revisions.

Local publication remains authoritative for the collector process. A Hub failure
does not roll the local snapshot back; it makes that collection attempt report an
error and watch mode retries on a later cycle. Authentication failures, missing
topologies and optimistic-lock conflicts are reported explicitly. The bearer
token is never written to the snapshot or diff files.
