//go:build !dev

package main

import _ "embed"

//go:embed cli.prod.yaml
var embeddedCLIConfig []byte
