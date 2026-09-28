package main

import (
	"strings"
	"testing"
)

// TestListRejectsSurplusOperands pins go-cask#366: `list` takes no operands, and
// a surplus word is a usage error (exit 2) on stderr rather than a silently
// ignored argument that prints the whole store as if nothing were wrong. The
// flag form is exercised in the same test so the new NArg check cannot have been
// bought by rejecting the flags `list` documents (cli.md §2, §3).
func TestListRejectsSurplusOperands(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "listed bytes")); code != 0 {
		t.Fatal("put failed")
	}

	stdout, stderr, code := runBoth(t, mf, "list", "extra")
	if code != 2 {
		t.Fatalf("list extra exit = %d, want 2 (usage)", code)
	}
	if stdout != "" {
		t.Fatalf("list extra printed %q to stdout, want nothing", stdout)
	}
	if !strings.Contains(stderr, "list takes no positional arguments") {
		t.Fatalf("list extra stderr = %q, want the usage error naming the operands", stderr)
	}

	// The same rejection holds for every surplus word, including one that looks
	// like a filter value that was written as an operand.
	for _, extra := range []string{"extra", "blob@1"} {
		if _, _, code := runBoth(t, mf, "list", extra); code != 2 {
			t.Errorf("list %q exit = %d, want 2 (usage)", extra, code)
		}
	}

	// The documented flag forms still work. The stored object is raw bytes (no
	// envelope), so a header filter matches it only as the unspecified codec;
	// -type is exercised separately, where an unmatched filter is an empty
	// result at exit 0 rather than a failure.
	for _, args := range [][]string{
		nil,
		{"-json"},
		{"-limit", "1", "-offset", "0"},
		{"-codec", "unspecified"},
	} {
		stdout, code := run(t, mf, "list", args...)
		if code != 0 {
			t.Fatalf("list %v exit = %d, want 0", args, code)
		}
		if strings.TrimSpace(stdout) == "" {
			t.Fatalf("list %v printed nothing, want the stored object", args)
		}
	}
	if _, code := run(t, mf, "list", "-type", "blob@1"); code != 0 {
		t.Fatalf("list -type blob@1 exit = %d, want 0 (an unmatched filter is an empty result)", code)
	}
}
