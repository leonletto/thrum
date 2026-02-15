package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/testgen"
)

func main() {
	output := flag.String("output", "", "Output path for .tar.gz fixture")
	seed := flag.Int64("seed", 42, "Random seed for deterministic generation")
	flag.Parse()

	if *output == "" {
		fmt.Fprintln(os.Stderr, "Usage: testgen -output <path.tar.gz> [-seed N]")
		os.Exit(1)
	}

	// Generate to temp directory
	tmpDir, err := os.MkdirTemp("", "thrum-testgen-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	thrumDir := filepath.Join(tmpDir, ".thrum")
	cfg := testgen.DefaultConfig()
	cfg.Seed = *seed

	fmt.Fprintf(os.Stderr, "Generating fixture (seed=%d)...\n", cfg.Seed)
	if err := testgen.Generate(thrumDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Compressing to %s...\n", *output)
	if err := testgen.CompressToTarGz(thrumDir, *output); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Done.\n")
}
