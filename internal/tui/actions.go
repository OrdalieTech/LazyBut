package tui

import "github.com/OrdalieTech/LazyBut/internal/gitbutler"

func (m Model) availableActions() []action {
	lane, hasLane := m.selectedLane()
	item, hasItem := m.selectedContent()
	isBranch := hasLane && lane.Kind == laneAppliedBranch
	isAppliedBranch := hasLane && lane.Kind == laneAppliedBranch
	isChange := hasItem && item.Kind == contentChange && item.ID != ""
	isCommit := hasItem && (item.Kind == contentCommit || item.Kind == contentUpstreamCommit) && item.ID != ""

	if m.data.Status == nil {
		actions := []action{
			{ID: actionRefresh, Key: "r", Aliases: []string{"ctrl+r"}, Label: "refresh"},
		}
		if gitbutler.IsCLINotFound(m.err) {
			return append(actions, action{
				ID:          actionInstallGitButler,
				Key:         "i",
				Label:       "install GitButler CLI",
				ConfirmText: installGitButlerConfirmText(),
			})
		}
		return append(actions,
			action{ID: actionSetup, Key: "g", Label: "setup GitButler", ConfirmText: setupGitButlerConfirmText(m.err)},
			action{ID: actionSetupInit, Key: "G", Label: "init and setup GitButler", Dangerous: true, ConfirmText: "Run `but setup --init` here?"},
		)
	}

	actions := []action{
		{ID: actionRefresh, Key: "r", Aliases: []string{"ctrl+r"}, Label: "refresh"},
		{ID: actionAddBranch, Key: "+", Aliases: []string{"B"}, Label: "add branch"},
		{ID: actionNewBranch, Key: "n", Label: "new branch", InputLabel: "branch name"},
		{ID: actionPullCheck, Key: "u", Label: "check upstream update"},
		{ID: actionPull, Key: "U", Label: "update from upstream", ConfirmText: m.upstreamUpdateConfirmText()},
	}
	actions = append(actions,
		action{ID: actionUndo, Key: "z", Label: "undo last GitButler operation", Dangerous: true, ConfirmText: "Undo the last GitButler operation?"},
		action{ID: actionCleanDryRun, Key: "C", Label: "clean dry-run"},
		action{ID: actionClean, Key: "K", Label: "clean empty branches", Dangerous: true, ConfirmText: "Remove empty branches from the workspace?"},
		action{ID: actionResolveStatus, Key: "R", Label: "resolve status"},
	)

	if isBranch {
		actions = append(actions,
			action{ID: actionApplyToggle, Key: "a", Label: "unapply branch", ConfirmText: "Unapply this branch from the workspace?"},
			action{ID: actionRename, Key: "e", Label: "rename branch", InputLabel: "new name"},
			action{ID: actionNewStacked, Key: "N", Label: "new stacked branch", InputLabel: "new branch name"},
			action{ID: actionPush, Key: "P", Label: "push branch", ConfirmText: "Push the selected branch?"},
			action{ID: actionPushDryRun, Key: "Y", Label: "push dry-run"},
			action{ID: actionForcePush, Key: "F", Label: "force push branch", Dangerous: true, ConfirmText: "Force push the selected branch?"},
			action{ID: actionNewPR, Key: "p", Label: "create PR"},
			action{ID: actionNewDraftPR, Key: "ctrl+p", Label: "create draft PR"},
			action{ID: actionPRDraft, Key: "T", Label: "set PR draft", ConfirmText: "Mark the selected branch review as draft?"},
			action{ID: actionPRReady, Key: "W", Label: "set PR ready", ConfirmText: "Mark the selected branch review as ready?"},
			action{ID: actionMerge, Key: "ctrl+m", Label: "merge branch into target", ConfirmText: "Merge selected branch into the target branch?"},
			action{ID: actionDelete, Key: "D", Label: "delete branch", Dangerous: true, ConfirmText: "Delete this branch?"},
			action{ID: actionMove, Key: "M", Label: "move selected branch/commit", InputLabel: "target branch, commit, or zz"},
			action{ID: actionRub, Key: "b", Label: "rub selected item into target", InputLabel: "target branch, commit, or zz"},
		)
	}

	if isAppliedBranch {
		actions = append(actions,
			action{ID: actionCommit, Key: "c", Label: "commit branch changes", InputLabel: "commit message"},
			action{ID: actionAbsorb, Key: "ctrl+a", Label: "absorb changes into commits", ConfirmText: "Run `but absorb`?"},
			action{ID: actionSnapshot, Key: "s", Label: "oplog snapshot", InputLabel: "snapshot message"},
			action{ID: actionRestore, Key: "S", Label: "restore oplog snapshot", Dangerous: true, ConfirmText: "Restore this snapshot? Uncommitted changes will be replaced."},
			action{ID: actionResolveFinish, Key: "f", Label: "finish resolve", ConfirmText: "Finish current conflict resolution?"},
			action{ID: actionResolveCancel, Key: "x", Label: "cancel resolve", Dangerous: true, ConfirmText: "Cancel current conflict resolution?"},
		)
	}

	if isChange {
		actions = append(actions,
			action{ID: actionStage, Key: "m", Aliases: []string{"a"}, Label: "assign/stage change to branch", InputLabel: "target branch"},
			action{ID: actionDiscard, Key: "d", Aliases: []string{"X"}, Label: "discard selected change", Dangerous: true, ConfirmText: "Discard the selected file or hunk?"},
			action{ID: actionAmend, Key: "A", Aliases: []string{"i"}, Label: "amend change into commit", InputLabel: "target commit id"},
		)
	}

	if isCommit {
		actions = append(actions,
			action{ID: actionUncommit, Key: "o", Label: "uncommit selected commit", Dangerous: true, ConfirmText: "Move the selected commit back to unassigned changes?"},
			action{ID: actionSquash, Key: "Q", Label: "squash commits", ConfirmText: "Squash these commits? This rewrites history."},
			action{ID: actionMove, Key: "M", Label: "move selected commit", InputLabel: "target branch or commit"},
			action{ID: actionRub, Key: "b", Label: "rub selected commit", InputLabel: "target branch, commit, or zz"},
		)
	}

	return dedupeActions(actions)
}

func dedupeActions(actions []action) []action {
	seen := map[actionID]bool{}
	out := make([]action, 0, len(actions))
	for _, action := range actions {
		if seen[action.ID] {
			continue
		}
		seen[action.ID] = true
		out = append(out, action)
	}
	return out
}
