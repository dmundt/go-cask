// Command cask is the single entry point of go-cask: CLI store operations
// over the library in-process and, via the web subcommand, the embedded
// viewer — see cli.md. It is a thin main: all behavior lives in
// the cas library and the internal/ packages. The product ships no network
// JSON API (backend-architecture §1).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// errHelp reports that -h/-help asked for the usage text while the global flags
// were parsed. main prints the usage and exits 0: help is not a failure
// (cli.md §3).
var errHelp = errors.New("help requested")

// modeFlags are the global flags parsed before the subcommand.
type modeFlags struct {
	store string
}

// commandSpec is one subcommand. commands is the single source of dispatch, of
// the usage text, and of the parser that accepts each command's flags, so a
// subcommand cannot be run in one place and documented in another (cli.md §2).
type commandSpec struct {
	// name is the subcommand word.
	name string
	// operands is the operand and flag suffix cli.md §2 shows after name, e.g.
	// "<file|-> [-json]" for put; empty when the command takes none.
	operands string
	// summary is the one-line description used by the usage text.
	summary string
	// flags builds a fresh parser holding the flags the command accepts; nil
	// when the command takes no flags. It is the same parser the operation
	// parses with, so the help text cannot list a flag the parser rejects.
	flags func() *flag.FlagSet
	// op is the store operation, run in-process over the opened target.
	op func(ctx context.Context, t *target, args []string) error
	// run executes a command that is not a store operation (web, version).
	run func(ctx context.Context, mf modeFlags, args []string) int
}

// synopsis renders the command's one-line usage form from name and operands, so
// the word used for dispatch and the word documented cannot disagree.
func (c commandSpec) synopsis() string {
	if c.operands == "" {
		return c.name
	}
	return c.name + " " + c.operands
}

// commands is the CLI's complete command set, in usage order (cli.md §2). It is
// filled in init because a runner reaches back into the usage text (reportError
// renders the failing command's help), which a variable initializer would turn
// into an initialization cycle.
var commands []commandSpec

func init() {
	commands = []commandSpec{
		{
			name:     "put",
			operands: "<file|-> [-json]",
			summary:  "store bytes (or stdin); prints the hash",
			flags:    func() *flag.FlagSet { return putFlags(new(putArgs)) },
			op:       opPut,
		},
		{
			name:     "get",
			operands: "<hash> [-o <file>]",
			summary:  "retrieve to a file or stdout (no -o prints to stdout)",
			flags:    func() *flag.FlagSet { return getFlags(new(getArgs)) },
			op:       opGet,
		},
		{
			name:     "list",
			operands: "[-limit <n>] [-offset <n>] [-json]",
			summary:  "list objects ({total, objects}); skips a stray non-object file with a warning",
			flags:    func() *flag.FlagSet { return listFlags(new(listArgs)) },
			op:       opList,
		},
		{
			name:     "meta",
			operands: "<hash> [-json]",
			summary:  "metadata of one object (size, type, algorithm)",
			flags:    func() *flag.FlagSet { return metaFlags(new(metaArgs)) },
			op:       opMeta,
		},
		{
			name:    "stats",
			summary: "storage statistics (N objects, M bytes)",
			flags:   statsFlags,
			op:      opStats,
		},
		{
			name:     "verify",
			operands: "<hash>|--all",
			summary:  "integrity check (single object or full scan)",
			flags:    func() *flag.FlagSet { return verifyFlags(new(verifyArgs)) },
			op:       opVerify,
		},
		{
			name:     "gc",
			operands: "--min-age <dur> <roots...>",
			summary:  "reclaim objects unreachable from roots and older than the grace (default 1h; 0 = immediate, dangerous)",
			flags:    func() *flag.FlagSet { return gcFlags(new(gcArgs)) },
			op:       opGC,
		},
		{
			name:     "prune",
			operands: "--min-age <dur> <roots...> [--dry-run]",
			summary:  "age-based retention (dry-run default)",
			flags:    func() *flag.FlagSet { return pruneFlags(new(pruneArgs)) },
			op:       opPrune,
		},
		{
			name:     "clean",
			operands: "[--min-age <dur>]",
			summary:  "remove orphan *.tmp files older than the grace (default 24h)",
			flags:    func() *flag.FlagSet { return cleanFlags(new(cleanArgs)) },
			op:       opClean,
		},
		{
			name:     "seed-preview",
			operands: "[-count <n>]",
			summary:  "add deterministic viewer preview objects (-count 1-10000)",
			flags:    func() *flag.FlagSet { return seedPreviewFlags(new(seedPreviewArgs)) },
			op:       opSeedPreview,
		},
		{
			name:     "web",
			operands: "[-store <dir>] [-bind <addr>] [-hash-algo <name>] [-tokens r=t,...] [-allow-insecure-bind] [-no-open]",
			summary:  "start the embedded viewer; prints a one-time startup token and the token URL",
			flags:    func() *flag.FlagSet { return webFlags(new(webArgs), "") },
			run:      runWeb,
		},
		{
			name:    "version",
			summary: "print library + Go version",
			flags:   versionFlags,
			run:     runVersionCommand,
		},
	}
}

// command returns the table entry named name.
func command(name string) (commandSpec, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return commandSpec{}, false
}

// globalFlags registers the flags that precede the subcommand (cli.md §1) and
// returns the parser with the store path bound to store.
func globalFlags(store *string) *flag.FlagSet {
	flags := newFlagSet("cask")
	flags.StringVar(store, "store", "", "the store directory (the library in-process, FSBackend)")
	return flags
}

// usage renders the top-level help from the command table and the global flag
// set, so it can neither describe a command the CLI does not run nor miss one
// it does (cli.md §2, §4).
func usage() string {
	var b strings.Builder
	b.WriteString("usage: cask [-store <path>] <command> [args]\n\ncommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %s\n        %s\n", c.synopsis(), c.summary)
	}
	b.WriteString("\nglobal flags:\n")
	printDefaults(globalFlags(new(string)), &b)
	b.WriteString("\nrun 'cask <command> -h' for a command's flags\n")
	return b.String()
}

// commandUsage renders one command's synopsis and flag help, reading the
// synopsis from the table entry and the flags from the parser that accepts them
// (cli.md §2, §4).
func commandUsage(flags *flag.FlagSet) string {
	var b strings.Builder
	if c, ok := command(flags.Name()); ok {
		fmt.Fprintf(&b, "usage: cask %s\n\n%s\n", c.synopsis(), c.summary)
	} else {
		fmt.Fprintf(&b, "usage: cask %s [flags]\n", flags.Name())
	}
	b.WriteString("\nflags:\n")
	printDefaults(flags, &b)
	return b.String()
}

func main() {
	ctx := context.Background()
	mf, cmd, args, err := parseGlobal(os.Args[1:])
	if errors.Is(err, errHelp) {
		fmt.Print(usage())
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n%s", err, usage())
		os.Exit(2)
	}
	os.Exit(runOp(ctx, mf, cmd, args))
}

// parseGlobal consumes the global flags and returns the subcommand with its
// remaining arguments. A flag may be written -store <path>, -store=<path>, or
// --store=<path>; an unknown flag is a usage error, and -h/-help reports
// errHelp so main can print the usage and exit 0 (cli.md §1, §3, §4).
func parseGlobal(args []string) (modeFlags, string, []string, error) {
	var mf modeFlags
	flags := globalFlags(&mf.store)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return mf, "", nil, errHelp
		}
		return mf, "", nil, usagef("%v", err)
	}
	rest := flags.Args()
	if len(rest) == 0 {
		return mf, "", nil, usagef("no command")
	}
	return mf, rest[0], rest[1:], nil
}

// runOp dispatches a subcommand and returns the exit code: 0 success, 1 runtime
// error, 2 usage error (cli.md §3).
func runOp(ctx context.Context, mf modeFlags, cmd string, args []string) int {
	spec, ok := command(cmd)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", cmd, usage())
		return 2
	}
	if spec.run != nil {
		return spec.run(ctx, mf, args)
	}
	return runStoreOp(ctx, mf, spec, args)
}

// runStoreOp opens the store, takes the maintenance lock when the command is a
// sweep, runs the command's operation, and maps its error to an exit code.
//
// Maintenance operations (gc, prune, clean) take the store's exclusive
// cross-process lock first, so two maintenance sweeps never run on one store
// directory concurrently. Writers (put) and the viewer (web) never lock:
// object writes are safe across processes by construction (unique temps +
// atomic rename, cas-core §4.4), and sweeps reclaim only objects older than
// their grace `--min-age` so a concurrent writer's fresh objects survive.
// Read-only operations never lock.
func runStoreOp(ctx context.Context, mf modeFlags, spec commandSpec, args []string) int {
	t, err := openTarget(ctx, mf)
	if err != nil {
		// A -h/-help request is answered even when the store could not be
		// opened, so the usage text is always reachable (cli.md §3).
		if help, ok := helpRequest(args, spec.flags); ok {
			return reportError(help)
		}
		return reportError(err)
	}
	if maintenanceOp(spec.name) {
		lock, err := acquireStoreLock(mf.store)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		defer lock.release()
	}
	return reportError(spec.op(ctx, t, args))
}

// reportError prints err to stderr and returns the exit code its classification
// dictates: 2 for a usage error, 1 for a runtime/store error, and 0 when err is
// nil or a -h/-help request asked for the usage text (cli.md §3).
func reportError(err error) int {
	if err == nil {
		return 0
	}
	var help helpRequested
	if errors.As(err, &help) {
		fmt.Print(commandUsage(help.flags))
		return 0
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	var ue usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}

// maintenanceOp reports whether cmd is a store maintenance sweep (gc, prune,
// clean). These take the store's exclusive cross-process lock so two sweeps
// never overlap; writers (put) and reads never lock (cas-core §6).
func maintenanceOp(cmd string) bool {
	switch cmd {
	case "gc", "prune", "clean":
		return true
	default:
		return false
	}
}
