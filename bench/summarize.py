"""Summarize startup.c CSV output using only the Python standard library."""
import csv
import json
import math
import statistics
import sys
from collections import defaultdict


def distribution(values):
    values = sorted(values)
    return {
        "mean": statistics.mean(values),
        "median": statistics.median(values),
        "p95": values[math.ceil(len(values) * 0.95) - 1],
        "min": values[0],
        "max": values[-1],
        "stddev": statistics.pstdev(values),
    }


groups = defaultdict(list)
with open(sys.argv[1], newline="", encoding="utf-8") as source:
    for row in csv.DictReader(source):
        groups[(row["implementation"], row["command"])].append(row)
results = []
for (implementation, command), rows in groups.items():
    metrics = {
        field: distribution([float(row[field]) for row in rows])
        for field in rows[0]
        if field not in ("implementation", "command", "sample")
    }
    cpu = [float(row["user_us"]) + float(row["system_us"]) for row in rows]
    metrics["total_cpu_us"] = distribution(cpu)
    # Ratio of totals avoids averaging unstable percentages for short processes.
    cpu_percent = 100 * sum(cpu) / sum(float(row["wall_us"]) for row in rows)
    results.append({
        "implementation": implementation, "command": command,
        "samples": len(rows), "cpu_percent": cpu_percent, "metrics": metrics,
    })
print(json.dumps(results, indent=2))
