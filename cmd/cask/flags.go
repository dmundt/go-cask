package main

import (
	"errors"
	"flag"
	"io"
	"strings"
)

// newFlagSet returns a parser for the named subcommand. It reports failures to
// the caller instead of printing them: the CLI owns the message, the usage
// text, and the exit code (cli.md §3).
func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

// helpRequested reports a -h/-help request. It is not a failure: the caller
// prints the command's usage and exits 0 (cli.md §3).
type helpRequested struct{ flags *flag.FlagSet }

// Error implements the error interface.
func (h helpRequested) Error() string { return "help requested" }

// parseFlags parses args with flags, which accepts flags before the operands —
// the order cli.md §2 shows for list, meta, gc, prune, clean, seed-preview, and
// web. A malformed or unknown flag is a usage error (exit 2); -h/-help asks for
// the usage text (cli.md §3, §4).
func parseFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return flagError(flags, err)
	}
	return nil
}

// parseOperands parses args whose flags may follow the operand, the order
// cli.md §2 gives for put ("put <file> [-json]") and get ("get <hash> [-o
// <file>]"). The standard flag package stops at the first operand, so flags and
// their values are moved in front of the operands before parsing; an unknown
// flag is a usage error instead of being mistaken for an operand.
func parseOperands(flags *flag.FlagSet, args []string) error {
	reordered, err := flagsFirst(flags, args)
	if err != nil {
		return err
	}
	return parseFlags(flags, reordered)
}

// flagError maps a flag-package failure to the CLI's error types: -h/-help
// carries the parser whose help to print, anything else is a usage error.
func flagError(flags *flag.FlagSet, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return helpRequested{flags: flags}
	}
	return usageError{msg: err.Error()}
}

// helpRequest reports the -h/-help request in args, using a fresh parser built
// by newFlags — the same parser the operation parses with, so flag arity is
// honored (a -h that is another flag's value is not a help request). It is
// consulted before a store is opened, so the usage text works even when -store
// is missing or unusable (cli.md §3).
func helpRequest(args []string, newFlags func() *flag.FlagSet) (helpRequested, bool) {
	if newFlags == nil {
		return helpRequested{}, false
	}
	var help helpRequested
	if err := parseOperands(newFlags(), args); errors.As(err, &help) {
		return help, true
	}
	return helpRequested{}, false
}

// flagsFirst reorders args so every flag and its value precede the operands,
// carrying the same "flag needs an argument" and "--" (end of flags) semantics
// as the flag package. A flag the parser does not define is rejected here,
// because the flag package would never see one placed after an operand.
func flagsFirst(flags *flag.FlagSet, args []string) ([]string, error) {
	reordered := make([]string, 0, len(args))
	operands := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			operands = append(operands, args[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			operands = append(operands, arg)
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "h" || name == "help" {
			// Leave -h/-help to the flag package, whose ErrHelp becomes the
			// command's usage text.
			reordered = append(reordered, arg)
			continue
		}
		defined := flags.Lookup(name)
		if defined == nil {
			return nil, usagef("flag provided but not defined: %s", arg)
		}
		reordered = append(reordered, arg)
		if hasValue || isBoolFlag(defined) {
			continue
		}
		if i+1 >= len(args) {
			return nil, usagef("flag needs an argument: %s", arg)
		}
		i++
		reordered = append(reordered, args[i])
	}
	return append(reordered, operands...), nil
}

// isBoolFlag reports whether f takes no separate value argument, matching the
// flag package's own rule.
func isBoolFlag(f *flag.Flag) bool {
	type boolFlag interface{ IsBoolFlag() bool }
	b, ok := f.Value.(boolFlag)
	return ok && b.IsBoolFlag()
}

// printDefaults writes flags' help into w: the flag package only writes to its
// own output, which newFlagSet discards, so the caller redirects it.
func printDefaults(flags *flag.FlagSet, w io.Writer) {
	flags.SetOutput(w)
	flags.PrintDefaults()
}
