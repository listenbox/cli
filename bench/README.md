# YouTube import benchmark

These are historical results for the commits identified below. They predate the
shared `listenbox/youtubei` crate and do not measure the current implementation.

One command, from process launch through successful publication:

```sh
listenbox import --slug <unique-show> 'https://www.youtube.com/watch?v=w<unique-id>'
```

| Metric | Go | Rust |
|---|---:|---:|
| Wall time, including startup | 331 ms | 445 ms |
| CPU time (user + system) | 198 ms | 285 ms |
| CPU utilization | 58.4% | 64.7% |
| Peak RAM (RSS) | 31.2 MiB | 35.0 MiB |
| Binary size | 21.6 MiB | 19.6 MiB |

Measured on 2026-09-24. Each runtime figure is the median of 20 imports after four
warmups per build. On this fixture, Rust takes longer and uses more CPU time and
RAM; its executable is smaller.

## What runs

The repository's YouTube web mock serves a two-second, 1080p AVC video (1.50 MiB)
and separate Opus audio (43 KiB). Both CLIs run their actual extraction library,
download both streams, transcode Opus to AAC with bundled FFmpeg, prepare MP4/HLS,
upload to the local API/storage, and publish the episode. Each import gets a fresh
video ID and show. The driver rejects failed imports, unpublished video, and runs
that did not download the Opus fixture.

Go is [3d0f569](https://github.com/listenbox/cli/commit/3d0f56927a9fc0ae8a97d1cb64d209505ea2ae14):
**go-astiav 0.43.0 + kkdai/youtube 2.10.6**, from [PR #7](https://github.com/listenbox/cli/pull/7).
Rust is [caaec94](https://github.com/listenbox/cli/commit/caaec943677ce8fc4b32713b12389213d8445b7f):
**ffmpeg-the-third 6.0.0 + YouTube.js 18.0.0 through rquickjs 0.11.0**.
Both are optimized, stripped release executables with statically linked FFmpeg
9.0.2. No external FFmpeg or JavaScript process runs during the import.

## Measurement

Linux x86-64, AMD EPYC-Rome VM, eight vCPUs with normal affinity. Go 1.27.1;
Rust 1.98.1. Builds and fixture setup finish before timing. The E2E driver repeats
Go/Rust/Rust/Go twelve times, discarding the first two groups as warmups. File
caches are warm; no builds or other test suites run alongside the samples.

[`measure.c`](measure.c) measures the CLI child from `posix_spawn` to process exit
using a monotonic clock and `wait4`. Wall time includes startup, local HTTP waits,
and the complete import. CPU time combines user and system time across CLI
threads. CPU utilization is CPU time divided by wall time, expressed as a
percentage (100% means one fully occupied CPU). RAM is peak resident memory.
The supervisor and backend services' CPU/RAM are excluded. There is no separate
startup-only claim: startup is included in the one command's wall time.

These results describe this short, deterministic local workload, not live YouTube
latency, signature challenges, long-video memory growth, or languages in general.
[Raw samples, binary hashes and summary](results/youtube-import.json) ·
[Build and host details](results/environment.json).

## Reproduce

In a separate checkout of Go commit `3d0f569`, run `moon run cli:build`.
Then, from the parent Listenbox workspace with this Rust CLI submodule:

```sh
LISTENBOX_BENCHMARK_GO=/absolute/go-checkout/dist/listenbox moon run api:benchmark-cli
```

That target builds the Rust release executable and measurement wrapper, starts
the canonical E2E environment, measures imports, and summarizes them with
[`summarize.py`](summarize.py). Results are written to
`apps/api/.test-results/cli-import-summary.json`. No Go correctness suite is run.
