package gitbutler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	// Return at the deadline even if a grandchild (git, ssh) holds the pipes.
	cmd.WaitDelay = time.Second
	// Keep diagnostics out of successful JSON and text output.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "BUT_OUTPUT_FORMAT=human", "BUT_PAGER=cat", "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1")
	out, err := cmd.Output()
	if err != nil {
		out = append(out, stderr.Bytes()...)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return out, ctxErr
	}
	return out, err
}

type Client struct {
	Dir      string
	Runner   Runner
	GHRunner Runner

	diffWithoutNoTUI atomic.Bool

	githubMu              sync.Mutex
	githubPRs             map[string]Review
	githubPRsExpiresAt    time.Time
	githubPRErrorBackoff  time.Time
	githubRefreshInFlight bool
}

const (
	githubPRCacheTTL  = time.Minute
	githubPRErrorTTL  = 2 * time.Minute
	githubPRListLimit = "1000"
	githubPRTimeout   = 8 * time.Second
)

func NewClient(dir string, runner Runner) *Client {
	return &Client{Dir: dir, Runner: runner}
}

// Bin returns the configured `but` binary name (default "but"), so callers can
// run it interactively outside the Runner abstraction (e.g. forge auth).
func (c *Client) Bin() string {
	switch r := c.Runner.(type) {
	case ExecRunner:
		if r.Bin != "" {
			return r.Bin
		}
	case *ExecRunner:
		if r != nil && r.Bin != "" {
			return r.Bin
		}
	}
	return "but"
}

// ForgeAuthCommand builds the interactive `but config forge auth` command. It
// prompts for a device-login or token, so it must run attached to the terminal
// (via tea.Exec), not through the Runner.
func (c *Client) ForgeAuthCommand(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Bin(), "config", "forge", "auth")
	cmd.Dir = c.Dir
	return cmd
}

func (c *Client) Status(ctx context.Context) (*WorkspaceStatus, error) {
	var status WorkspaceStatus
	if err := c.runJSON(ctx, &status, "status", "--json", "--upstream"); err != nil {
		return nil, err
	}
	c.enrichStatusWithGitHubPRs(&status)
	return &status, nil
}

func (c *Client) GitChanges(ctx context.Context) ([]FileChange, error) {
	raw, err := c.runGit(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseGitChanges(raw), nil
}

// GitFetch updates remote-tracking refs directly via git. Older `but` CLIs
// have no fetch command and only fetch during `but pull`, so without this
// the behind/upstream state reported by `but status` goes stale indefinitely.
func (c *Client) GitFetch(ctx context.Context) error {
	_, err := c.runGit(ctx, "fetch", "--quiet")
	return err
}

func (c *Client) GitDiff(ctx context.Context, path string) (string, error) {
	raw, err := c.runGit(ctx, "diff", "--no-ext-diff", "--", path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(raw)) != "" {
		return string(raw), nil
	}
	raw, err = c.runGit(ctx, "diff", "--cached", "--no-ext-diff", "--", path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "(no git diff yet; GitButler status is still loading)\n", nil
	}
	return string(raw), nil
}

func (c *Client) BranchList(ctx context.Context) (*BranchList, error) {
	var branches BranchList
	if err := c.runJSON(ctx, &branches, "branch", "list", "--json", "--all"); err != nil {
		return nil, err
	}
	c.enrichBranchListWithGitHubPRs(&branches)
	return &branches, nil
}

func (c *Client) enrichStatusWithGitHubPRs(status *WorkspaceStatus) {
	if status == nil || !statusHasGitHubPRCandidates(status) {
		return
	}
	prs := c.githubPullRequests()
	if len(prs) == 0 {
		return
	}
	for stackIdx := range status.Stacks {
		for branchIdx := range status.Stacks[stackIdx].Branches {
			branch := &status.Stacks[stackIdx].Branches[branchIdx]
			pr, ok := prs[branch.Name]
			if !ok {
				continue
			}
			id := fmt.Sprint(pr.Number)
			url := pr.URL
			state := pr.State
			mergedAt := pr.MergedAt
			branch.ReviewID = &id
			branch.ReviewURL = &url
			branch.ReviewState = &state
			branch.ReviewMergedAt = &mergedAt
		}
	}
}

func statusHasGitHubPRCandidates(status *WorkspaceStatus) bool {
	for _, stack := range status.Stacks {
		for _, branch := range stack.Branches {
			if branch.Name != "" {
				return true
			}
		}
	}
	return false
}

func (c *Client) enrichBranchListWithGitHubPRs(branches *BranchList) {
	if branches == nil || !branchListHasGitHubPRCandidates(branches) {
		return
	}
	prs := c.githubPullRequests()
	if len(prs) == 0 {
		return
	}
	for stackIdx := range branches.AppliedStacks {
		for headIdx := range branches.AppliedStacks[stackIdx].Heads {
			head := &branches.AppliedStacks[stackIdx].Heads[headIdx]
			if pr, ok := prs[head.Name]; ok {
				head.Reviews = []Review{pr}
			}
		}
	}
	for branchIdx := range branches.Branches {
		branch := &branches.Branches[branchIdx]
		if pr, ok := prs[branch.Name]; ok {
			branch.Reviews = []Review{pr}
		}
	}
}

func branchListHasGitHubPRCandidates(branches *BranchList) bool {
	for _, stack := range branches.AppliedStacks {
		for _, head := range stack.Heads {
			if head.Name != "" {
				return true
			}
		}
	}
	for _, branch := range branches.Branches {
		if branch.Name != "" {
			return true
		}
	}
	return false
}

type githubPullRequest struct {
	Number      uint64 `json:"number"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	State       string `json:"state"`
	MergedAt    string `json:"mergedAt"`
}

// githubPullRequests serves the cached PRs by head branch and refreshes an
// expired cache in the background, so status loads never wait on GitHub.
func (c *Client) githubPullRequests() map[string]Review {
	if !c.shouldUseGitHubFallback() {
		return nil
	}
	c.githubMu.Lock()
	defer c.githubMu.Unlock()
	now := time.Now()
	if !now.Before(c.githubPRsExpiresAt) && !now.Before(c.githubPRErrorBackoff) && !c.githubRefreshInFlight {
		c.githubRefreshInFlight = true
		go c.fetchGitHubPullRequests()
	}
	return maps.Clone(c.githubPRs)
}

func (c *Client) fetchGitHubPullRequests() {
	ctx, cancel := context.WithTimeout(context.Background(), githubPRTimeout)
	defer cancel()
	var raw []githubPullRequest
	err := c.runGHJSON(ctx, &raw, "pr", "list", "--state", "all", "--json", "number,url,headRefName,state,mergedAt", "--limit", githubPRListLimit)
	prs := make(map[string]Review, len(raw))
	for _, pr := range raw {
		if pr.HeadRefName == "" || pr.Number == 0 || pr.URL == "" {
			continue
		}
		if _, exists := prs[pr.HeadRefName]; exists {
			continue
		}
		prs[pr.HeadRefName] = Review{Number: pr.Number, URL: pr.URL, State: pr.State, MergedAt: pr.MergedAt}
	}

	c.githubMu.Lock()
	defer c.githubMu.Unlock()
	c.githubRefreshInFlight = false
	if err != nil {
		c.githubPRErrorBackoff = time.Now().Add(githubPRErrorTTL)
		return
	}
	c.githubPRs = prs
	c.githubPRsExpiresAt = time.Now().Add(githubPRCacheTTL)
	c.githubPRErrorBackoff = time.Time{}
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
	// 0.22.2 removed --no-tui. Older versions need it when their TUI is enabled.
	args := []string{"diff"}
	if !c.diffWithoutNoTUI.Load() {
		args = append(args, "--no-tui")
	}
	if target != "" {
		args = append(args, target)
	}
	out, err := c.runText(ctx, args...)
	if err != nil && strings.Contains(err.Error(), "unexpected argument '--no-tui'") {
		c.diffWithoutNoTUI.Store(true)
		return c.runText(ctx, append([]string{"diff"}, args[2:]...)...)
	}
	return out, err
}

func (c *Client) Apply(ctx context.Context, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "apply", branch)
}

func (c *Client) Unapply(ctx context.Context, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "unapply", branch)
}

func (c *Client) NewBranch(ctx context.Context, name string, anchor string) (*WorkspaceStatus, error) {
	args := []string{"branch", "new"}
	if anchor != "" {
		// --above only exists from 0.22.1; --anchor is deprecated but still accepted.
		args = append(args, "--anchor", anchor)
	}
	args = append(args, name)
	return c.mutate(ctx, args...)
}

func (c *Client) DeleteBranch(ctx context.Context, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "discard", branch)
}

func (c *Client) Reword(ctx context.Context, target, message string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "reword", target, "-m", message)
}

func (c *Client) Commit(ctx context.Context, branch, message string, changeIDs []string) (*WorkspaceStatus, error) {
	args := []string{"commit", "-b", branch, "-m", message}
	hasChange := false
	for _, id := range changeIDs {
		if id != "" {
			args = append(args, id)
			hasChange = true
		}
	}
	if !hasChange {
		return nil, errors.New("refusing to commit without selected change IDs")
	}
	return c.mutate(ctx, args...)
}

func (c *Client) Amend(ctx context.Context, target, source string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "amend", "-t", target, source)
}

func (c *Client) Absorb(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "absorb")
}

func (c *Client) Squash(ctx context.Context, source, target string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "squash", source, "-t", target, "--use-target-message")
}

func (c *Client) Uncommit(ctx context.Context, target string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "uncommit", target)
}

func (c *Client) MoveCommit(ctx context.Context, source, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "move", source, "-b", branch)
}

func (c *Client) MoveBranch(ctx context.Context, source, target string) (*WorkspaceStatus, error) {
	if target == "zz" {
		return c.mutate(ctx, "move", source, "--unstack")
	}
	return c.mutate(ctx, "move", source, "--above", target)
}

func (c *Client) Pull(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "pull")
}

func (c *Client) Land(ctx context.Context, branch string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "land", branch, "--yes")
}

func (c *Client) PullCheck(ctx context.Context) (string, error) {
	return c.runText(ctx, "pull", "--check")
}

func (c *Client) Push(ctx context.Context, branch string, force bool) (*WorkspaceStatus, error) {
	args := []string{"push", branch}
	if force {
		args = append(args, "--skip-force-push-protection")
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
	if err := c.runJSON(ctx, &entries, "oplog", "list", "--json"); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *Client) OplogRestore(ctx context.Context, snapshot string) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "oplog", "restore", snapshot)
}

func (c *Client) CleanDryRun(ctx context.Context) (string, error) {
	return c.runText(ctx, "clean", "--dry-run")
}

func (c *Client) Clean(ctx context.Context) (*WorkspaceStatus, error) {
	return c.mutate(ctx, "clean")
}

func (c *Client) Discard(ctx context.Context, targets ...string) (*WorkspaceStatus, error) {
	args := []string{"discard"}
	for _, target := range targets {
		if target != "" {
			args = append(args, target)
		}
	}
	// `but discard` without targets discards every uncommitted change.
	if len(args) == 1 {
		return nil, errors.New("refusing to discard without selected change IDs")
	}
	return c.mutate(ctx, args...)
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

func (c *Client) runGHJSON(ctx context.Context, out any, args ...string) error {
	raw, err := c.ghRunner().Run(ctx, c.Dir, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse `gh %s`: %w", strings.Join(args, " "), err)
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

func (c *Client) runGit(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.Dir
	// Background fetches must fail rather than prompt over the TUI, and return
	// at the deadline even if a credential helper or ssh holds the pipes.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	raw, err := cmd.CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return raw, ctxErr
	}
	if err != nil {
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return raw, err
		}
		return raw, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return raw, nil
}

func (c *Client) mutate(ctx context.Context, args ...string) (*WorkspaceStatus, error) {
	args = append(append([]string{}, args...), "--json", "--status-after")
	raw, err := c.runner().Run(ctx, c.Dir, args...)
	if err != nil {
		return nil, parseCommandError(raw, err)
	}
	// `land` and `setup` succeed without printing JSON, and a failed
	// status-after is reported as a `status_error` string: reload either way.
	if len(bytes.TrimSpace(raw)) == 0 {
		return c.Status(ctx)
	}
	var wrapped StatusAfter
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("parse `but %s`: %w", strings.Join(args, " "), err)
	}
	if wrapped.Status != nil {
		return wrapped.Status, nil
	}
	return c.Status(ctx)
}

func (c *Client) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{Bin: "but"}
}

func (c *Client) ghRunner() Runner {
	if c.GHRunner != nil {
		return c.GHRunner
	}
	return ExecRunner{Bin: "gh"}
}

func (c *Client) shouldUseGitHubFallback() bool {
	if c.GHRunner != nil || c.Runner == nil {
		return true
	}
	_, ok := c.Runner.(ExecRunner)
	if ok {
		return true
	}
	_, ok = c.Runner.(*ExecRunner)
	return ok
}

func parseCommandError(raw []byte, runErr error) error {
	if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, os.ErrNotExist) {
		return fmt.Errorf("GitButler CLI not found: %v: %w", runErr, ErrCLINotFound)
	}
	if errors.Is(runErr, context.DeadlineExceeded) {
		return fmt.Errorf("GitButler command timed out; press r to retry")
	}
	if strings.Contains(strings.ToLower(runErr.Error()), "signal: killed") {
		return fmt.Errorf("GitButler command was killed; press r to retry")
	}
	if cliErr, ok := parseCLIError(raw); ok {
		return cliErr
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return runErr
	}
	// `but` explains itself on stderr; "exit status 1" in front is noise.
	return errors.New(text)
}

func parseGitChanges(raw []byte) []FileChange {
	entries := bytes.Split(raw, []byte{0})
	changes := make([]FileChange, 0, len(entries))
	for idx := 0; idx < len(entries); idx++ {
		entry := entries[idx]
		if len(entry) < 4 {
			continue
		}
		code := string(entry[:2])
		path := string(entry[3:])
		if path == "" {
			continue
		}
		if code[0] == 'R' || code[0] == 'C' {
			idx++ // porcelain -z stores the old path in the next NUL entry.
		}
		changes = append(changes, FileChange{
			CLIID:      "git:" + path,
			FilePath:   path,
			ChangeType: StatusText(gitChangeType(code)),
		})
	}
	return changes
}

func gitChangeType(code string) string {
	if strings.Contains(code, "U") {
		return "conflicted"
	}
	switch {
	case code == "??":
		return "untracked"
	case strings.ContainsAny(code, "R"):
		return "renamed"
	case strings.ContainsAny(code, "C"):
		return "copied"
	case strings.ContainsAny(code, "D"):
		return "deleted"
	case strings.ContainsAny(code, "A"):
		return "added"
	case strings.ContainsAny(code, "M"):
		return "modified"
	}
	return strings.TrimSpace(code)
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
