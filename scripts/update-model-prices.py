#!/usr/bin/env python3
"""Refresh the build-bundled LiteLLM base token prices from an immutable commit."""
import argparse
import json
import math
from pathlib import Path
import re
from urllib.request import urlopen

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("commit", help="Full BerriAI/litellm commit SHA")
args = parser.parse_args()
if not re.fullmatch(r"[0-9a-f]{40}", args.commit):
    parser.error("commit must be a full 40-character SHA")
base = f"https://raw.githubusercontent.com/BerriAI/litellm/{args.commit}/"
with urlopen(base + "model_prices_and_context_window.json", timeout=30) as response:
    source = json.load(response)
fields = {"input": "input_cost_per_token", "output": "output_cost_per_token",
          "cache_read": "cache_read_input_token_cost", "cache_write": "cache_creation_input_token_cost"}
models = {}
for name, entry in source.items():
    if not isinstance(entry, dict) or entry.get("mode") != "chat":
        continue
    prices = {key: round(entry[field] * 1_000_000, 10) for key, field in fields.items()
              if isinstance(entry.get(field), (float, int)) and not isinstance(entry[field], bool)
              and math.isfinite(entry[field]) and entry[field] >= 0}
    if "input" in prices and "output" in prices:
        models[name] = prices
if not models:
    raise SystemExit("No chat prices found; existing snapshot was not changed")
with urlopen(base + "LICENSE", timeout=30) as response:
    license_text = response.read()
root = Path(__file__).resolve().parent.parent / "internal" / "web"
snapshot = {"source": "https://github.com/BerriAI/litellm", "commit": args.commit,
            "unit": "USD per million tokens", "license": license_text.decode(), "models": models}
(root / "model-prices.json").write_text(json.dumps(snapshot, sort_keys=True, separators=(",", ":")) + "\n")
(root / "model-prices.LICENSE").write_bytes(license_text)
print(f"Bundled {len(models)} chat model prices from {args.commit}")
