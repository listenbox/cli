//go:build dev

package main

import _ "embed"

//go:embed cli.dev.yaml
var embeddedCLIConfig []byte
