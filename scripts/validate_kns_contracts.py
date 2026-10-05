from __future__ import annotations

import json
import urllib.request
from pathlib import Path

from jsonschema import Draft202012Validator


KNS_BASE = "https://raw.githubusercontent.com/Kilop206/KNS/main"


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
    print(
        f"KNS Discovery VERSION {version} and diff fixture conform "
        "to canonical KNS contracts."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
