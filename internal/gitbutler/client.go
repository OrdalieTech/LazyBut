package gitbutler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	out, err := cmd.CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return out, ctxErr
	}
	return out, err
}

type Client struct {
	Dir      string
	Runner   Runner
	GHRunner Runner

	githubMu             sync.Mutex
	githubPRs            map[string]Review
	githubPRsExpiresAt   time.Time
	githubPRErrorBackoff time.Time
}

const (
	githubPRCacheTTL  = time.Minute
	githubPRErrorTTL  = 2 * time.Minute
	githubPRListLimit = "1000"
	githubPRTimeout   = 2 * time.Second
)

func NewClient(dir string, runner Runner) *Client {
	return &Client{Dir: dir, Runner: runner}
}

func (c *Client) Status(ctx context.Context) (*WorkspaceStatus, error) {
	var status WorkspaceStatus
	if err := c.runJSON(ctx, &status, "status", "-j"); err != nil {
		return nil, err
	}
	c.enrichStatusWithGitHubPRs(ctx, &status)
	return &status, nil
}

func (c *Client) GitChanges(ctx context.Context) ([]FileChange, error) {
	raw, err := c.runGit(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseGitChanges(raw), nil
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
	if err := c.runJSON(ctx, &branches, "branch", "list", "-j", "--all"); err != nil {
		return nil, err
	}
	c.enrichBranchListWithGitHubPRs(ctx, &branches)
	return &branches, nil
}

func (c *Client) enrichStatusWithGitHubPRs(ctx context.Context, status *WorkspaceStatus) {
	if status == nil || !statusNeedsGitHubPRs(status) {
		return
	}
	prs := c.githubPullRequests(ctx)
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
			if branch.ReviewID == nil || *branch.ReviewID == "" {
				id := fmt.Sprint(pr.Number)
				branch.ReviewID = &id
			}
			if branch.ReviewURL == nil || *branch.ReviewURL == "" {
				url := pr.URL
				branch.ReviewURL = &url
			}
		}
	}
}

func statusNeedsGitHubPRs(status *WorkspaceStatus) bool {
	for _, stack := range status.Stacks {
		for _, branch := range stack.Branches {
			if branch.Name == "" {
				continue
			}
			if branch.ReviewID == nil || *branch.ReviewID == "" || branch.ReviewURL == nil || *branch.ReviewURL == "" {
				return true
			}
		}
	}
	return false
}

func (c *Client) enrichBranchListWithGitHubPRs(ctx context.Context, branches *BranchList) {
	if branches == nil || !branchListNeedsGitHubPRs(branches) {
		return
	}
	prs := c.githubPullRequests(ctx)
	if len(prs) == 0 {
		return
	}
	for stackIdx := range branches.AppliedStacks {
		for headIdx := range branches.AppliedStacks[stackIdx].Heads {
			head := &branches.AppliedStacks[stackIdx].Heads[headIdx]
			if len(head.Reviews) > 0 {
				continue
			}
			if pr, ok := prs[head.Name]; ok {
				head.Reviews = []Review{pr}
			}
		}
	}
	for branchIdx := range branches.Branches {
		branch := &branches.Branches[branchIdx]
		if len(branch.Reviews) > 0 {
			continue
		}
		if pr, ok := prs[branch.Name]; ok {
			branch.Reviews = []Review{pr}
		}
	}
}

func branchListNeedsGitHubPRs(branches *BranchList) bool {
	for _, stack := range branches.AppliedStacks {
		for _, head := range stack.Heads {
			if head.Name != "" && len(head.Reviews) == 0 {
				return true
			}
		}
	}
	for _, branch := range branches.Branches {
		if branch.Name != "" && len(branch.Reviews) == 0 {
			return true
		}
	}
	return false
}

type githubPullRequest struct {
	Number      uint64 `json:"number"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
}

func (c *Client) githubPullRequests(ctx context.Context) map[string]Review {
	if !c.shouldUseGitHubFallback() {
		return nil
	}

	now := time.Now()
	c.githubMu.Lock()
	if now.Before(c.githubPRsExpiresAt) {
		prs := cloneReviewMap(c.githubPRs)
		c.githubMu.Unlock()
		return prs
	}
	if now.Before(c.githubPRErrorBackoff) {
		c.githubMu.Unlock()
		return nil
	}
	c.githubMu.Unlock()

	ghCtx, cancel := context.WithTimeout(ctx, githubPRTimeout)
	defer cancel()
	var raw []githubPullRequest
	if err := c.runGHJSON(ghCtx, &raw, "pr", "list", "--state", "open", "--json", "number,url,headRefName", "--limit", githubPRListLimit); err != nil {
		c.githubMu.Lock()
		c.githubPRErrorBackoff = time.Now().Add(githubPRErrorTTL)
		c.githubMu.Unlock()
		return nil
	}

	prs := make(map[string]Review, len(raw))
	for _, pr := range raw {
		if pr.HeadRefName == "" || pr.Number == 0 || pr.URL == "" {
			continue
		}
		if _, exists := prs[pr.HeadRefName]; exists {
			continue
		}
		prs[pr.HeadRefName] = Review{Number: pr.Number, URL: pr.URL}
	}

	c.githubMu.Lock()
	c.githubPRs = cloneReviewMap(prs)
	c.githubPRsExpiresAt = time.Now().Add(githubPRCacheTTL)
	c.githubPRErrorBackoff = time.Time{}
	c.githubMu.Unlock()
	return prs
}

func cloneReviewMap(in map[string]Review) map[string]Review {
	if in == nil {
		return nil
	}
	out := make(map[string]Review, len(in))
	for name, review := range in {
		out[name] = review
	}
	return out
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
	// --no-tui is essential: `but diff` can launch an interactive TUI diff
	// viewer when `but.ui.tui` is configured on, which would hijack the terminal
	// out from under LazyBut. We only ever want the plain text diff for the
	// preview pane.
	if target == "" {
		return c.runText(ctx, "diff", "--no-tui")
	}
	return c.runText(ctx, "diff", target, "--no-tui")
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
	// `but branch delete` prompts for confirmation when the branch has unpushed
	// commits. LazyBut runs non-interactively (stdin is /dev/null), where `but`
	// refuses to prompt and the command fails — and the user has *already*
	// confirmed via LazyBut's own Dangerous dialog. --force skips only that
	// prompt; it does not bypass GitButler's structural safety checks (e.g. it
	// still refuses to leave an anonymous segment).
	return c.mutate(ctx, "branch", "delete", branch, "--force")
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
	if err != nil && needsFormatJSONFallback(raw, args) {
		args = formatJSONArgs(args)
		raw, err = c.runner().Run(ctx, c.Dir, args...)
	}
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
	if isUnsupportedJSONFlagError(text) {
		return fmt.Errorf("GitButler CLI is too old for LazyBut: %s. Update `but` with `curl -fsSL https://gitbutler.com/install.sh | sh`, or pass --but-bin to a newer GitButler CLI", firstNonEmptyLine(text))
	}
	return fmt.Errorf("%s: %s", runErr, text)
}

func isUnsupportedJSONFlagError(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "unexpected argument '-j'") ||
		strings.Contains(lower, "unexpected argument \"-j\"")
}

func needsFormatJSONFallback(raw []byte, args []string) bool {
	for _, arg := range args {
		if arg == "-j" {
			return isUnsupportedJSONFlagError(string(raw))
		}
	}
	return false
}

func formatJSONArgs(args []string) []string {
	next := make([]string, 0, len(args)+1)
	for _, arg := range args {
		if arg == "-j" {
			next = append(next, "--format", "json")
			continue
		}
		next = append(next, arg)
	}
	return next
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
