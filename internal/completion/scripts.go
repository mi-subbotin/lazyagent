// Package completion holds embedded shell completion scripts that the
// `lazyagent completion <shell>` subcommand prints to stdout.
//
// The scripts are handcoded — using cobra would pull in a much larger
// flag-parsing dependency for what is in practice a single file per
// shell. They cover the full subcommand tree (config / logs / shared /
// completion), plus the small set of global flags.
package completion

import _ "embed"

//go:embed bash.sh
var Bash string

//go:embed zsh.sh
var Zsh string

//go:embed fish.sh
var Fish string
