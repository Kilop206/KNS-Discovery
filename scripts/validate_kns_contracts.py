from __future__ import annotations

import json
import os
import subprocess
import tempfile
import urllib.request
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

KNS_BASE = "https://raw.githubusercontent.com/Kilop206/KNS/main"
ROOT = Path(__file__).resolve().parents[1]


def load_json(url: str) -> dict:
    with urllib.request.urlopen(url, timeout=15) as response:
        return json.load(response)


def main() -> int:
    version = Path("VERSION").read_text(encoding="utf-8").strip()
    release = load_json(f"{KNS_BASE}/releases/ecosystem-v1.json")
    expected = release.get("components", {}).get("kns_discovery")
    if version != expected:
        raise SystemExit(
            f"kns_discovery VERSION {version} != ecosystem release train {expected}"
        )

    schema = load_json(f"{KNS_BASE}/contracts/discovery-diff-v1.schema.json")
    Draft202012Validator.check_schema(schema)

    representative = {
        "schema_version": "1.0",
        "baseline_available": True,
        "added_nodes": ["device:new"],
        "removed_nodes": ["device:gone"],
        "changed_nodes": ["host:a"],
        "added_links": ["device:new<->host:a"],
        "removed_links": [],
        "changed_links": ["host:a<->router:b"],
    }
    Draft202012Validator(schema).validate(representative)

    topology_schema = load_json(f"{KNS_BASE}/contracts/topology-v1.schema.json")
    snapshot_schema = load_json(f"{KNS_BASE}/contracts/discovery.snapshot.schema.json")
    registry = Registry().with_resources(
        (item["$id"], Resource.from_contents(item))
        for item in (topology_schema, snapshot_schema)
    )
    validator = Draft202012Validator(
        snapshot_schema, registry=registry, format_checker=FormatChecker()
    )
    with tempfile.TemporaryDirectory(prefix="kns-discovery-contract-") as temporary:
        output = Path(temporary) / "snapshot.json"
        environment = os.environ.copy()
        environment["KNS_DISCOVERY_CONTRACT_OUTPUT"] = str(output)
        subprocess.run(
            [
                "go",
                "test",
                "./internal/discovery",
                "-run",
                "^TestSnapshotSerializationConformsToTopologyV1ProducerContract$",
                "-count=1",
            ],
            check=True,
            cwd=ROOT,
            env=environment,
        )
        validator.validate(json.loads(output.read_text(encoding="utf-8")))

    invalid = load_json(
        f"{KNS_BASE}/contracts/fixtures/discovery.snapshot.invalid.json"
    )
    if validator.is_valid(invalid):
        raise SystemExit("Canonical invalid discovery snapshot was accepted")
    print(
        f"KNS Discovery VERSION {version}, diff fixture and Go snapshot conform "
        "to canonical KNS contracts."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
