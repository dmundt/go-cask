// Command cask is the single entry point of go-cask: CLI store operations
// (local -store or remote -api) and, via the web subcommand, the embedded
// server (CAS API + viewer) — see cli.instructions.md. It is a thin main:
// all behavior lives in the cas/client libraries and internal/ packages.
package main

import (
	"context"
	"fmt"
	"os"
)

const usage = `usage: cask <command> [args]

commands:
  put <file|- >      store bytes (or stdin); prints the hash
  get <hash> [-o <file>]
  cat <hash>
  list [--algo] [--limit] [--offset]
  meta <hash>
  stats
  verify <hash|--all>
  gc <roots...>
  prune --min-age <dur> <roots...> [--dry-run]
  web [-store <dir>] [-bind <addr>] [-tokens r=t,...] [-rate n] [-burst n]
  version
`

func main() {
	ctx := context.Background()
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "web":
		runWeb(ctx, os.Args[2:])
	case "version":
		runVersion()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
