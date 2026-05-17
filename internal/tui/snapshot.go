package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

// Snapshot renders one frame at the given size. When stdout is not a real
// terminal lipgloss strips ANSI colors, which is useless for offline
// inspection — force a 256-color profile so the output stays styled.
func Snapshot(client *gitbutler.Client, width, height int) string {
	return SnapshotMode(client, width, height, "")
}

// SnapshotMode renders one frame at the given size, optionally with an
// overlay active (help, confirm, palette, branch). Used for visual review.
func SnapshotMode(client *gitbutler.Client, width, height int, overlay string) string {
	lipgloss.SetColorProfile(termenv.ANSI256)

	model := newModel(client)
	model.width = width
	model.height = height

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, err := client.Status(ctx)
	if err != nil {
		model.err = err
		model.loading = false
		model = model.maybePromptForBootstrap(err)
		return model.View()
	}
	branches, err := client.BranchList(ctx)
	if err != nil {
		model.err = err
		model.loading = false
		model.data = buildWorkspaceData(status, nil)
		return model.View()
	}
	model.loading = false
	model.data = buildWorkspaceData(status, branches)
	model, _ = model.withPreview()
	// Synchronously fetch the preview so snapshots are not stuck on "loading...".
	if target := model.previewSelectionTarget(); target != "" {
		pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pcancel()
		var body string
		var perr error
		if len(target) > 5 && target[:5] == "show:" {
			body, perr = client.Show(pctx, target[5:])
		} else {
			body, perr = client.Diff(pctx, target)
		}
		model.preview = body
		model.previewErr = perr
		model.previewTarget = target
	}
	switch overlay {
	case "help":
		model.mode = modeHelp
	case "confirm":
		model.mode = modeConfirm
		model.confirm = confirmState{Action: action{Label: "delete branch", ConfirmText: "Delete this branch?", Dangerous: true}}
	case "prompt":
		model.mode = modeInput
		model.prompt = promptState{Action: action{Label: "commit", InputLabel: "commit message"}, Value: "wip: tidy"}
	case "palette":
		model.mode = modePalette
		model.palette = model.availableActions()
	case "branch":
		model.mode = modeBranchPicker
	case "picker":
		// Synthesise a stage-to-branch picker for visual review.
		model.mode = modeTargetPicker
		items := model.branchItems()
		model.targetPicker = targetPickerState{
			Title:  "assign to branch",
			Action: action{ID: actionStage, Label: "assign/stage change to branch"},
			Items:  items,
		}
	case "loading":
		model.loading = true
		model.spinnerFrame = 3
	case "toast":
		model.setToast("committed", toastSuccess)
	case "error":
		model.setToast("but: command failed", toastError)
	case "upstream":
		model.mode = modeConfirm
		model.confirm = confirmState{Action: action{ID: actionPull, Label: "update from upstream"}}
	}
	return model.View()
}
