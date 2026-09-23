# Listenbox CLI

Official command-line client for publishing and managing podcasts on
[Listenbox](https://listenbox.app).

The CLI uses Listenbox's public API. YouTube imports use [kkdai/youtube](https://github.com/kkdai/youtube) to download on your computer and bundled FFmpeg 9+ to prepare media before uploading it directly to Listenbox's media storage.

## Install

Build a native distribution with Go 1.27+, Node.js 26, Moon, and FFmpeg/FFprobe 9+ installed:

```sh
moon run package
tar -xzf dist/listenbox.tar.gz -C /your/bin/directory
```

Keep `listenbox`, `ffmpeg`, `ffprobe`, and the license notices together. The bundle uses the build machine's native media binaries; Linux builds require compatible system C libraries. Media processing resolves these bundled executables directly.

Make sure the installation directory is on `PATH`, then authorize the CLI:

```sh
listenbox login
```

## Usage

```sh
listenbox shows list
listenbox import "https://www.youtube.com/playlist?list=YOUR_PLAYLIST_ID"
listenbox help
```

Run `listenbox help <command>` for command-specific usage.

## Develop

Generated API client and configuration files are committed, so a fresh clone
builds without another repository or a code-generation step.

```sh
go build ./cmd/listenbox
go test -tags=dev ./...
```

[Moon](https://moonrepo.dev) runs the complete project check:

```sh
moon run check
```

CI follows [Moon's CI guide](https://moonrepo.dev/docs/guides/ci): full Git
history, source-based affected selection, and plain `moon ci`. It runs the same
project tasks used locally; aggregate checks and maintenance commands are excluded
from automatic selection. The same `moon.yml` also works as a Listenbox submodule.

Go modules, compiler output, and golangci-lint data are cached. Moon task results
are not restored across CI runs. Sources, module files, fixtures, templates, and
configuration determine affected tasks; `$CI` is not an input. CI verifies
formatting, and selected Go tests bypass Go's test-result cache. Native Moon
reports are attached to the workflow, including on failure.

## License

See [LICENSE](LICENSE).
