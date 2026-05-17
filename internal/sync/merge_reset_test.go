package sync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// thrum-ychn: MergeAll previously merged JSONL contents at the file level
// but left the local a-sync branch pointer where it was. When a peer daemon
// pushed commits in parallel, the local branch stayed divergent from
// origin/a-sync, so every subsequent commit had a parent that wasn't in
// the remote's history and push kept rejecting as non-fast-forward. These
// tests exercise the new reset-at-end-of-MergeAll behavior: after a
// successful merge the branch fast-forwards to origin/a-sync so the next
// commit is push-ready.

// setupRepoWithRemote creates a bare remote, a local client repo with an
// a-sync branch + worktree, and configures origin → bare. Returns
// (localRepoPath, bareRemotePath).
func setupRepoWithRemote(t *testing.T) (string, string) {
	t.Helper()

	bareDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main")
	cmd.Dir = bareDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("bare init: %v", err)
	}

	repoPath := setupMergeTestRepo(t)

	cmd = exec.Command("git", "remote", "add", "origin", bareDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("add origin: %v", err)
	}

	// Initial push so origin/a-sync exists. Use the sync worktree to push
	// the a-sync branch (already populated by setupMergeTestRepo).
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("initial add: %v", err)
	}
	cmd = exec.Command("git", "commit", "-m", "initial a-sync", "--allow-empty")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	cmd = exec.Command("git", "push", "-u", "origin", "a-sync")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	return repoPath, bareDir
}

// pushFromSecondClone simulates a peer daemon pushing a commit to the same
// bare remote. Creates a second clone, appends to events.jsonl, commits,
// and pushes. Returns the appended event line so callers can assert it
// surfaces locally after merge.
func pushFromSecondClone(t *testing.T, bareDir, extraEvent string) {
	pushFromSecondCloneWithMessage(t, bareDir, extraEvent, "", "")
}

// pushFromSecondCloneWithMessage is the extended variant that also writes a
// messages/<msgFile> file with msgContent, exercising the messages/ merge
// path (merge.go:131-183) through to the reset-at-end step. MessageFile
// and messageContent may be empty to skip the messages write.
func pushFromSecondCloneWithMessage(t *testing.T, bareDir, extraEvent, messageFile, messageContent string) {
	t.Helper()

	peerDir := t.TempDir()
	cmd := exec.Command("git", "clone", "--branch", "a-sync", bareDir, peerDir) //nolint:gosec
	if err := cmd.Run(); err != nil {
		t.Fatalf("clone peer: %v", err)
	}

	if extraEvent != "" {
		eventsPath := filepath.Join(peerDir, "events.jsonl")
		existing, _ := os.ReadFile(eventsPath) //nolint:gosec
		out := string(existing)
		if len(out) > 0 && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += extraEvent + "\n"
		if err := os.WriteFile(eventsPath, []byte(out), 0600); err != nil {
			t.Fatalf("peer write events.jsonl: %v", err)
		}
	}

	if messageFile != "" {
		messagesDir := filepath.Join(peerDir, "messages")
		if err := os.MkdirAll(messagesDir, 0750); err != nil {
			t.Fatalf("peer mkdir messages: %v", err)
		}
		if err := os.WriteFile(filepath.Join(messagesDir, messageFile), []byte(messageContent+"\n"), 0600); err != nil {
			t.Fatalf("peer write messages/%s: %v", messageFile, err)
		}
	}

	cmd = exec.Command("git", "-c", "user.email=peer@test", "-c", "user.name=peer", "add", ".")
	cmd.Dir = peerDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("peer add: %v", err)
	}
	cmd = exec.Command("git", "-c", "user.email=peer@test", "-c", "user.name=peer", "commit", "-m", "peer commit")
	cmd.Dir = peerDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("peer commit: %v", err)
	}
	cmd = exec.Command("git", "push", "origin", "a-sync")
	cmd.Dir = peerDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("peer push: %v", err)
	}
}

// getBranchSHA returns the resolved commit SHA of a ref in a repo.
func getBranchSHA(t *testing.T, repoDir, ref string) string {
	t.Helper()
	out, err := safecmd.Git(context.Background(), repoDir, "rev-parse", ref)
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

// Structural: after MergeAll, local a-sync HEAD == origin/a-sync HEAD.
// Locks in the invariant for future refactor safety.
func TestMerger_MergeAll_ResetsLocalBranchToOriginTip(t *testing.T) {
	repoPath, bareDir := setupRepoWithRemote(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Peer pushes a commit — origin/a-sync now ahead of local.
	pushFromSecondClone(t, bareDir, `{"type":"message.create","timestamp":"2026-02-03T00:00:00Z","message_id":"msg_peer_001","event_id":"evt_peer_001"}`)

	m := NewMerger(repoPath, syncDir, false)
	ctx := context.Background()
	if err := m.Fetch(ctx); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := m.MergeAll(ctx); err != nil {
		t.Fatalf("mergeAll: %v", err)
	}

	localTip := getBranchSHA(t, syncDir, "HEAD")
	remoteTip := getBranchSHA(t, syncDir, "origin/a-sync")

	if localTip != remoteTip {
		t.Errorf("local HEAD (%s) should equal origin/a-sync tip (%s) after MergeAll reset",
			localTip, remoteTip)
	}
}

// Repro: the full CommitAndPush path succeeds when origin advances between
// our last sync and our next push. Pre-fix this entered the rejection
// retry loop and failed after 3 retries; post-fix the merge-reset branches
// us onto origin's tip and the first retry push fast-forwards.
func TestSyncer_CommitAndPush_SucceedsAfterConcurrentRemoteAdvance(t *testing.T) {
	repoPath, bareDir := setupRepoWithRemote(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Peer pushes — origin ahead of local.
	pushFromSecondClone(t, bareDir, `{"type":"message.create","timestamp":"2026-02-03T00:00:01Z","message_id":"msg_peer_002","event_id":"evt_peer_002"}`)

	// Local has a pending change that hasn't been committed yet.
	localEvent := `{"type":"message.create","timestamp":"2026-02-03T00:00:02Z","message_id":"msg_local_001","event_id":"evt_local_001"}`
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	local, _ := os.ReadFile(eventsPath) //nolint:gosec
	out := string(local)
	if len(out) > 0 && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += localEvent + "\n"
	if err := os.WriteFile(eventsPath, []byte(out), 0600); err != nil {
		t.Fatalf("write local event: %v", err)
	}

	// Production path: doSync runs Fetch → MergeAll → CommitAndPush. Simulate
	// that order here so we exercise the realistic flow.
	s := NewSyncer(repoPath, syncDir, false)
	ctx := context.Background()
	if err := s.merger.Fetch(ctx); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := s.merger.MergeAll(ctx); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := s.CommitAndPush(ctx); err != nil {
		t.Fatalf("CommitAndPush should succeed after remote advance; got %v", err)
	}

	// Bare remote should now contain BOTH events.
	peerCheck := t.TempDir()
	cmd := exec.Command("git", "clone", "--branch", "a-sync", bareDir, peerCheck) //nolint:gosec
	if err := cmd.Run(); err != nil {
		t.Fatalf("clone bare for check: %v", err)
	}
	finalEvents, err := os.ReadFile(filepath.Join(peerCheck, "events.jsonl")) //nolint:gosec
	if err != nil {
		t.Fatalf("read final events: %v", err)
	}
	finalStr := string(finalEvents)
	if !strings.Contains(finalStr, "msg_peer_002") {
		t.Errorf("bare remote missing peer event after local push; events:\n%s", finalStr)
	}
	if !strings.Contains(finalStr, "msg_local_001") {
		t.Errorf("bare remote missing local event after push; events:\n%s", finalStr)
	}
}

// Coverage for the messages/ merge path through the reset: a peer pushes
// both an events.jsonl entry AND a messages/*.jsonl file; local has a
// different messages file. After MergeAll, both message files should be
// present locally and HEAD should be at origin/a-sync. Protects against
// regressions in the messages-dedup code path interacting with the reset.
func TestMerger_MergeAll_WithMessagesDir_ResetsAndCopiesRemote(t *testing.T) {
	repoPath, bareDir := setupRepoWithRemote(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Local has its own messages file.
	localMsgDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(localMsgDir, 0750); err != nil {
		t.Fatalf("local mkdir messages: %v", err)
	}
	localMsgEvent := `{"type":"message.create","timestamp":"2026-02-03T00:00:10Z","message_id":"msg_local_X","event_id":"evt_local_X"}`
	if err := os.WriteFile(filepath.Join(localMsgDir, "msg_local_X.jsonl"), []byte(localMsgEvent+"\n"), 0600); err != nil {
		t.Fatalf("write local message file: %v", err)
	}

	// Peer pushes a different messages file + events.jsonl entry.
	peerMsgEvent := `{"type":"message.create","timestamp":"2026-02-03T00:00:11Z","message_id":"msg_peer_X","event_id":"evt_peer_X"}`
	pushFromSecondCloneWithMessage(t, bareDir,
		`{"type":"message.create","timestamp":"2026-02-03T00:00:12Z","message_id":"msg_peer_Y","event_id":"evt_peer_Y"}`,
		"msg_peer_X.jsonl", peerMsgEvent)

	m := NewMerger(repoPath, syncDir, false)
	ctx := context.Background()
	if err := m.Fetch(ctx); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := m.MergeAll(ctx); err != nil {
		t.Fatalf("mergeAll: %v", err)
	}

	// Structural invariant: HEAD == origin/a-sync.
	localTip := getBranchSHA(t, syncDir, "HEAD")
	remoteTip := getBranchSHA(t, syncDir, "origin/a-sync")
	if localTip != remoteTip {
		t.Errorf("HEAD (%s) != origin/a-sync (%s) after messages-path MergeAll", localTip, remoteTip)
	}

	// Peer's message file must be present locally after the merge copied it.
	peerMsgPath := filepath.Join(localMsgDir, "msg_peer_X.jsonl")
	data, err := os.ReadFile(peerMsgPath) //nolint:gosec
	if err != nil {
		t.Fatalf("peer message file should be copied to local after merge: %v", err)
	}
	if !strings.Contains(string(data), "msg_peer_X") {
		t.Errorf("peer messages file content unexpected: %s", string(data))
	}

	// Local's message file must survive the reset (working tree preserved).
	if _, err := os.Stat(filepath.Join(localMsgDir, "msg_local_X.jsonl")); err != nil {
		t.Errorf("local message file should survive reset: %v", err)
	}
}

// localOnly mode must not invoke the git reset — no origin exists and any
// reset attempt would fail or worse, mutate unrelated branch state.
func TestMerger_MergeAll_LocalOnlyDoesNotReset(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Commit an initial a-sync state locally so HEAD is set.
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial", "--allow-empty")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	expectedTip := getBranchSHA(t, syncDir, "HEAD")

	// localOnly=true — no remote; reset must not be attempted.
	m := NewMerger(repoPath, syncDir, true)
	if _, err := m.MergeAll(context.Background()); err != nil {
		// MergeAll may fail in other ways in local-only mode; what we care
		// about is that HEAD didn't move.
		_ = err
	}

	postTip := getBranchSHA(t, syncDir, "HEAD")
	if postTip != expectedTip {
		t.Errorf("local-only MergeAll changed HEAD: before %s, after %s", expectedTip, postTip)
	}
}

// First-ever sync path: origin exists but origin/a-sync does not. The
// reset should silently no-op rather than error, matching the existing
// pattern for fetch errors (merge.go:70-72).
func TestMerger_MergeAll_FirstSyncNoRemoteBranch(t *testing.T) {
	// Bare remote with no a-sync branch pushed yet.
	bareDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main")
	cmd.Dir = bareDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("bare init: %v", err)
	}

	repoPath := setupMergeTestRepo(t)
	cmd = exec.Command("git", "remote", "add", "origin", bareDir) //nolint:gosec
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("add origin: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	// Commit so HEAD exists locally.
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial", "--allow-empty")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	expectedTip := getBranchSHA(t, syncDir, "HEAD")

	m := NewMerger(repoPath, syncDir, false)
	if _, err := m.MergeAll(context.Background()); err != nil {
		t.Fatalf("MergeAll should not error when origin/a-sync doesn't exist yet: %v", err)
	}

	postTip := getBranchSHA(t, syncDir, "HEAD")
	if postTip != expectedTip {
		t.Errorf("MergeAll moved HEAD despite missing origin/a-sync: before %s, after %s",
			expectedTip, postTip)
	}
}
