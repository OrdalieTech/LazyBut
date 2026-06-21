package gitbutler

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	outputs map[string][]byte
	errs    map[string]error
	calls   [][]string
}

func (r *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{}, args...))
	key := strings.Join(args, " ")
	return r.outputs[key], r.errs[key]
}

func TestClientStatusUsesJSON(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: map[string][]byte{"status -j": statusRaw}}
	client := NewClient(".", runner)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.UnassignedChanges[0].CLIID != "ur" {
		t.Fatalf("unexpected status: %#v", status.UnassignedChanges)
	}
	if !reflect.DeepEqual(runner.calls[0], []string{"status", "-j"}) {
		t.Fatalf("calls = %#v", runner.calls)
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

func TestClientMutationUsesStatusAfter(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')
	runner := &fakeRunner{outputs: map[string][]byte{
		"stage ur feature/ui -j --status-after": wrapped,
	}}
	client := NewClient(".", runner)

	status, err := client.Stage(context.Background(), "ur", "feature/ui")
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
	runner := &fakeRunner{outputs: map[string][]byte{
		"pull -j --status-after": []byte(`{"result":{},"status":"updated"}`),
		"status -j":              statusRaw,
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
		{"pull", "-j", "--status-after"},
		{"status", "-j"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClientMutationRejectsMalformedStructuredStatusAfter(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"pull -j --status-after": []byte(`{"result":{},"status":{"stacks":"bad"}}`),
		"status -j":              []byte(`{}`),
	}}
	client := NewClient(".", runner)

	_, err := client.Pull(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse `but pull -j --status-after`") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v, want no fallback status call", runner.calls)
	}
}

func TestClientStageManyRunsSequentialStages(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')
	runner := &fakeRunner{outputs: map[string][]byte{
		"stage a1 feature/ui -j --status-after": wrapped,
		"stage a2 feature/ui -j --status-after": wrapped,
	}}
	client := NewClient(".", runner)

	if _, err := client.StageMany(context.Background(), []string{"a1", "a2"}, "feature/ui"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"stage", "a1", "feature/ui", "-j", "--status-after"},
		{"stage", "a2", "feature/ui", "-j", "--status-after"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
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
		{"apply", "apply feature/ui -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Apply(ctx, "feature/ui")
			return err
		}},
		{"unapply", "unapply feature/ui --force -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Unapply(ctx, "feature/ui", true)
			return err
		}},
		{"new stacked branch", "branch new --anchor feature/ui child -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.NewBranch(ctx, "child", "feature/ui")
			return err
		}},
		{"delete branch", "branch delete feature/ui --force -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.DeleteBranch(ctx, "feature/ui")
			return err
		}},
		{"reword", "reword feature/ui -m renamed -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Reword(ctx, "feature/ui", "renamed")
			return err
		}},
		{"commit", "commit feature/ui -m msg --only --changes a1 --changes a2 -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Commit(ctx, "feature/ui", "msg", []string{"a1", "a2"}, true)
			return err
		}},
		{"amend", "amend a1 c1 -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Amend(ctx, "a1", "c1")
			return err
		}},
		{"absorb", "absorb -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Absorb(ctx)
			return err
		}},
		{"squash", "squash c1 c2 -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Squash(ctx, "c1", "c2")
			return err
		}},
		{"uncommit", "uncommit c1 -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Uncommit(ctx, "c1", false)
			return err
		}},
		{"move", "move c1 feature/ui -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Move(ctx, "c1", "feature/ui")
			return err
		}},
		{"rub", "rub c1 zz -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Rub(ctx, "c1", "zz")
			return err
		}},
		{"pull", "pull -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Pull(ctx)
			return err
		}},
		{"resolve finish", "resolve finish -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.ResolveFinish(ctx)
			return err
		}},
		{"resolve cancel", "resolve cancel -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.ResolveCancel(ctx)
			return err
		}},
		{"undo", "undo -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Undo(ctx)
			return err
		}},
		{"oplog restore", "oplog restore snap --force -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.OplogRestore(ctx, "snap")
			return err
		}},
		{"clean", "clean -j --status-after", func(ctx context.Context, c *Client) error {
			_, err := c.Clean(ctx)
			return err
		}},
		{"discard", "discard a1 -j --status-after", func(ctx context.Context, c *Client) error {
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

func TestClientSetupAndPRActionsUseStatusAfter(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')
	runner := &fakeRunner{outputs: map[string][]byte{
		"setup --init -j --status-after":            wrapped,
		"pr set-ready feature/ui -j --status-after": wrapped,
		"pr set-draft feature/ui -j --status-after": wrapped,
		"merge feature/ui -j --status-after":        wrapped,
		"push feature/ui --dry-run":                 []byte("dry-run ok"),
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
	if _, err := client.Merge(context.Background(), "feature/ui"); err != nil {
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
		"push feature/ui --with-force": []byte("pushed"),
		"status -j":                    statusRaw,
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
		{"push", "feature/ui", "--with-force"},
		{"status", "-j"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClientParsesCLIError(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{"status -j": []byte(`{"error":"setup_required","message":"unable to open database file","hint":"run but setup"}`)},
		errs:    map[string]error{"status -j": errors.New("exit status 1")},
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
		outputs: map[string][]byte{"status -j": []byte(`{
  "error": "setup_required",
  "message": "No GitButler project found at .",
  "hint": "run ` + "`but setup`" + ` to configure the project"
}
Error: Setup required: No GitButler project found at .`)},
		errs: map[string]error{"status -j": errors.New("exit status 1")},
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

func TestParseCommandErrorForOldGitButlerCLI(t *testing.T) {
	raw := []byte("error: unexpected argument '-j' found\n\nUsage: but status [OPTIONS]")
	err := parseCommandError(raw, errors.New("exit status 2"))
	if err == nil || !strings.Contains(err.Error(), "GitButler CLI is too old for LazyBut") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "exit status 2") {
		t.Fatalf("error should hide raw exit status: %v", err)
	}
}

func TestRunJSONFallsBackToFormatJSON(t *testing.T) {
	statusRaw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"status -j":            []byte("error: unexpected argument '-j' found"),
			"status --format json": statusRaw,
		},
		errs: map[string]error{
			"status -j": errors.New("exit status 2"),
		},
	}
	client := NewClient(".", runner)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.UnassignedChanges[0].CLIID != "ur" {
		t.Fatalf("unexpected status: %#v", status.UnassignedChanges)
	}
	want := [][]string{{"status", "-j"}, {"status", "--format", "json"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}
