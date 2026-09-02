// Command cask is the single entry point of go-cask: CLI store operations
// over the library in-process and, via the web subcommand, the embedded
// viewer — see cli.instructions.md. It is a thin main: all behavior lives in
// the cas library and the internal/ packages. The product ships no network
// JSON API (backend-architecture §1).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const usage = `usage: cask [-store <path>] <command> [args]

flags:
  -store <path>   the store directory (the library in-process, FSRawStore)

commands:
  put <file|- >      store bytes (or stdin); prints the hash
  get <hash> [-o <file>]     (no -o prints to stdout)
  list [--algo] [--limit] [--offset]
  meta <hash>
  stats
  verify <hash|--all>
  gc <roots...>
  prune --min-age <dur> <roots...> [--dry-run]
  clean [--min-age <dur>]   remove orphan *.tmp files (crash leftovers)
  web [-store <dir>] [-bind <addr>] [-tokens r=t,...] [-allow-insecure-bind]
  version
`

// modeFlags are the global flags parsed before the subcommand.
type modeFlags struct {
	store string
}

func main() {
	ctx := context.Background()
	mf, cmd, args, err := parseGlobal(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n%s", err, usage)
		os.Exit(2)
	}
	switch cmd {
	case "web":
		runWeb(ctx, args)
	case "version":
		runVersion()
	default:
		os.Exit(runOp(ctx, mf, cmd, args))
	}
}

// parseGlobal consumes the -store flag and returns the subcommand with its
// remaining args.
func parseGlobal(args []string) (modeFlags, string, []string, error) {
	var mf modeFlags
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-store":
			if i+1 >= len(args) {
				return mf, "", nil, fmt.Errorf("-store needs a path")
			}
			mf.store, i = args[i+1], i+2
		default:
			return mf, args[i], args[i+1:], nil
		}
	}
	return mf, "", nil, fmt.Errorf("no command")
}

// runOp dispatches a store operation and returns the exit code: 0 success,
// 1 runtime error, 2 usage error (cli §3). web/version are handled in main.
func runOp(ctx context.Context, mf modeFlags, cmd string, args []string) int {
	t, err := openTarget(ctx, mf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	var opErr error
	switch cmd {
	case "put":
		opErr = opPut(ctx, t, args)
	case "get":
		opErr = opGet(ctx, t, args)
	case "list":
		opErr = opList(ctx, t, args)
	case "meta":
		opErr = opMeta(ctx, t, args)
	case "stats":
		opErr = opStats(ctx, t, args)
	case "verify":
		opErr = opVerify(ctx, t, args)
	case "gc":
		opErr = opGC(ctx, t, args)
	case "prune":
		opErr = opPrune(ctx, t, args)
	case "clean":
		opErr = opClean(ctx, t, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", cmd, usage)
		return 2
	}
	if opErr != nil {
		code := 1
		var ue usageError
		if errors.As(opErr, &ue) {
			code = 2
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", opErr)
		return code
	}
	return 0
}
