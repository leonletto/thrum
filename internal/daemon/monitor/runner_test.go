package monitor

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testJob is a concrete RunnerJob used exclusively in tests.
// Test fixtures MAY use "sh -c" because the test author controls all inputs.
type testJob struct {
	id              string
	name            string
	argv            []string
	matchPattern    string
	target          string
	cwd             string
	env             map[string]string
	debounceSeconds int
}

func (j *testJob) GetID() string              { return j.id }
func (j *testJob) GetName() string            { return j.name }
func (j *testJob) GetArgv() []string          { return j.argv }
func (j *testJob) GetCwd() string             { return j.cwd }
func (j *testJob) GetEnv() map[string]string  { return j.env }
func (j *testJob) GetDebounceSeconds() int    { return j.debounceSeconds }

func defaultJob(t *testing.T) *testJob {
	t.Helper()
	return &testJob{
		id:              "mon_TEST",
		name:            "test",
		target:          "@t",
		cwd:             t.TempDir(),
		env:             map[string]string{},
		debounceSeconds: 60,
	}
}

// collectEmits is a thread-safe helper that returns emitted content strings.
func collectEmits(t *testing.T) (deliver DeliverFn, getEmits func() []string) {
	t.Helper()
	var mu sync.Mutex
	var emits []string
	deliver = func(_, content string) {
		mu.Lock()
		emits = append(emits, content)
		mu.Unlock()
	}
	getEmits = func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(emits))
		copy(out, emits)
		return out
	}
	return
}

// TestRunner_MatchesAndEmits: child emits two matching lines and one non-matching
// line; with a 60-second debounce window only the first match fires leading-edge.
func TestRunner_MatchesAndEmits(t *testing.T) {
	job := defaultJob(t)
	// sh -c is acceptable in test fixtures — the test author controls the input.
	job.argv = []string{"sh", "-c", "echo 'ERROR: boom'; echo 'normal line'; echo 'WARNING: hi'"}
	job.matchPattern = "(ERROR|WARNING)"
	re := regexp.MustCompile(job.matchPattern)

	deliver, getEmits := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	emits := getEmits()
	// With a 60-second window, only the first matching line fires (leading edge).
	// "WARNING: hi" is suppressed because it arrives within the same window.
	require.Len(t, emits, 1, "expected exactly one leading-edge emit within the debounce window")
	assert.Contains(t, emits[0], "ERROR: boom")
}

// TestRunner_TrailingSummaryFires: when a burst of 5 matching lines arrives
// inside a short debounce window, the runner must emit exactly 2 messages:
// the leading-edge match and a trailing summary with the suppressed count.
// This exercises the flushTimer wiring in Run that calls FlushExpired after
// the window elapses. Without the timer, every suppressed match would be
// silently dropped (the review-finding-1 regression).
func TestRunner_TrailingSummaryFires(t *testing.T) {
	job := defaultJob(t)
	// Emit 5 matching lines back-to-back, then sleep long enough for the
	// trailing timer to fire (debounce=1s, so sleep ~1.5s inside the child).
	job.argv = []string{"sh", "-c",
		"echo M1; echo M2; echo M3; echo M4; echo M5; sleep 1.5"}
	job.matchPattern = "^M"
	job.debounceSeconds = 1
	re := regexp.MustCompile(job.matchPattern)

	deliver, getEmits := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	emits := getEmits()
	require.Len(t, emits, 2,
		"expected 2 emits: leading edge + trailing summary, got %d (%v)",
		len(emits), emits)
	assert.Equal(t, "M1", emits[0], "first emit is the leading-edge match")
	assert.Contains(t, emits[1], "M2",
		"trailing summary should contain the first suppressed match")
	assert.Contains(t, emits[1], "+3 more matches suppressed",
		"trailing summary should report the suppressed count")
}

// TestRunner_NonMatchingLinesAreFiltered: a child that emits lines that do NOT
// match the regex should produce zero deliveries.
func TestRunner_NonMatchingLinesAreFiltered(t *testing.T) {
	job := defaultJob(t)
	job.argv = []string{"sh", "-c", "echo 'INFO: heartbeat'; echo 'DEBUG: all good'"}
	job.matchPattern = "^ERROR"
	re := regexp.MustCompile(job.matchPattern)

	deliver, getEmits := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	assert.Empty(t, getEmits(), "non-matching lines must not produce any delivery calls")
}

// TestRunner_MergesStderrIntoFilter: stderr lines are filtered the same as stdout.
func TestRunner_MergesStderrIntoFilter(t *testing.T) {
	job := defaultJob(t)
	job.name = "stderr-test"
	// "out" goes to stdout, "err" goes to stderr — both should be filtered.
	job.argv = []string{"sh", "-c", "echo out; echo err >&2"}
	job.matchPattern = "^err$"
	re := regexp.MustCompile(job.matchPattern)

	deliver, getEmits := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.Run(ctx)

	emits := getEmits()
	require.Len(t, emits, 1, "stderr must be visible to the regex filter")
	assert.Equal(t, "err", emits[0])
}

// TestRunner_OversizedLineTruncated: a line longer than 2048 bytes must be
// truncated by the LineReader to exactly 2048 bytes before regex matching.
// The runner should still match on the truncated content.
func TestRunner_OversizedLineTruncated(t *testing.T) {
	// Build a 3000-char line starting with "ERROR" so it matches after truncation.
	bigLine := "ERROR:" + strings.Repeat("X", 3000)
	job := defaultJob(t)
	job.argv = []string{"sh", "-c", "printf '%s\n' '" + bigLine + "'"}
	job.matchPattern = "^ERROR"
	re := regexp.MustCompile(job.matchPattern)

	deliver, getEmits := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	emits := getEmits()
	require.Len(t, emits, 1, "truncated oversized line should still match and emit once")
	// The truncated-content prefix itself must not exceed the 2KB cap.
	// Per design spec §Line-length cap, the runner appends a
	// "[line truncated, original length ≥ 2048 bytes]" marker after the
	// capped content so the receiver can distinguish truncation from EOL.
	const truncMarker = "[line truncated, original length ≥ 2048 bytes]"
	assert.True(t, strings.HasSuffix(emits[0], truncMarker),
		"truncated match must end with the truncation marker; got %q", emits[0])
	prefix := strings.TrimSuffix(emits[0], "\n"+truncMarker)
	assert.LessOrEqual(t, len(prefix), maxLineBytes,
		"capped content must not exceed the 2KB cap")
}

// TestRunner_ExitNoticeCalledOnChildExit: when the child exits, the runner
// calls exitNotice with the correct exit code and a non-zero duration.
func TestRunner_ExitNoticeCalledOnChildExit(t *testing.T) {
	job := defaultJob(t)
	job.argv = []string{"sh", "-c", "exit 42"}
	job.matchPattern = "."
	re := regexp.MustCompile(job.matchPattern)

	type exitRecord struct {
		name     string
		code     int
		duration time.Duration
	}
	var notices []exitRecord
	var noticeMu sync.Mutex

	exitNotice := func(name string, code int, dur time.Duration, _ string) {
		noticeMu.Lock()
		notices = append(notices, exitRecord{name, code, dur})
		noticeMu.Unlock()
	}

	deliver, _ := collectEmits(t)
	r, err := NewRunner(job, re, exitNotice, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	noticeMu.Lock()
	defer noticeMu.Unlock()
	require.Len(t, notices, 1, "exitNotice must be called exactly once")
	assert.Equal(t, "test", notices[0].name)
	assert.Equal(t, 42, notices[0].code, "exit code must match the child's exit status")
	assert.Greater(t, int64(notices[0].duration), int64(0), "duration must be positive")
}

// TestRunner_CwdIsRespected: the child runs in the requested working directory.
func TestRunner_CwdIsRespected(t *testing.T) {
	dir := t.TempDir()
	job := defaultJob(t)
	job.cwd = dir
	job.argv = []string{"pwd"}
	job.matchPattern = regexp.QuoteMeta(dir)
	re := regexp.MustCompile(job.matchPattern)

	deliver, getEmits := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	emits := getEmits()
	require.Len(t, emits, 1)
	assert.Contains(t, emits[0], dir)
}

// TestRunner_EnvIsolation: runner must use only buildEnv (no os.Environ
// inheritance). We inject a known job.Env value and a daemon-side env var,
// then assert the job value is present and the daemon-side var is absent.
func TestRunner_EnvIsolation(t *testing.T) {
	// Set a daemon-level env var that must NOT appear in the child.
	t.Setenv("THRUM_SHOULD_NOT_LEAK", "definitely_secret_xyz")

	job := defaultJob(t)
	// Echo both the job-provided and the daemon-only variable.
	job.argv = []string{"sh", "-c",
		"echo JOB_VAR=$JOB_VAR; echo DAEMON_VAR=$THRUM_SHOULD_NOT_LEAK"}
	job.env = map[string]string{"JOB_VAR": "present"}
	job.matchPattern = "JOB_VAR=present|DAEMON_VAR=definitely_secret"
	re := regexp.MustCompile(job.matchPattern)

	deliver, getEmits := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	emits := getEmits()
	// "JOB_VAR=present" matches; "DAEMON_VAR=" (empty, not inherited) does not.
	for _, e := range emits {
		assert.NotContains(t, e, "definitely_secret_xyz",
			"daemon env var must not leak into child process environment")
	}
	// At least one emit for JOB_VAR=present.
	found := false
	for _, e := range emits {
		if strings.Contains(e, "JOB_VAR=present") {
			found = true
			break
		}
	}
	assert.True(t, found, "job.Env variable must be present in child environment")
}

// TestRunner_SIGTERMThenSIGKILL: when ctx is canceled, the runner sends
// SIGTERM to the child. If the child ignores SIGTERM (trap '' TERM), the
// runner must send SIGKILL after the 5-second grace period.
//
// To keep the test fast, we override shutdownGrace to 300ms.
func TestRunner_SIGTERMThenSIGKILL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SIGTERM/SIGKILL test in short mode")
	}

	job := defaultJob(t)
	// Child traps SIGTERM and then loops forever — only SIGKILL will end it.
	job.argv = []string{"sh", "-c", "trap '' TERM; while true; do sleep 0.1; done"}
	job.matchPattern = "."
	re := regexp.MustCompile(job.matchPattern)

	deliver, _ := collectEmits(t)
	r, err := NewRunner(job, re, nil, deliver)
	require.NoError(t, err)

	// Override grace period to 300ms so the test doesn't wait the full 5 seconds.
	r.shutdownGrace = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx)
	}()

	// Give the child a moment to start.
	time.Sleep(100 * time.Millisecond)

	// Cancel ctx — this should trigger SIGTERM, then SIGKILL after grace.
	cancel()

	select {
	case err := <-done:
		// Run returns nil even on SIGKILL (we absorb the exit error).
		assert.NoError(t, err, "Run must return without error after SIGKILL")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5 seconds after ctx cancellation")
	}
}

// TestRunner_ExitNoticeContainsTailOutput: the last bytes of stdout are
// forwarded to the exitNotice callback.
func TestRunner_ExitNoticeContainsTailOutput(t *testing.T) {
	job := defaultJob(t)
	job.argv = []string{"sh", "-c", "echo 'LAST_LINE'"}
	job.matchPattern = "." // match everything
	re := regexp.MustCompile(job.matchPattern)

	var tailReceived string
	var tailMu sync.Mutex
	exitNotice := func(_ string, _ int, _ time.Duration, tail string) {
		tailMu.Lock()
		tailReceived = tail
		tailMu.Unlock()
	}

	deliver, _ := collectEmits(t)
	r, err := NewRunner(job, re, exitNotice, deliver)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx))

	tailMu.Lock()
	defer tailMu.Unlock()
	assert.Contains(t, tailReceived, "LAST_LINE",
		"exit-notice tail must contain recent stdout output")
}

// TestRunner_NewRunnerRejectsEmptyArgv verifies that NewRunner validates argv.
func TestRunner_NewRunnerRejectsEmptyArgv(t *testing.T) {
	job := defaultJob(t)
	job.argv = []string{}
	re := regexp.MustCompile(".")
	_, err := NewRunner(job, re, nil, func(_, _ string) {})
	require.Error(t, err)
}

// TestRunner_BuildEnvDoesNotUseOsEnviron: buildEnv output must not contain
// THRUM_SHOULD_NOT_LEAK even if it is set in the test process environment.
func TestRunner_BuildEnvDoesNotUseOsEnviron(t *testing.T) {
	t.Setenv("THRUM_SHOULD_NOT_LEAK", "secret_value_999")

	job := defaultJob(t)
	env := buildEnv(job)
	for _, kv := range env {
		assert.NotContains(t, kv, "THRUM_SHOULD_NOT_LEAK",
			"buildEnv must not include variables from the daemon's full environment")
		assert.NotContains(t, kv, "secret_value_999",
			"daemon environment secrets must not appear in child env")
	}
}

// TestRunner_BuildEnvIncludesJobEnv verifies that job-specific env overrides
// are present in the output of buildEnv.
func TestRunner_BuildEnvIncludesJobEnv(t *testing.T) {
	job := defaultJob(t)
	job.env = map[string]string{
		"MY_TOKEN": "abc123",
		"DEBUG":    "1",
	}
	env := buildEnv(job)
	envMap := make(map[string]string)
	for _, kv := range env {
		idx := strings.Index(kv, "=")
		if idx >= 0 {
			envMap[kv[:idx]] = kv[idx+1:]
		}
	}
	assert.Equal(t, "abc123", envMap["MY_TOKEN"])
	assert.Equal(t, "1", envMap["DEBUG"])
}

// TestRunner_BuildEnvContainsBaseKeys verifies that HOME, USER, LANG, TZ,
// PATH are always present in buildEnv output.
func TestRunner_BuildEnvContainsBaseKeys(t *testing.T) {
	// Override osGetenv for this test to avoid flakiness on CI without HOME set.
	orig := osGetenv
	defer func() { osGetenv = orig }()
	osGetenv = func(key string) string {
		switch key {
		case "HOME":
			return "/test/home"
		case "USER":
			return "tester"
		case "LANG":
			return "en_US.UTF-8"
		case "TZ":
			return "UTC"
		}
		return os.Getenv(key)
	}

	job := defaultJob(t)
	env := buildEnv(job)
	envMap := make(map[string]string)
	for _, kv := range env {
		idx := strings.Index(kv, "=")
		if idx >= 0 {
			envMap[kv[:idx]] = kv[idx+1:]
		}
	}
	assert.Equal(t, "/test/home", envMap["HOME"])
	assert.Equal(t, "tester", envMap["USER"])
	assert.Equal(t, "en_US.UTF-8", envMap["LANG"])
	assert.Equal(t, "UTC", envMap["TZ"])
	assert.NotEmpty(t, envMap["PATH"])
}
