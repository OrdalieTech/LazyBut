package tui

import "github.com/OrdalieTech/LazyBut/internal/gitbutler"

func (m Model) availableActions() []action {
	lane, hasLane := m.selectedLane()
	item, hasItem := m.selectedContent()
	isBranch := hasLane && lane.Kind == laneAppliedBranch
	isChange := hasItem && item.Kind == contentChange && item.ID != ""
	isCommit := hasItem && item.Kind == contentCommit && item.ID != ""

	if m.data.Status == nil {
		actions := []action{
			{ID: actionRefresh, Key: "r", Aliases: []string{"ctrl+r"}, Label: "refresh"},
		}
		if m.err == nil {
			return actions
		}
		if gitbutler.IsCLINotFound(m.err) {
			return append(actions, action{
				ID:          actionInstallGitButler,
				Key:         "i",
				Label:       "install GitButler CLI",
				ConfirmText: installGitButlerConfirmText(),
			})
		}
		if gitbutler.IsSetupRequired(m.err) {
			return append(actions,
				action{ID: actionSetup, Key: "g", Label: "setup GitButler", ConfirmText: setupGitButlerConfirmText(m.err)},
				action{ID: actionSetupInit, Key: "G", Label: "init and setup GitButler", Dangerous: true, ConfirmText: "Run `but setup --init` here?"},
			)
		}
		return actions
	}

	actions := []action{
		{ID: actionRefresh, Key: "r", Aliases: []string{"ctrl+r"}, Label: "refresh"},
		{ID: actionAddBranch, Key: "+", Aliases: []string{"B"}, Label: "add branch"},
		{ID: actionNewBranch, Key: "n", Label: "new branch", InputLabel: "branch name"},
		{ID: actionPullCheck, Key: "u", Label: "check upstream update"},
		{ID: actionPull, Key: "p", Label: "update from upstream", ConfirmText: m.upstreamUpdateConfirmText()},
	}
	actions = append(actions,
		action{ID: actionUndo, Key: "z", Label: "undo last GitButler operation", Dangerous: true, ConfirmText: "Undo the last GitButler operation?"},
		action{ID: actionCleanDryRun, Key: "C", Label: "clean dry-run"},
		action{ID: actionClean, Key: "K", Label: "clean empty branches", Dangerous: true, ConfirmText: "Remove empty branches from the workspace?"},
		action{ID: actionResolveStatus, Key: "R", Label: "resolve status"},
		action{ID: actionForgeAuth, Key: "ctrl+g", Label: "authenticate GitHub (forge)", ConfirmText: forgeAuthConfirmAction().ConfirmText},
	)

	if isBranch {
		actions = append(actions,
			action{ID: actionApplyToggle, Key: "a", Label: "unapply branch", ConfirmText: "Unapply this branch from the workspace?"},
			action{ID: actionRename, Key: "e", Label: "rename branch", InputLabel: "new name"},
			action{ID: actionNewStacked, Key: "N", Label: "new stacked branch", InputLabel: "new branch name"},
			action{ID: actionPush, Key: "P", Label: "push branch", ConfirmText: "Push the selected branch?"},
			action{ID: actionPushDryRun, Key: "Y", Label: "push dry-run"},
			action{ID: actionForcePush, Key: "F", Label: "force push branch", Dangerous: true, ConfirmText: "Force push the selected branch?"},
			action{ID: actionNewPR, Key: "o", Label: "create PR"},
			action{ID: actionNewDraftPR, Key: "O", Label: "create draft PR"},
			action{ID: actionPRDraft, Key: "T", Label: "set PR draft", ConfirmText: "Mark the selected branch review as draft?"},
			action{ID: actionPRReady, Key: "W", Label: "set PR ready", ConfirmText: "Mark the selected branch review as ready?"},
			action{ID: actionCopyPRURL, Key: "ctrl+o", Label: "copy PR URL"},
			action{ID: actionLand, Key: "alt+m", Label: "land branch into target", ConfirmText: "Land selected branch into the target with `but land --yes`?"},
			action{ID: actionDelete, Key: "D", Label: "delete branch", Dangerous: true, ConfirmText: "Delete this branch?"},
		)
		if m.focus == panelLanes && len(m.moveTargetItems(false)) > 0 {
			actions = append(actions, action{ID: actionMove, Key: "M", Label: "stack or unstack selected branch", InputLabel: "target branch or zz"})
		}
	}

	if isBranch {
		actions = append(actions,
			action{ID: actionAbsorb, Key: "ctrl+a", Label: "absorb changes into commits", ConfirmText: "Run `but absorb`?"},
			action{ID: actionSnapshot, Key: "s", Label: "oplog snapshot", InputLabel: "snapshot message"},
			action{ID: actionRestore, Key: "S", Label: "restore oplog snapshot", Dangerous: true, ConfirmText: "Restore this snapshot? Uncommitted changes will be replaced."},
			action{ID: actionResolveFinish, Key: "f", Label: "finish resolve", ConfirmText: "Finish current conflict resolution?"},
			action{ID: actionResolveCancel, Key: "x", Label: "cancel resolve", Dangerous: true, ConfirmText: "Cancel current conflict resolution?"},
		)
	}

	if isChange {
		actions = append(actions,
			action{ID: actionDiscard, Key: "d", Aliases: []string{"X"}, Label: "discard selected change", Dangerous: true, ConfirmText: "Discard the selected file or hunk?"},
			action{ID: actionAmend, Key: "A", Aliases: []string{"i"}, Label: "amend change into commit", InputLabel: "target commit id"},
		)
		if _, ok := m.commitBranch(); ok || len(m.branchItems()) > 0 {
			actions = append(actions, action{ID: actionCommit, Key: "c", Label: "commit selected change(s)", InputLabel: "commit message"})
		}
	}

	if isCommit {
		actions = append(actions,
			action{ID: actionUncommit, Key: "U", Label: "uncommit selected commit", Dangerous: true, ConfirmText: "Move the selected commit back to unassigned changes?"},
		)
		if m.focus != panelLanes && len(m.moveTargetItems(true)) > 0 {
			actions = append(actions, action{ID: actionMove, Key: "M", Label: "move selected commit", InputLabel: "target branch"})
		}
		if len(m.commitItems()) > 1 {
			actions = append(actions, action{ID: actionSquash, Key: "Q", Label: "squash commit into target", ConfirmText: "Squash the selected commit into this target? This rewrites history."})
		}
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
