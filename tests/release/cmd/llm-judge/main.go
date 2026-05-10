// Package main is the LLM-judge CLI for the behavioral test harness.
//
// The upstream LLM client library is imported via the placeholder alias
// `git.local/llmclient` so tracked source contains no upstream identity.
// `make behavioral-setup` generates a gitignored go.work at repo root that
// resolves the alias to the directory pointed at by LLM_CLIENT_PATH in
// .env.
//
// Subcommands: ping (smoke), rubric, diagnose, similarity. The
// similarity subcommand is wired by thrum-9mnx.4.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	llmclient "git.local/llmclient/endpoint"
)

const (
	rubricPromptV   = 1
	diagnosePromptV = 1
	defaultModel    = "glm-4.5-flash"
	chatTimeout     = 60 * time.Second
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

// newClient constructs a ZAI-backed chat client from ZAI_API_KEY in the
// environment. Caller is responsible for any retries/cancellation.
func newClient() (*llmclient.UnifiedChatClient, error) {
	apiKey := os.Getenv("ZAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ZAI_API_KEY not set")
	}
	return llmclient.NewChatClient(llmclient.ChatClientConfig{
		EndpointURL: llmclient.ZaiDefaultEndpoint,
		Provider:    "zai",
		APIKey:      apiKey,
	})
}

// modelName resolves the model name to use. Override with
// LLM_JUDGE_MODEL; defaults to glm-4.5-flash for cost/latency.
func modelName() string {
	if m := os.Getenv("LLM_JUDGE_MODEL"); m != "" {
		return m
	}
	return defaultModel
}

// pingCmd verifies the client lib is wired up. Used by `make
// behavioral-setup`. Constructs a client without making any network call
// and emits {"ok":true} on success.
func pingCmd() int {
	c, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm-judge ping: %v\n", err)
		return 1
	}
	_ = c.ProviderName()
	out, _ := json.Marshal(map[string]any{"ok": true})
	fmt.Println(string(out))
	return 0
}

type rubricResult struct {
	Score     int    `json:"score"`
	Reasoning string `json:"reasoning"`
	PromptV   int    `json:"prompt_v"`
	Model     string `json:"model"`
}

func rubricCmd(args []string) int {
	fs := flag.NewFlagSet("rubric", flag.ExitOnError)
	rubric := fs.String("rubric", "", "evaluation rubric text")
	transcriptFile := fs.String("transcript", "", "path to a file containing the transcript text")
	threshold := fs.Int("threshold", 4, "minimum acceptable score")
	_ = fs.Parse(args)

	if *rubric == "" || *transcriptFile == "" {
		fmt.Fprintln(os.Stderr, "rubric: --rubric and --transcript required")
		return 2
	}

	body, err := os.ReadFile(*transcriptFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rubric: read transcript: %v\n", err)
		return 1
	}

	c, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rubric: client: %v\n", err)
		return 1
	}

	prompt := buildRubricPrompt(*rubric, string(body))
	score, reasoning, modelUsed, err := callForScore(c, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rubric: model call: %v\n", err)
		return 1
	}

	out, _ := json.Marshal(rubricResult{
		Score:     score,
		Reasoning: reasoning,
		PromptV:   rubricPromptV,
		Model:     modelUsed,
	})
	fmt.Println(string(out))

	if score < *threshold {
		return 3 // soft fail — distinct from infrastructure (1) or arg error (2)
	}
	return 0
}

func buildRubricPrompt(rubric, transcript string) string {
	var b strings.Builder
	b.WriteString("You are evaluating an AI agent's behavior.\n\n")
	b.WriteString("RUBRIC:\n")
	b.WriteString(rubric)
	b.WriteString("\n\nTRANSCRIPT:\n")
	b.WriteString(transcript)
	b.WriteString("\n\nReturn ONLY a JSON object with two fields:\n")
	b.WriteString(`  "score": integer 0-5 per the rubric` + "\n")
	b.WriteString(`  "reasoning": one-sentence explanation` + "\n")
	b.WriteString("No other text. No markdown.\n")
	return b.String()
}

func callForScore(c *llmclient.UnifiedChatClient, prompt string) (int, string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), chatTimeout)
	defer cancel()
	resp, err := c.Chat(ctx, modelName(), []llmclient.ChatMessage{
		{Role: "user", Content: prompt},
	}, nil)
	if err != nil {
		return 0, "", "", err
	}
	text := strings.TrimSpace(resp.Content)
	// Models sometimes wrap JSON in fences; strip them.
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	var parsed struct {
		Score     int    `json:"score"`
		Reasoning string `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return 0, "", resp.Model, fmt.Errorf("model returned non-JSON: %s", text)
	}
	return parsed.Score, parsed.Reasoning, resp.Model, nil
}

type diagnoseResult struct {
	Reasoning string `json:"reasoning"`
	PromptV   int    `json:"prompt_v"`
	Model     string `json:"model"`
}

func diagnoseCmd(args []string) int {
	fs := flag.NewFlagSet("diagnose", flag.ExitOnError)
	testDesc := fs.String("test-description", "", "the test's description")
	stepDesc := fs.String("step-description", "", "the step's description (id + intent)")
	failedPredicate := fs.String("failed-predicate", "", "the predicate that failed")
	transcriptFile := fs.String("transcript", "", "path to captured pane transcript")
	stateFile := fs.String("state", "", "path to fixture-state-snapshot JSON")
	_ = fs.Parse(args)

	transcript := readOrEmpty(*transcriptFile)
	state := readOrEmpty(*stateFile)

	c, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "diagnose: %v\n", err)
		return 1
	}

	prompt := buildDiagnosePrompt(*testDesc, *stepDesc, *failedPredicate, transcript, state)
	ctx, cancel := context.WithTimeout(context.Background(), chatTimeout)
	defer cancel()
	resp, err := c.Chat(ctx, modelName(), []llmclient.ChatMessage{
		{Role: "user", Content: prompt},
	}, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diagnose: %v\n", err)
		return 1
	}

	out, _ := json.Marshal(diagnoseResult{
		Reasoning: strings.TrimSpace(resp.Content),
		PromptV:   diagnosePromptV,
		Model:     resp.Model,
	})
	fmt.Println(string(out))
	return 0
}

func readOrEmpty(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func buildDiagnosePrompt(testDesc, stepDesc, failedPredicate, transcript, state string) string {
	var b strings.Builder
	b.WriteString("An AI agent's behavioral test step failed. Explain in one sentence the most likely cause.\n\n")
	b.WriteString("TEST: " + testDesc + "\n")
	b.WriteString("STEP: " + stepDesc + "\n")
	b.WriteString("FAILED PREDICATE: " + failedPredicate + "\n\n")
	if transcript != "" {
		b.WriteString("RECENT PANE OUTPUT:\n" + transcript + "\n\n")
	}
	if state != "" {
		b.WriteString("FIXTURE STATE:\n" + state + "\n\n")
	}
	b.WriteString("Return ONLY a one-sentence explanation. No JSON, no markdown.\n")
	return b.String()
}

// similarityCmd is implemented by thrum-9mnx.4.
func similarityCmd(args []string) int {
	_ = args
	fmt.Fprintln(os.Stderr, "similarity: not implemented yet (thrum-9mnx.4)")
	return 1
}
