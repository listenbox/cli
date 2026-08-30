# Listenbox CLI

Official command-line client for publishing and managing podcasts on
[Listenbox](https://listenbox.app).

The CLI talks only to Listenbox's public API. Its OpenAPI contract, generated Go
client, and local configuration types are committed to this repository.

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
builds without another repository or a code-generation step. The pinned
oasmith tool regenerates the public client from `openapi/public.openapi.yaml`
as part of the project check.

```sh
go build ./cmd/listenbox
go test -tags=dev ./...
```

[Task](https://taskfile.dev) runs the complete project check:

```sh
task check
```

## License

See [LICENSE](LICENSE).
