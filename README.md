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
pkgx moon run check
```

CI runs the same check with formatting verification and Go vet. It restores Go
modules, compiler output, and golangci-lint data, saving an updated cache for each
commit. Moon restores only its portable `hashes` and `outputs` directories, keyed
by runner architecture and the resolved toolchain. Moon hashes task sources,
embedded YAML, module files, configuration, and CI environment inputs before
reusing a result.

## License

See [LICENSE](LICENSE).
