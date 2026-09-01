// Command cas-api-demo round-trips a file through a running CAS API server
// using the public client SDK (client/), demonstrating streaming upload and
// download, dedup, and metadata.
//
// Usage:
//
//	go run ./examples/cas-api/demo -api http://127.0.0.1:8080 \
//	    -token operator -file ./data.txt
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/dmundt/go-cask/client"
)

func main() {
	var (
		api   = flag.String("api", "http://127.0.0.1:8080", "CAS API server URL")
		token = flag.String("token", "operator", "bearer token")
		file  = flag.String("file", "", "file to store and fetch")
	)
	flag.Parse()
	if *file == "" {
		fmt.Fprintln(os.Stderr, "usage: cas-api-demo -api <url> -token <tok> -file <path>")
		os.Exit(2)
	}

	ctx := context.Background()
	c := client.New(*api, *token)

	f, err := os.Open(*file)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	h, dedup, err := c.Put(ctx, f, "sha256")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("stored %s deduplicated=%v\n", h, dedup)

	got, err := c.GetBytes(ctx, h)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("fetched %d bytes\n", len(got))

	m, err := c.Meta(ctx, h)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("meta: size=%d algorithm=%s\n", m.Size, m.Algorithm)

	st, err := c.Stats(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("stats: %d objects, %d bytes\n", st.ObjectCount, st.TotalSize)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
