package tui

import "github.com/OrdalieTech/LazyBut/internal/gitbutler"

type laneKind int

const (
	laneUnassigned laneKind = iota
	laneAppliedBranch
)

type lane struct {
	Key           string
	ID            string
	Name          string
	Kind          laneKind
	Depth         int
	Applied       bool
	ChangeCount   int
	CommitCount   int
	UpstreamCount int    // commits available to pull from this branch's remote
	PushStatus    string // raw branchStatus enum: nothingToPush / unpushedCommits / unpushedCommitsRequiringForce / completelyUnpushed / integrated
	Ahead         *int
	MergeClean    *bool
	CIPending     int // counts from Branch.CI for inline check badges
	CIPassing     int
	CIFailing     int
	CIPresent     bool
	ReviewID      string // pull-request id parsed from branch.ReviewID
}

type branchOption struct {
	Name       string
	Ahead      *int
	MergeClean *bool
	HasLocal   bool
}

type contentKind int

const (
	contentSummary contentKind = iota
	contentChange
	contentCommit
	contentUpstreamCommit
)

type contentItem struct {
	Key        string
	ID         string
	Kind       contentKind
	Label      string
	Detail     string
	Conflicted bool   // commits flagged as conflicted by GitButler
	ReviewID   string // per-commit PR id when available
	Hash       string // full commit SHA (commits only)
	Author     string // commit author name (commits only)
	Created    string // commit createdAt timestamp, raw (commits only)
}

type workspaceData struct {
	Status        *gitbutler.WorkspaceStatus
	Branches      *gitbutler.BranchList
	Fast          bool
	FastChanges   []gitbutler.FileChange
	Lanes         []lane
	BranchOptions []branchOption
}

func buildWorkspaceData(status *gitbutler.WorkspaceStatus, branches *gitbutler.BranchList) workspaceData {
	data := workspaceData{Status: status, Branches: branches}
	if status == nil {
		return data
	}

	data.Lanes = append(data.Lanes, lane{
		Key:         "zz",
		ID:          "zz",
		Name:        "unassigned changes",
		Kind:        laneUnassigned,
		Applied:     true,
		ChangeCount: len(status.UnassignedChanges),
	})

	applied := map[string]bool{}
	for _, stack := range status.Stacks {
		for branchIdx, branch := range stack.Branches {
			key := branch.CLIID
			if key == "" {
				key = branch.Name
			}
			applied[branch.Name] = true
			mergeClean := branch.MergeStatus.String() != "conflicted"
			ln := lane{
				Key:           key,
				ID:            branch.CLIID,
				Name:          branch.Name,
				Kind:          laneAppliedBranch,
				Depth:         branchIdx,
				Applied:       true,
				ChangeCount:   len(stack.AssignedChanges),
				CommitCount:   len(branch.Commits),
				UpstreamCount: len(branch.UpstreamCommits),
				PushStatus:    branch.BranchStatus.String(),
				MergeClean:    &mergeClean,
			}
			if branch.CI != nil {
				ln.CIPresent = true
				ln.CIPending = len(branch.CI.Pending)
				ln.CIPassing = len(branch.CI.Passing)
				ln.CIFailing = len(branch.CI.Failing)
			}
			if branch.ReviewID != nil {
				ln.ReviewID = *branch.ReviewID
			}
			data.Lanes = append(data.Lanes, ln)
		}
	}

	data.BranchOptions = buildBranchOptions(branches, applied)
	return data
}

func buildFastWorkspaceData(changes []gitbutler.FileChange) workspaceData {
	return workspaceData{
		Fast:        true,
		FastChanges: changes,
		Lanes: []lane{{
			Key:         "zz",
			ID:          "zz",
			Name:        "local changes (git)",
			Kind:        laneUnassigned,
			Applied:     true,
			ChangeCount: len(changes),
		}},
	}
}

func buildBranchOptions(branches *gitbutler.BranchList, applied map[string]bool) []branchOption {
	if branches == nil {
		return nil
	}
	for _, stack := range branches.AppliedStacks {
		for _, head := range stack.Heads {
			applied[head.Name] = true
		}
	}
	options := make([]branchOption, 0, len(branches.Branches))
	for _, branch := range branches.Branches {
		if applied[branch.Name] {
			continue
		}
		options = append(options, branchOption{
			Name:       branch.Name,
			Ahead:      branch.CommitsAhead,
			MergeClean: branch.MergesCleanly,
			HasLocal:   branch.HasLocal,
		})
	}
	return options
}

func (d workspaceData) ContentFor(index int) []contentItem {
	if len(d.Lanes) == 0 || index < 0 || index >= len(d.Lanes) {
		return nil
	}
	selected := d.Lanes[index]
	if d.Status == nil && d.Fast && selected.Kind == laneUnassigned {
		if len(d.FastChanges) == 0 {
			return []contentItem{{Kind: contentSummary, Label: "no file changes from git", Detail: "loading GitButler workspace..."}}
		}
		return changesToContent(d.FastChanges)
	}
	if d.Status == nil {
		return nil
	}
	switch selected.Kind {
	case laneUnassigned:
		return changesToContent(d.Status.UnassignedChanges)
	case laneAppliedBranch:
		stack, branch, ok := d.findAppliedBranch(selected.Name)
		if !ok {
			return []contentItem{{Kind: contentSummary, Label: selected.Name, Detail: "branch not found in status"}}
		}
		items := changesToContent(stack.AssignedChanges)
		for _, commit := range branch.Commits {
			items = append(items, commitToContent(commit, contentCommit))
		}
		for _, commit := range branch.UpstreamCommits {
			items = append(items, commitToContent(commit, contentUpstreamCommit))
		}
		if len(items) == 0 {
			items = append(items, contentItem{Kind: contentSummary, Label: selected.Name, Detail: "no assigned changes or commits"})
		}
		return items
	default:
		return nil
	}
}

func (d workspaceData) findAppliedBranch(name string) (gitbutler.Stack, gitbutler.Branch, bool) {
	if d.Status == nil {
		return gitbutler.Stack{}, gitbutler.Branch{}, false
	}
	for _, stack := range d.Status.Stacks {
		for _, branch := range stack.Branches {
			if branch.Name == name {
				return stack, branch, true
			}
		}
	}
	return gitbutler.Stack{}, gitbutler.Branch{}, false
}

func commitToContent(commit gitbutler.Commit, kind contentKind) contentItem {
	label := commit.Message
	if label == "" {
		label = "(no commit message)"
	}
	keyPrefix := "commit:"
	detail := commit.CommitID
	if kind == contentUpstreamCommit {
		keyPrefix = "upstream:"
		detail = "upstream " + commit.CommitID
	}
	return contentItem{
		Key:        keyPrefix + firstNonEmpty(commit.CLIID, commit.CommitID),
		ID:         firstNonEmpty(commit.CLIID, commit.CommitID),
		Kind:       kind,
		Label:      label,
		Detail:     detail,
		Conflicted: commit.Conflicted != nil && *commit.Conflicted,
		ReviewID:   derefString(commit.ReviewID),
		Hash:       commit.CommitID,
		Author:     commit.AuthorName,
		Created:    commit.CreatedAt,
	}
}

func changesToContent(changes []gitbutler.FileChange) []contentItem {
	items := make([]contentItem, 0, len(changes))
	for _, change := range changes {
		items = append(items, contentItem{
			Key:    "change:" + change.CLIID,
			ID:     change.CLIID,
			Kind:   contentChange,
			Label:  change.FilePath,
			Detail: change.ChangeType.String(),
		})
	}
	return items
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
