// Package main is the LLM-judge CLI for the behavioral test harness.
//
// The upstream LLM client library is imported via the placeholder alias
// `git.local/llmclient` so tracked source contains no upstream identity.
// `make behavioral-setup` generates a gitignored go.work at repo root that
// resolves the alias to the directory pointed at by LLM_CLIENT_PATH in
// .env.
//
// Subcommands: ping (smoke), rubric, diagnose, similarity. Stubs for the
// non-ping subcommands are wired in this task; their bodies land in
// thrum-9mnx.3 / thrum-9mnx.4.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	llmclient "git.local/llmclient/endpoint"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "rubric":
		os.Exit(rubricCmd(os.Args[2:]))
	case "diagnose":
		os.Exit(diagnoseCmd(os.Args[2:]))
	case "similarity":
		os.Exit(similarityCmd(os.Args[2:]))
	case "ping":
		os.Exit(pingCmd())
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: llm-judge <rubric|diagnose|similarity|ping> [args]")
}

// pingCmd verifies the client lib is wired up. Used by `make
// behavioral-setup` after generating go.work. It constructs a client
// against the default endpoint with the API key from the environment
// and emits {"ok":true} on success — without making any network call.
func pingCmd() int {
	apiKey := os.Getenv("ZAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "llm-judge ping: ZAI_API_KEY not set")
		return 1
	}
	client, err := llmclient.NewChatClient(llmclient.ChatClientConfig{
		EndpointURL: llmclient.ZaiDefaultEndpoint,
		Provider:    "zai",
		APIKey:      apiKey,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm-judge ping: %v\n", err)
		return 1
	}
	// Touch the client so the linker doesn't elide it; ProviderName is
	// a no-network method that confirms the client is wired up.
	_ = client.ProviderName()
	_ = context.Background()
	out, err := json.Marshal(map[string]any{"ok": true})
	if err != nil {
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// Stubs for thrum-9mnx.3 / thrum-9mnx.4. Implemented in those tasks.
func rubricCmd(args []string) int {
	_ = args
	fmt.Fprintln(os.Stderr, "rubric: not implemented yet (thrum-9mnx.3)")
	return 1
}

func diagnoseCmd(args []string) int {
	_ = args
	fmt.Fprintln(os.Stderr, "diagnose: not implemented yet (thrum-9mnx.3)")
	return 1
}

func similarityCmd(args []string) int {
	_ = args
	fmt.Fprintln(os.Stderr, "similarity: not implemented yet (thrum-9mnx.4)")
	return 1
}
