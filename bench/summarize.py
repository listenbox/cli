#!/usr/bin/env python3
"""Summarize api:benchmark-cli: one median per metric, plus auditable raw samples."""
import hashlib
import json
from pathlib import Path
import statistics
import subprocess
import sys

log_path, go_binary, rust_binary = map(Path, sys.argv[1:])
samples = []
passed = 0
suite_passed = False
for line in log_path.read_text().splitlines():
    event = json.loads(line)
    if event.get("Action") == "pass":
        if event.get("Test") == "TestCLIYouTubeImportBenchmark":
            passed += 1
        elif "Test" not in event:
            suite_passed = True
    _, marker, sample = event.get("Output", "").partition("CLI_IMPORT_SAMPLE ")
    if marker:
        build, metrics = sample.split(" ", 1)
        samples.append({"build": build, **json.loads(metrics)})
if passed != 12 or not suite_passed or len(samples) != 48:
    raise SystemExit("Expected 12 passing measurement cases and 48 successful imports")
if [row["build"] for row in samples] != ["go", "rust", "rust", "go"] * 12:
    raise SystemExit("Unexpected import order")

summary = {}
binaries = {}
for build, binary in (("go", go_binary), ("rust", rust_binary)):
    rows = [row for row in samples if row["build"] == build]
    for index, row in enumerate(rows):
        row["warmup"] = index < 4
    measured = rows[4:]
    metrics = ("wall_ms", "cpu_ms", "cpu_percent", "peak_rss_mib")
    if any(row[key] <= 0 for row in rows for key in metrics):
        raise SystemExit("Nonpositive process accounting value")
    summary[build] = {key: statistics.median(row[key] for row in measured) for key in metrics}
    data = binary.read_bytes()
    summary[build]["binary_mib"] = len(data) / 1024**2
    binaries[build] = {
        "commit": subprocess.check_output(
            ["git", "-C", str(binary.parent), "rev-parse", "HEAD"], text=True
        ).strip(),
        "bytes": len(data),
        "sha256": hashlib.sha256(data).hexdigest(),
    }
json.dump({
    "command": "listenbox import --slug <unique-show> https://www.youtube.com/watch?v=w<unique-id>",
    "warmups_per_build": 4,
    "samples_per_build": 20,
    "statistic": "median for each metric; CPU time is user + system; CPU utilization is 100 * CPU / wall per sample",
    "summary": summary,
    "binaries": binaries,
    "samples": samples,
}, sys.stdout, indent=2)
print()
