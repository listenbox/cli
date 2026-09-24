# CLI startup and footprint comparison

Measured 2026-09-24 on Linux x86-64. These are measurements of two complete CLI
implementations, not a general claim about Go versus Rust.

The Go baseline is [3d0f569](https://github.com/listenbox/cli/commit/3d0f56927a9fc0ae8a97d1cb64d209505ea2ae14)
from [the go-astiav PR](https://github.com/listenbox/cli/pull/7), including
**go-astiav v0.43.0 and kkdai/youtube v2.10.6**. It statically links FFmpeg;
this comparison does not use the earlier CLI with FFmpeg subprocesses.
The Rust executable is [5bd42d5](https://github.com/listenbox/cli/commit/5bd42d509f33ae08bb6ef5cd21546cf3943bdaca),
including **ffmpeg-the-third 6.0.0, YouTube.js 18.0.0 and rquickjs 0.11.0**.

## Normal CPU affinity (all 8 vCPUs)

| Command | Build | Wall p50 / p95 (µs) | Mean user / system CPU (µs) | Mean total CPU (µs) | CPU utilization | Peak RSS p50 / max (KiB) |
|---|---|---:|---:|---:|---:|---:|
| `--help` | Go | 6,004 / 9,277 | 3,449 / 4,589 | 8,039 | 126.8% | 13,404 / 13,604 |
| `--help` | Rust | 2,545 / 3,842 | 638 / 1,930 | 2,568 | 95.5% | 6,640 / 6,860 |
| `episodes create --help` | Go | 5,562 / 9,144 | 3,112 / 4,520 | 7,632 | 126.4% | 13,416 / 13,628 |
| `episodes create --help` | Rust | 2,435 / 3,920 | 664 / 1,835 | 2,499 | 95.6% | 6,734 / 6,944 |

Rust's root-help median wall time is 57.6% lower,
mean total CPU time is 68.0% lower,
and median peak RSS is 50.5% lower.

## Binary size

| Artifact | Go + go-astiav + kkdai/youtube | Rust + ffmpeg-the-third + YouTube.js/rquickjs |
|---|---:|---:|
| Stripped executable | 22,639,472 bytes (21.59 MiB) | 20,525,240 bytes (19.57 MiB) |
| Executable compressed with gzip level 9 | 8,728,187 bytes | 8,141,703 bytes |

The Rust executable is 9.3% smaller uncompressed.
Both include their media and extraction implementations even though help does not
initialize or execute them. Neither needs FFmpeg, FFprobe, Node, or Bun executables
at runtime. FFmpeg is static; platform C libraries are dynamic. The Go binding
also requires avdevice, avfilter, and swscale libraries at build time. Rust only
requests its used FFmpeg libraries. Codec/source selections otherwise match.

## Controlled single-CPU run (CPU 7)

| Command | Build | Wall p50 / p95 (µs) | Mean user / system CPU (µs) | Mean total CPU (µs) | CPU utilization | Peak RSS p50 / max (KiB) |
|---|---|---:|---:|---:|---:|---:|
| `--help` | Go | 4,622 / 5,913 | 1,953 / 2,462 | 4,415 | 93.5% | 13,176 / 13,276 |
| `--help` | Rust | 1,960 / 2,629 | 563 / 1,283 | 1,846 | 92.8% | 6,632 / 6,940 |
| `episodes create --help` | Go | 4,344 / 5,545 | 1,937 / 2,202 | 4,140 | 93.6% | 13,300 / 13,348 |
| `episodes create --help` | Rust | 1,849 / 2,520 | 480 / 1,242 | 1,722 | 91.8% | 6,758 / 6,988 |

The same process affinity applies to each child. The normal-affinity run above
retains each runtime's ordinary CPU discovery and thread behavior.

## Method

- AMD EPYC-Rome KVM guest, 8 vCPUs; Linux 7.0.0-29-generic. Full environment,
  compiler versions, optimization flags and dependencies: [environment.json](results/environment.json).
- Go: CGO enabled, `go build -trimpath -ldflags="-s -w"`; Rust:
  `cargo build --locked --release`, opt-level 3, thin LTO, symbols stripped.
  Both use checksum-pinned FFmpeg 9.0.2, compiled with GCC 15.2.0.
- [`startup.c`](startup.c) directly uses `posix_spawn`, `CLOCK_MONOTONIC`, and
  `wait4`. Wall time covers spawn through process exit, including loading,
  runtime initialization, argument parsing, and help generation. It includes the
  common spawn/wait overhead; it is not a timestamp inside `main`.
- No HTTP requests, JSON response decoding, media processing, or terminal
  rendering. Both programs run identical argument vectors; stdout/stderr and stdin
  use `/dev/null`. The Go implementation also decodes its embedded YAML defaults
  on the help path; Rust has typed compiled defaults and exits from Clap help.
- 30 discarded warmups then 300 samples per binary per command, alternating Go
  and Rust order each iteration. Two commands and two affinity settings yield
  2,400 measured processes. Page/file caches are warm; no cache flushing.
- CPU time is per-child user plus system time from `wait4`, excluding the harness.
  CPU utilization is total CPU time divided by total wall time, so a process
  using multiple threads can exceed 100%. For these short processes, individual
  user/system accounting samples can be zero; their means and combined CPU time
  are more useful than separate medians. Values are reported in microseconds,
  not interpreted as nanosecond measurement precision.
- RAM is per-process maximum resident set size (`ru_maxrss` on Linux), not heap
  allocation, virtual address space, or media-import peak memory. RSS includes
  resident shared pages; it is not proportional set size.
- Benchmarks ran without concurrent builds or test suites. Local service
  containers remained running. This is one virtual host/run, not a hardware-wide
  guarantee, a cold-disk benchmark, or evidence of media/network throughput.
- Raw samples: [normal affinity](results/linux-amd64.csv),
  [CPU 7](results/linux-amd64-cpu7.csv). Corresponding JSON summaries contain
  means, medians, p95, ranges, standard deviations, page faults and context
  switches. [`binaries.json`](results/binaries.json) records exact commits,
  SHA-256 hashes, file lengths, and deterministic gzip sizes.

## Reproduce

Build the two commits in separate checkouts with their Moon package tasks and
required tools installed. In the Rust checkout:

```sh
cc -O2 -Wall -Wextra -Werror bench/startup.c -o /tmp/cli-startup
env -u GOMAXPROCS -u GOGC -u GOMEMLIMIT -u RUST_LOG \
  /tmp/cli-startup /absolute/go/dist/listenbox /absolute/rust/dist/release/listenbox > samples.csv
python3 bench/summarize.py samples.csv
# Repeat with `taskset -c 7 /tmp/cli-startup ...` for the controlled affinity run.
```

The summarizer uses the Python standard library; p95 uses nearest rank.
