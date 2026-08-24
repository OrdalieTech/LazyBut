package gitbutler

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	outputs map[string][]byte
	errs    map[string]error
	gate    chan struct{} // when non-nil, Run blocks until the channel is closed

	mu    sync.Mutex
	calls [][]string
}

func (r *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{}, args...))
	r.mu.Unlock()
	key := strings.Join(args, " ")
	return r.outputs[key], r.errs[key]
}

func (r *fakeRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestClientBinAndForgeAuthCommand(t *testing.T) {
	// Default and custom binary names both resolve correctly.
	if got := NewClient(".", &fakeRunner{}).Bin(); got != "but" {
		t.Fatalf("default bin = %q, want but", got)
	}
	c := NewClient("/repo", ExecRunner{Bin: "but-nightly"})
	if got := c.Bin(); got != "but-nightly" {
		t.Fatalf("custom bin = %q", got)
	}
	cmd := c.ForgeAuthCommand(context.Background())
	if cmd.Dir != "/repo" {
		t.Fatalf("auth cmd dir = %q", cmd.Dir)
	}
	if want := []string{"but-nightly", "config", "forge", "auth"}; !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("auth cmd args = %v, want %v", cmd.Args, want)
	}
}

func TestClientStatusUsesJSON(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: map[string][]byte{"status --json": statusRaw}}
	client := NewClient(".", runner)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.UnassignedChanges[0].CLIID != "ur" {
		t.Fatalf("unexpected status: %#v", status.UnassignedChanges)
	}
	if !reflect.DeepEqual(runner.calls[0], []string{"status", "--json"}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestClientStatusReconcilesStaleGitHubPRAndCachesResult(t *testing.T) {
	statusRaw := []byte(`{
		"unassignedChanges": [],
		"stacks": [{
			"cliId": "s1",
			"assignedChanges": [],
			"branches": [{
				"cliId": "b1",
				"name": "glose-os-poc",
				"commits": [],
				"upstreamCommits": [],
				"branchStatus": "nothingToPush",
				"reviewId": "700",
				"reviewUrl": "https://github.com/OrdalieTech/Ordalie-back/pull/700",
				"reviewState": "OPEN",
				"reviewMergedAt": null,
				"mergeStatus": "clean"
			}]
		}],
		"mergeBase": {},
		"upstreamState": {}
	}`)
	butRunner := &fakeRunner{outputs: map[string][]byte{"status --json": statusRaw}}
	ghRunner := &fakeRunner{outputs: map[string][]byte{
		"pr list --state all --json number,url,headRefName,state,mergedAt --limit 1000": []byte(`[{"number":781,"url":"https://github.com/OrdalieTech/Ordalie-back/pull/781","headRefName":"glose-os-poc","state":"MERGED","mergedAt":"2026-07-08T13:08:08Z"}]`),
	}}
	client := NewClient(".", butRunner)
	client.GHRunner = ghRunner

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	branch := status.Stacks[0].Branches[0]
	if branch.ReviewID == nil || *branch.ReviewID != "781" {
		t.Fatalf("review id = %#v, want 781", branch.ReviewID)
	}
	if branch.ReviewURL == nil || *branch.ReviewURL != "https://github.com/OrdalieTech/Ordalie-back/pull/781" {
		t.Fatalf("review url = %#v", branch.ReviewURL)
	}
	if branch.ReviewState == nil || *branch.ReviewState != "MERGED" {
		t.Fatalf("review state = %#v, want MERGED", branch.ReviewState)
	}
	if branch.ReviewMergedAt == nil || *branch.ReviewMergedAt == "" {
		t.Fatalf("review merged at = %#v, want timestamp", branch.ReviewMergedAt)
	}

	if _, err := client.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ghRunner.calls) != 1 {
		t.Fatalf("gh calls = %#v, want cached single call", ghRunner.calls)
	}
}

func TestClientBranchListReconcilesStaleGitHubPR(t *testing.T) {
	branchRaw := []byte(`{
		"appliedStacks": [{"id":"s1","heads":[{"name":"glose-os-poc","reviews":[{"number":700,"url":"https://github.com/OrdalieTech/Ordalie-back/pull/700","state":"OPEN"}]}]}],
		"branches": []
	}`)
	butRunner := &fakeRunner{outputs: map[string][]byte{"branch list --json --all": branchRaw}}
	ghRunner := &fakeRunner{outputs: map[string][]byte{
		"pr list --state all --json number,url,headRefName,state,mergedAt --limit 1000": []byte(`[{"number":781,"url":"https://github.com/OrdalieTech/Ordalie-back/pull/781","headRefName":"glose-os-poc","state":"MERGED","mergedAt":"2026-07-08T13:08:08Z"}]`),
	}}
	client := NewClient(".", butRunner)
	client.GHRunner = ghRunner

	branches, err := client.BranchList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reviews := branches.AppliedStacks[0].Heads[0].Reviews
	if len(reviews) != 1 || reviews[0].Number != 781 || reviews[0].URL != "https://github.com/OrdalieTech/Ordalie-back/pull/781" || reviews[0].State != "MERGED" || reviews[0].MergedAt == "" {
		t.Fatalf("reviews = %#v", reviews)
	}
}

const testGHPRListKey = "pr list --state all --json number,url,headRefName,state,mergedAt --limit 1000"

func testGitHubEnrichableStatusJSON() []byte {
	return []byte(`{
		"unassignedChanges": [],
		"stacks": [{
			"cliId": "s1",
			"assignedChanges": [],
			"branches": [{
				"cliId": "b1",
				"name": "glose-os-poc",
				"commits": [],
				"upstreamCommits": [],
				"branchStatus": "nothingToPush",
				"mergeStatus": "clean"
			}]
		}],
		"mergeBase": {},
		"upstreamState": {}
	}`)
}

func waitForGitHubRefresh(t *testing.T, c *Client) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.githubMu.Lock()
		done := !c.githubRefreshInFlight
		c.githubMu.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("github refresh did not finish")
}

func TestClientGitHubPRStaleCacheRefreshesInBackground(t *testing.T) {
	butRunner := &fakeRunner{outputs: map[string][]byte{"status --json": testGitHubEnrichableStatusJSON()}}
	gate := make(chan struct{})
	ghRunner := &fakeRunner{
		gate: gate,
		outputs: map[string][]byte{
			testGHPRListKey: []byte(`[{"number":781,"url":"https://github.com/OrdalieTech/Ordalie-back/pull/781","headRefName":"glose-os-poc","state":"MERGED","mergedAt":"2026-07-08T13:08:08Z"}]`),
		},
	}
	client := NewClient(".", butRunner)
	client.GHRunner = ghRunner
	client.githubPRs = map[string]Review{"glose-os-poc": {Number: 700, URL: "https://github.com/OrdalieTech/Ordalie-back/pull/700", State: "OPEN"}}
	client.githubPRsExpiresAt = time.Now().Add(-time.Second)

	// Expired cache: both calls serve the stale PR immediately even though the
	// gh runner is still blocked, and the refresh is single-flighted.
	for i := 0; i < 2; i++ {
		status, err := client.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		branch := status.Stacks[0].Branches[0]
		if branch.ReviewID == nil || *branch.ReviewID != "700" {
			t.Fatalf("call %d review id = %#v, want stale 700", i, branch.ReviewID)
		}
	}
	close(gate)
	waitForGitHubRefresh(t, client)
	if got := ghRunner.callCount(); got != 1 {
		t.Fatalf("gh calls = %d, want single-flighted refresh", got)
	}

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	branch := status.Stacks[0].Branches[0]
	if branch.ReviewID == nil || *branch.ReviewID != "781" {
		t.Fatalf("review id = %#v, want refreshed 781", branch.ReviewID)
	}
	if got := ghRunner.callCount(); got != 1 {
		t.Fatalf("gh calls = %d, want cached result", got)
	}
}

func TestClientGitHubPRBackgroundRefreshErrorSetsBackoff(t *testing.T) {
	butRunner := &fakeRunner{outputs: map[string][]byte{"status --json": testGitHubEnrichableStatusJSON()}}
	ghRunner := &fakeRunner{errs: map[string]error{testGHPRListKey: errors.New("exit status 1")}}
	client := NewClient(".", butRunner)
	client.GHRunner = ghRunner
	client.githubPRs = map[string]Review{"glose-os-poc": {Number: 700, URL: "https://github.com/OrdalieTech/Ordalie-back/pull/700", State: "OPEN"}}
	client.githubPRsExpiresAt = time.Now().Add(-time.Second)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	branch := status.Stacks[0].Branches[0]
	if branch.ReviewID == nil || *branch.ReviewID != "700" {
		t.Fatalf("review id = %#v, want stale 700", branch.ReviewID)
	}
	waitForGitHubRefresh(t, client)
	client.githubMu.Lock()
	backoffActive := time.Now().Before(client.githubPRErrorBackoff)
	client.githubMu.Unlock()
	if !backoffActive {
		t.Fatal("expected error backoff after failed background refresh")
	}

	status, err = client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	branch = status.Stacks[0].Branches[0]
	if branch.ReviewID == nil || *branch.ReviewID != "700" {
		t.Fatalf("review id during backoff = %#v, want stale 700", branch.ReviewID)
	}
	if got := ghRunner.callCount(); got != 1 {
		t.Fatalf("gh calls = %d, want no refresh during backoff", got)
	}
}

func TestParseGitChanges(t *testing.T) {
	raw := []byte(" M internal/tui/model.go\x00?? scratch.txt\x00R  new.go\x00old.go\x00UU conflicted.go\x00")
	changes := parseGitChanges(raw)

	want := []FileChange{
		{CLIID: "git:internal/tui/model.go", FilePath: "internal/tui/model.go", ChangeType: "modified"},
		{CLIID: "git:scratch.txt", FilePath: "scratch.txt", ChangeType: "untracked"},
		{CLIID: "git:new.go", FilePath: "new.go", ChangeType: "renamed"},
		{CLIID: "git:conflicted.go", FilePath: "conflicted.go", ChangeType: "conflicted"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
}

func TestClientMutationUsesJSONStatusAfter(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')
	runner := &fakeRunner{outputs: map[string][]byte{
		"commit -b feature/ui -m msg ur --json --status-after": wrapped,
	}}
	client := NewClient(".", runner)

	status, err := client.Commit(context.Background(), "feature/ui", "msg", []string{"ur"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Stacks[0].Branches[0].Name != "feature/ui" {
		t.Fatalf("unexpected status: %#v", status.Stacks)
	}
}

func TestClientMutationAcceptsStringStatusAfter(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')
	runner := &fakeRunner{outputs: map[string][]byte{
		"pull --json --status-after": []byte(`{"result":{},"status":"updated"}`),
		"status --json":              statusRaw,
	}}
	client := NewClient(".", runner)

	status, err := client.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Stacks[0].Branches[0].Name != "feature/ui" {
		t.Fatalf("unexpected status: %#v", status.Stacks)
	}
	want := [][]string{
		{"pull", "--json", "--status-after"},
		{"status", "--json"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClientMutationRejectsMalformedStructuredStatusAfter(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"pull --json --status-after": []byte(`{"result":{},"status":{"stacks":"bad"}}`),
		"status --json":              []byte(`{}`),
	}}
	client := NewClient(".", runner)

	_, err := client.Pull(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse `but pull --json --status-after`") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v, want no fallback status call", runner.calls)
	}
}

func TestClientCommitRefusesZeroChangeIDs(t *testing.T) {
	runner := &fakeRunner{}
	client := NewClient(".", runner)

	_, err := client.Commit(context.Background(), "feature/ui", "msg", []string{"", ""})
	if err == nil || !strings.Contains(err.Error(), "without selected change IDs") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v, want none", runner.calls)
	}
}

func TestClientMutationCommandSurface(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')

	tests := []struct {
		name string
		key  string
		call func(context.Context, *Client) error
	}{
		{"apply", "apply feature/ui --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Apply(ctx, "feature/ui")
			return err
		}},
		{"unapply", "unapply feature/ui --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Unapply(ctx, "feature/ui")
			return err
		}},
		{"new stacked branch", "branch new --anchor feature/ui child --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.NewBranch(ctx, "child", "feature/ui")
			return err
		}},
		{"reword", "reword feature/ui -m renamed --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Reword(ctx, "feature/ui", "renamed")
			return err
		}},
		{"commit", "commit -b feature/ui -m msg a1 a2 --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Commit(ctx, "feature/ui", "msg", []string{"a1", "a2"})
			return err
		}},
		{"amend", "amend -t c1 a1 --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Amend(ctx, "c1", "a1")
			return err
		}},
		{"absorb", "absorb --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Absorb(ctx)
			return err
		}},
		{"squash", "squash c1 -t c2 --use-target-message --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Squash(ctx, "c1", "c2")
			return err
		}},
		{"uncommit", "uncommit c1 --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Uncommit(ctx, "c1")
			return err
		}},
		{"move commit", "move c1 -b feature/ui --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.MoveCommit(ctx, "c1", "feature/ui")
			return err
		}},
		{"stack branch", "move child --above feature/ui --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.MoveBranch(ctx, "child", "feature/ui")
			return err
		}},
		{"unstack branch", "move child --unstack --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.MoveBranch(ctx, "child", "zz")
			return err
		}},
		{"land", "land feature/ui --yes --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Land(ctx, "feature/ui")
			return err
		}},
		{"pull", "pull --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Pull(ctx)
			return err
		}},
		{"resolve finish", "resolve finish --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.ResolveFinish(ctx)
			return err
		}},
		{"resolve cancel", "resolve cancel --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.ResolveCancel(ctx)
			return err
		}},
		{"undo", "undo --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Undo(ctx)
			return err
		}},
		{"oplog restore", "oplog restore snap --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.OplogRestore(ctx, "snap")
			return err
		}},
		{"clean", "clean --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Clean(ctx)
			return err
		}},
		{"discard", "discard a1 --json --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Discard(ctx, "a1")
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{outputs: map[string][]byte{tc.key: wrapped}}
			client := NewClient(".", runner)

			if err := tc.call(context.Background(), client); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(runner.calls[0], " "); got != tc.key {
				t.Fatalf("call = %q, want %q", got, tc.key)
			}
		})
	}
}

func TestClientTextCommandSurface(t *testing.T) {
	tests := []struct {
		name string
		key  string
		call func(context.Context, *Client) (string, error)
	}{
		{"show", "show feature/ui", func(ctx context.Context, c *Client) (string, error) {
			return c.Show(ctx, "feature/ui")
		}},
		{"diff all", "diff --no-tui", func(ctx context.Context, c *Client) (string, error) {
			return c.Diff(ctx, "")
		}},
		{"diff target", "diff a1 --no-tui", func(ctx context.Context, c *Client) (string, error) {
			return c.Diff(ctx, "a1")
		}},
		{"pull check", "pull --check", func(ctx context.Context, c *Client) (string, error) {
			return c.PullCheck(ctx)
		}},
		{"push dry-run", "push feature/ui --dry-run", func(ctx context.Context, c *Client) (string, error) {
			return c.PushDryRun(ctx, "feature/ui")
		}},
		{"new draft pr", "pr new feature/ui --default --draft", func(ctx context.Context, c *Client) (string, error) {
			return c.NewPR(ctx, "feature/ui", true)
		}},
		{"resolve status", "resolve status", func(ctx context.Context, c *Client) (string, error) {
			return c.ResolveStatus(ctx)
		}},
		{"oplog snapshot", "oplog snapshot -m checkpoint", func(ctx context.Context, c *Client) (string, error) {
			return c.OplogSnapshot(ctx, "checkpoint")
		}},
		{"clean dry-run", "clean --dry-run", func(ctx context.Context, c *Client) (string, error) {
			return c.CleanDryRun(ctx)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{outputs: map[string][]byte{tc.key: []byte("ok")}}
			client := NewClient(".", runner)

			out, err := tc.call(context.Background(), client)
			if err != nil {
				t.Fatal(err)
			}
			if out != "ok" {
				t.Fatalf("out = %q", out)
			}
			if got := strings.Join(runner.calls[0], " "); got != tc.key {
				t.Fatalf("call = %q, want %q", got, tc.key)
			}
		})
	}
}

func TestClientDeleteBranchUsesUndoableDiscard(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')
	runner := &fakeRunner{outputs: map[string][]byte{
		"discard feature/ui --json --status-after": wrapped,
	}}
	client := NewClient(".", runner)

	if _, err := client.DeleteBranch(context.Background(), "feature/ui"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"discard", "feature/ui", "--json", "--status-after"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClientSetupAndPRActionsUseStatusAfter(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')
	runner := &fakeRunner{outputs: map[string][]byte{
		"setup --init --json --status-after":            wrapped,
		"pr set-ready feature/ui --json --status-after": wrapped,
		"pr set-draft feature/ui --json --status-after": wrapped,
		"land feature/ui --yes --json --status-after":   wrapped,
		"push feature/ui --dry-run":                     []byte("dry-run ok"),
	}}
	client := NewClient(".", runner)

	if _, err := client.Setup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetPRReady(context.Background(), "feature/ui"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetPRDraft(context.Background(), "feature/ui"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Land(context.Background(), "feature/ui"); err != nil {
		t.Fatal(err)
	}
	out, err := client.PushDryRun(context.Background(), "feature/ui")
	if err != nil {
		t.Fatal(err)
	}
	if out != "dry-run ok" {
		t.Fatalf("dry-run output = %q", out)
	}
}

func TestClientPushRefreshesWithoutStatusAfter(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: map[string][]byte{
		"push feature/ui --skip-force-push-protection": []byte("pushed"),
		"status --json": statusRaw,
	}}
	client := NewClient(".", runner)

	status, err := client.Push(context.Background(), "feature/ui", true)
	if err != nil {
		t.Fatal(err)
	}
	if status.Stacks[0].Branches[0].Name != "feature/ui" {
		t.Fatalf("unexpected status: %#v", status.Stacks)
	}
	want := [][]string{
		{"push", "feature/ui", "--skip-force-push-protection"},
		{"status", "--json"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClientParsesCLIError(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{"status --json": []byte(`{"error":"setup_required","message":"unable to open database file","hint":"run but setup"}`)},
		errs:    map[string]error{"status --json": errors.New("exit status 1")},
	}
	client := NewClient(".", runner)

	_, err := client.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var cliErr CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want CLIError", err)
	}
	if cliErr.Code != "setup_required" {
		t.Fatalf("code = %q", cliErr.Code)
	}
	if !IsSetupRequired(err) {
		t.Fatalf("setup_required helper missed: %v", err)
	}
}

func TestClientParsesMixedCLIErrorOutput(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{"status --json": []byte(`{
  "error": "setup_required",
  "message": "No GitButler project found at .",
  "hint": "run ` + "`but setup`" + ` to configure the project"
}
Error: Setup required: No GitButler project found at .`)},
		errs: map[string]error{"status --json": errors.New("exit status 1")},
	}
	client := NewClient(".", runner)

	_, err := client.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var cliErr CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want CLIError", err)
	}
	if cliErr.Code != "setup_required" || cliErr.Message == "" || cliErr.Hint == "" {
		t.Fatalf("cli error = %#v", cliErr)
	}
}

func TestParseCommandErrorForMissingBut(t *testing.T) {
	err := parseCommandError(nil, os.ErrNotExist)
	if err == nil || !strings.Contains(err.Error(), "GitButler CLI not found") {
		t.Fatalf("error = %v", err)
	}
	if !IsCLINotFound(err) {
		t.Fatalf("cli-not-found helper missed: %v", err)
	}
}
