# Listenbox CLI

Official command-line client for publishing and managing podcasts on
[Listenbox](https://listenbox.app).

The CLI talks only to Listenbox's public API. Its generated Go client and local
configuration types are committed to this repository.

## Install

Requires Go 1.27 or newer.

```sh
go install github.com/listenbox/listenbox-cli/cmd/listenbox@latest
```

Make sure Go's binary directory is on `PATH`, then authorize the CLI:

```sh
listenbox login
```

## Usage

```sh
listenbox shows list
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
