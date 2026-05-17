package gitbutler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type ExecRunner struct {
	Bin string
}

var ErrCLINotFound = errors.New("gitbutler cli not found")

func (r ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	bin := r.Bin
	if bin == "" {
		bin = "but"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

type Client struct {
	Dir    string
	Runner Runner
}

func NewClient(dir string, runner Runner) *Client {
	return &Client{Dir: dir, Runner: runner}
}

func (c *Client) Status(ctx context.Context) (*WorkspaceStatus, error) {
	var status WorkspaceStatus
	if err := c.runJSON(ctx, &status, "status", "-j"); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *Client) BranchList(ctx context.Context) (*BranchList, error) {
	var branches BranchList
	if err := c.runJSON(ctx, &branches, "branch", "list", "-j", "--all"); err != nil {
		return nil, err
	}
	return &branches, nil
}

func (c *Client) Setup(ctx context.Context, init bool) (*WorkspaceStatus, error) {
	args := []string{"setup"}
	if init {
		args = append(args, "--init")
	}
	return c.mutate(ctx, args...)
}

func (c *Client) Show(ctx context.Context, target string) (string, error) {
	return c.runText(ctx, "show", target)
}

func (c *Client) Diff(ctx context.Context, target string) (string, error) {
	if target == "" {
		return c.runText(ctx, "diff")
	}
	return c.runText(ctx, "diff", target)
}

func (c *Client) Stage(ctx context.Context, changeID, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "stage", changeID, branch)
}

func (c *Client) StageMany(ctx context.Context, changeIDs []string, branch string) (*WorkspaceStatus, error) {
	var status *WorkspaceStatus
	for _, id := range changeIDs {
		if id == "" {
			continue
		}
		next, err := c.Stage(ctx, id, branch)
		if err != nil {
			return status, err
		}
		status = next
	}
	if status != nil {
		return status, nil
	}
	return c.Status(ctx)
}

func (c *Client) Apply(ctx context.Context, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "apply", branch)
}

func (c *Client) Unapply(ctx context.Context, branch string, force bool) (*WorkspaceStatus, error) {
	args := []string{"unapply", branch}
	if force {
		args = append(args, "--force")
	}
	return c.mutate(ctx, args...)
}

func (c *Client) NewBranch(ctx context.Context, name string, anchor string) (*WorkspaceStatus, error) {
	args := []string{"branch", "new"}
	if anchor != "" {
		args = append(args, "--anchor", anchor)
	}
	args = append(args, name)
	return c.mutate(ctx, args...)
}

func (c *Client) DeleteBranch(ctx context.Context, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "branch", "delete", branch)
}

func (c *Client) Reword(ctx context.Context, target, message string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "reword", target, "-m", message)
}

func (c *Client) Commit(ctx context.Context, branch, message string, changeIDs []string, only bool) (*WorkspaceStatus, error) {
	args := []string{"commit", branch, "-m", message}
	if only {
		args = append(args, "--only")
	}
	for _, id := range changeIDs {
		if id != "" {
			args = append(args, "--changes", id)
		}
	}
	return c.mutate(ctx, args...)
}

func (c *Client) Amend(ctx context.Context, changeID, commitID string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "amend", changeID, commitID)
}

func (c *Client) Absorb(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "absorb")
}

func (c *Client) Squash(ctx context.Context, targets ...string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, append([]string{"squash"}, targets...)...)
}

func (c *Client) Uncommit(ctx context.Context, target string, discard bool) (*WorkspaceStatus, error) {
	args := []string{"uncommit", target}
	if discard {
		args = append(args, "--discard")
	}
	return c.mutate(ctx, args...)
}

func (c *Client) Move(ctx context.Context, source, target string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "move", source, target)
}

func (c *Client) Rub(ctx context.Context, source, target string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "rub", source, target)
}

func (c *Client) Pull(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "pull")
}

func (c *Client) Merge(ctx context.Context, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "merge", branch)
}

func (c *Client) PullCheck(ctx context.Context) (string, error) {
	return c.runText(ctx, "pull", "--check")
}

func (c *Client) Push(ctx context.Context, branch string, force bool) (*WorkspaceStatus, error) {
	args := []string{"push", branch}
	if force {
		args = append(args, "--with-force")
	}
	if _, err := c.runText(ctx, args...); err != nil {
		return nil, err
	}
	return c.Status(ctx)
}

func (c *Client) PushDryRun(ctx context.Context, branch string) (string, error) {
	return c.runText(ctx, "push", branch, "--dry-run")
}

func (c *Client) NewPR(ctx context.Context, branch string, draft bool) (string, error) {
	args := []string{"pr", "new", branch, "--default"}
	if draft {
		args = append(args, "--draft")
	}
	return c.runText(ctx, args...)
}

func (c *Client) SetPRDraft(ctx context.Context, selector string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "pr", "set-draft", selector)
}

func (c *Client) SetPRReady(ctx context.Context, selector string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "pr", "set-ready", selector)
}

func (c *Client) ResolveStatus(ctx context.Context) (string, error) {
	return c.runText(ctx, "resolve", "status")
}

func (c *Client) ResolveFinish(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "resolve", "finish")
}

func (c *Client) ResolveCancel(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "resolve", "cancel")
}

func (c *Client) Undo(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "undo")
}

func (c *Client) OplogSnapshot(ctx context.Context, message string) (string, error) {
	return c.runText(ctx, "oplog", "snapshot", "-m", message)
}

// OplogList returns the recent operation history entries. Used to back the
// snapshot restore picker.
func (c *Client) OplogList(ctx context.Context) ([]OplogEntry, error) {
	var entries []OplogEntry
	if err := c.runJSON(ctx, &entries, "oplog", "list", "-j"); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *Client) OplogRestore(ctx context.Context, snapshot string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "oplog", "restore", snapshot, "--force")
}

func (c *Client) CleanDryRun(ctx context.Context) (string, error) {
	return c.runText(ctx, "clean", "--dry-run")
}

func (c *Client) Clean(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "clean")
}

func (c *Client) Discard(ctx context.Context, target string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "discard", target)
}

func (c *Client) runJSON(ctx context.Context, out any, args ...string) error {
	raw, err := c.runner().Run(ctx, c.Dir, args...)
	if err != nil {
		return parseCommandError(raw, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse `but %s`: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (c *Client) runText(ctx context.Context, args ...string) (string, error) {
	raw, err := c.runner().Run(ctx, c.Dir, args...)
	if err != nil {
		return "", parseCommandError(raw, err)
	}
	return string(raw), nil
}

func (c *Client) mutate(ctx context.Context, args ...string) (*WorkspaceStatus, error) {
	args = append(append([]string{}, args...), "-j", "--status-after")
	var wrapped StatusAfter
	if err := c.runJSON(ctx, &wrapped, args...); err != nil {
		return nil, err
	}
	if wrapped.Status != nil {
		return wrapped.Status, nil
	}
	if wrapped.StatusError != nil {
		return nil, wrapped.StatusError
	}
	return c.Status(ctx)
}

func (c *Client) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{Bin: "but"}
}

func parseCommandError(raw []byte, runErr error) error {
	if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, os.ErrNotExist) {
		return fmt.Errorf("GitButler CLI not found: %v: %w", runErr, ErrCLINotFound)
	}
	if cliErr, ok := parseCLIError(raw); ok {
		return cliErr
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return runErr
	}
	return fmt.Errorf("%s: %s", runErr, text)
}

func parseCLIError(raw []byte) (CLIError, bool) {
	var cliErr CLIError
	if err := json.Unmarshal(raw, &cliErr); err == nil && (cliErr.Code != "" || cliErr.Message != "") {
		return cliErr, true
	}
	text := strings.TrimSpace(string(raw))
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(text[start:end+1]), &cliErr); err == nil && (cliErr.Code != "" || cliErr.Message != "") {
			return cliErr, true
		}
	}
	if strings.Contains(text, "setup_required") || strings.Contains(strings.ToLower(text), "setup required") {
		return CLIError{Code: "setup_required", Message: firstNonEmptyLine(text)}, true
	}
	return CLIError{}, false
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func IsCLINotFound(err error) bool {
	return errors.Is(err, ErrCLINotFound)
}

func IsSetupRequired(err error) bool {
	if err == nil {
		return false
	}
	var cliErr CLIError
	if errors.As(err, &cliErr) && cliErr.Code == "setup_required" {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "setup_required") || strings.Contains(text, "setup required")
}
