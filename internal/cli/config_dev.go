//go:build dev

package cli

import _ "embed"

//go:embed cli.dev.yaml
var embeddedCLIConfig []byte
