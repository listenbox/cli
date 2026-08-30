//go:build !dev

package cli

import _ "embed"

//go:embed cli.prod.yaml
var embeddedCLIConfig []byte
