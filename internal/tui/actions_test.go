package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

type actionRunner struct {
	outputs map[string][]byte
	calls   [][]string
}

func (r *actionRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{}, args...))
	return r.outputs[strings.Join(args, " ")], nil
}

func wrapStatusAfter(t *testing.T, status *gitbutler.WorkspaceStatus) []byte {
	t.Helper()
	wrapped := append([]byte(`{"result":{},"status":`), rawStatus(t, status)...)
	return append(wrapped, '}')
}

func rawStatus(t *testing.T, status *gitbutler.WorkspaceStatus) []byte {
	t.Helper()
	statusRaw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	return statusRaw
}

func markFixtureBranchMerged(status *gitbutler.WorkspaceStatus) {
	status.UpstreamState.Behind = 0
	status.UpstreamState.LatestCommit = status.MergeBase
	status.UpstreamState.UpstreamCommits = nil
	status.Stacks[0].AssignedChanges = nil
	state := "MERGED"
	status.Stacks[0].Branches[0].ReviewState = &state
	status.Stacks[0].Branches[0].BranchStatus = gitbutler.StatusText("nothingToPush")
	status.Stacks[0].Branches[0].MergeStatus = gitbutler.StatusText("clean")
}

func TestDangerousActionsRequireConfirmation(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1
	model.contentCursor = 0

	dangerous := map[actionID]bool{
		actionDelete:        true,
		actionDiscard:       true,
		actionForcePush:     true,
		actionUndo:          true,
		actionRestore:       true,
		actionClean:         true,
		actionResolveCancel: true,
	}

	for _, action := range model.availableActions() {
		if !dangerous[action.ID] {
			continue
		}
		if !action.Dangerous && action.ConfirmText == "" {
			t.Fatalf("%s does not require confirmation", action.ID)
		}
	}
}

func TestWithPreviewTracksTargetBeforeCommandCompletes(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1
	model.contentCursor = 0

	next, cmd := model.withPreview()
	if cmd == nil {
		t.Fatal("expected preview command")
	}
	if next.previewTarget != "ae:sv" {
		t.Fatalf("preview target = %q", next.previewTarget)
	}
}

func TestSelectionAndRangeSelection(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1
	model.contentCursor = 0

	nextModel, _ := model.toggleSelection()
	next := nextModel.(Model)
	if !next.selected["ae:sv"] {
		t.Fatalf("selected = %#v", next.selected)
	}

	next.contentCursor = 1
	rangeModel, _ := next.rangeSelection()
	ranged := rangeModel.(Model)
	if !ranged.selected["ae:sv"] || !ranged.selected["c1"] {
		t.Fatalf("range selected = %#v", ranged.selected)
	}
}

func TestLaneMoveClearsSelection(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1
	model.contentCursor = 0
	model.selected = map[string]bool{"ae:sv": true}

	nextModel, _ := model.move(1)
	next := nextModel.(Model)
	if len(next.selected) != 0 {
		t.Fatalf("selection should be cleared after changing lanes: %#v", next.selected)
	}
	if next.contentCursor != 0 {
		t.Fatalf("content cursor = %d, want 0", next.contentCursor)
	}
}

func TestSetupActionsAvailableWithoutStatus(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.err = gitbutler.CLIError{Code: "setup_required", Message: "setup required"}

	seen := map[actionID]bool{}
	for _, action := range model.availableActions() {
		seen[action.ID] = true
	}
	if !seen[actionSetup] || !seen[actionSetupInit] {
		t.Fatalf("setup actions missing: %#v", seen)
	}
}

func TestInstallActionAvailableWhenGitButlerCLIMissing(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.err = gitbutler.ErrCLINotFound

	seen := actionIDs(model.availableActions())
	if !seen[actionInstallGitButler] {
		t.Fatalf("install action missing: %#v", seen)
	}
	if seen[actionSetup] || seen[actionSetupInit] {
		t.Fatalf("setup actions should wait until but exists: %#v", seen)
	}
}

func TestBootstrapPromptForMissingGitButlerCLI(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))

	nextModel, _ := model.Update(loadedMsg{err: gitbutler.ErrCLINotFound})
	next := nextModel.(Model)
	if next.mode != modeConfirm || next.confirm.Action.ID != actionInstallGitButler {
		t.Fatalf("confirm = mode %d action %#v", next.mode, next.confirm.Action)
	}
}

func TestGenericStatusErrorOnlyOffersRefresh(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.err = errors.New("parse status failed")

	seen := actionIDs(model.availableActions())
	if !seen[actionRefresh] {
		t.Fatalf("refresh missing: %#v", seen)
	}
	if seen[actionSetup] || seen[actionSetupInit] || seen[actionInstallGitButler] {
		t.Fatalf("bootstrap actions should not show for generic errors: %#v", seen)
	}
}

func TestBootstrapPromptForGitButlerSetup(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.width = 120

	nextModel, _ := model.Update(loadedMsg{err: gitbutler.CLIError{
		Code:    "setup_required",
		Message: "No GitButler project found at .",
		Hint:    "run `but setup` to configure the project",
	}})
	next := nextModel.(Model)
	if next.mode != modeConfirm || next.confirm.Action.ID != actionSetup {
		t.Fatalf("confirm = mode %d action %#v", next.mode, next.confirm.Action)
	}
	if !strings.Contains(next.confirm.Action.ConfirmText, "No GitButler project found at .") ||
		!strings.Contains(next.confirm.Action.ConfirmText, "but setup") {
		t.Fatalf("confirm text = %q", next.confirm.Action.ConfirmText)
	}
	if next.toast != "" {
		t.Fatalf("bootstrap setup should not show error toast: %q", next.toast)
	}
	if top := next.renderTop(); strings.Contains(top, "setup_required") || strings.Contains(top, "No GitButler project") {
		t.Fatalf("bootstrap setup should not pollute header: %q", top)
	}
}

func TestBootstrapPromptIgnoresOutsideMouseClicks(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))

	nextModel, _ := model.Update(loadedMsg{err: gitbutler.CLIError{Code: "setup_required", Message: "run but setup"}})
	next := nextModel.(Model)

	clickedModel, _ := next.handleConfirmMouse(tea.MouseMsg{X: 1, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	clicked := clickedModel.(Model)
	if clicked.mode != modeConfirm || clicked.confirm.Action.ID != actionSetup {
		t.Fatalf("outside click dismissed bootstrap prompt: mode %d action %#v", clicked.mode, clicked.confirm.Action)
	}
}

func TestBootstrapPromptEscDismissesToGuidedState(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.width = 120

	nextModel, _ := model.Update(loadedMsg{err: gitbutler.CLIError{Code: "setup_required", Message: "run but setup"}})
	next := nextModel.(Model)
	dismissedModel, _ := next.handleConfirmKey(tea.KeyMsg{Type: tea.KeyEsc})
	dismissed := dismissedModel.(Model)

	if dismissed.mode != modeNormal || dismissed.confirm.Action.ID != "" {
		t.Fatalf("dismissed = mode %d confirm %#v", dismissed.mode, dismissed.confirm)
	}
	if top := dismissed.renderTop(); strings.Contains(top, "setup_required") || strings.Contains(top, "run but setup") {
		t.Fatalf("dismissed header should stay calm: %q", top)
	}
	if !actionIDs(dismissed.availableActions())[actionSetup] {
		t.Fatal("setup action should remain available after dismissing bootstrap prompt")
	}
	lines := strings.Join(dismissed.bootstrapLines(100, 12), "\n")
	if !strings.Contains(lines, "run `but setup`") {
		t.Fatalf("bootstrap guidance missing setup action:\n%s", lines)
	}
}

func TestGenericConfirmMouseFooter(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.width = 100
	model.height = 30
	model.mode = modeConfirm
	model.confirm = confirmState{Action: action{ID: actionRefresh, Label: "refresh", ConfirmText: "Refresh now?"}}

	x, y, w, h := overlayBounds(model.width, model.height, model.renderConfirm())
	nextModel, cmd := model.handleConfirmMouse(tea.MouseMsg{X: x + 4, Y: y + h - 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	next := nextModel.(Model)
	if !next.loading || cmd == nil {
		t.Fatalf("confirm click should execute action, loading=%v cmd nil=%v", next.loading, cmd == nil)
	}

	model.mode = modeConfirm
	model.loading = false
	model.confirm = confirmState{Action: action{ID: actionRefresh, Label: "refresh", ConfirmText: "Refresh now?"}}
	cancelledModel, cmd := model.handleConfirmMouse(tea.MouseMsg{X: x + w - 4, Y: y + h - 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	cancelled := cancelledModel.(Model)
	if cancelled.mode != modeNormal || cmd != nil {
		t.Fatalf("cancel click = mode %d cmd nil=%v", cancelled.mode, cmd == nil)
	}
}

func TestBranchActionsIncludeDryRunAndPRLifecycle(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1

	seen := map[actionID]bool{}
	for _, action := range model.availableActions() {
		seen[action.ID] = true
	}
	for _, want := range []actionID{actionAddBranch, actionPushDryRun, actionNewDraftPR, actionPRDraft, actionPRReady, actionLand} {
		if !seen[want] {
			t.Fatalf("missing %s in branch actions: %#v", want, seen)
		}
	}
}

func TestLazyGitStyleKeyAliases(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1
	model.contentCursor = 0

	seen := map[actionID]action{}
	for _, action := range model.availableActions() {
		seen[action.ID] = action
	}
	if !seen[actionAmend].matches("A") || !seen[actionAmend].matches("i") {
		t.Fatalf("amend aliases missing: %#v", seen[actionAmend])
	}
	if !seen[actionDiscard].matches("d") || !seen[actionDiscard].matches("X") {
		t.Fatalf("discard aliases missing: %#v", seen[actionDiscard])
	}
}

func TestCommitOnZZUsesSoleAppliedBranchAndSelectedIDs(t *testing.T) {
	status := loadFixtureStatus(t)
	wrapped := wrapStatusAfter(t, status)
	runner := &actionRunner{outputs: map[string][]byte{
		"commit -b feature/ui -m selected ur --json --status-after": wrapped,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))
	model.laneCursor = 0
	model.contentCursor = 0

	if !actionIDs(model.availableActions())[actionCommit] {
		t.Fatal("commit should be available for a zz change with one applied branch")
	}
	_, cmd := model.execute(action{ID: actionCommit}, "selected")
	if cmd == nil {
		t.Fatal("expected commit command")
	}
	if msg := cmd().(mutationMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	want := [][]string{{"commit", "-b", "feature/ui", "-m", "selected", "ur", "--json", "--status-after"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestCommitOnZZPromptsForBranchWhenAmbiguous(t *testing.T) {
	status := loadFixtureStatus(t)
	status.Stacks = append(status.Stacks, gitbutler.Stack{Branches: []gitbutler.Branch{{Name: "feature/other"}}})
	wrapped := wrapStatusAfter(t, status)
	runner := &actionRunner{outputs: map[string][]byte{
		"commit -b feature/other -m selected ur --json --status-after": wrapped,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))
	model.laneCursor = 0
	model.contentCursor = 0

	if !actionIDs(model.availableActions())[actionCommit] {
		t.Fatal("commit should be available when a target branch can be selected")
	}
	nextModel, cmd := model.startAction(model.actionByID(actionCommit))
	next := nextModel.(Model)
	if cmd != nil || next.mode != modeTargetPicker || len(next.targetPicker.Items) != 2 {
		t.Fatalf("commit target picker = mode %d, items %d, cmd nil=%v", next.mode, len(next.targetPicker.Items), cmd == nil)
	}
	next.targetPicker.Cursor = 1
	nextModel, cmd = next.handleTargetPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = nextModel.(Model)
	if cmd != nil || next.mode != modeInput || next.prompt.Action.Target != "feature/other" {
		t.Fatalf("commit prompt = mode %d, target %q, cmd nil=%v", next.mode, next.prompt.Action.Target, cmd == nil)
	}
	next.prompt.Value = "selected"
	_, cmd = next.handleInputKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected targeted commit command")
	}
	if msg := cmd().(mutationMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	want := [][]string{{"commit", "-b", "feature/other", "-m", "selected", "ur", "--json", "--status-after"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestSquashPickerUsesSelectedSourceAndStableTargetOrder(t *testing.T) {
	status := loadFixtureStatus(t)
	status.Stacks[0].Branches[0].Commits = []gitbutler.Commit{
		{CLIID: "c3", Message: "third"},
		{CLIID: "c2", Message: "second"},
		{CLIID: "c1", Message: "first"},
	}
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))
	model.laneCursor = 1
	model.contentCursor = 2 // c2; assigned change is row 0

	action := model.actionByID(actionSquash)
	picker, ok := model.pickerForAction(action)
	if !ok {
		t.Fatal("expected squash target picker")
	}
	got := []string{picker.Items[0].Value, picker.Items[1].Value}
	want := []string{"c3", "c1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want displayed history order %#v", got, want)
	}
}

func TestLazyGitStyleNavigationKeys(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))

	nextModel, _ := model.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	next := nextModel.(Model)
	if next.focus != panelContents {
		t.Fatalf("l focus = %d, want contents", next.focus)
	}

	nextModel, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	next = nextModel.(Model)
	if next.focus != panelLanes {
		t.Fatalf("h focus = %d, want lanes", next.focus)
	}

	nextModel, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = nextModel.(Model)
	if next.focus != panelContents {
		t.Fatalf("enter focus = %d, want contents", next.focus)
	}
}

func TestKanbanNavigationKeysMoveColumnsAndItems(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.width = kanbanMinWidth
	model.height = 32

	nextModel, _ := model.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	next := nextModel.(Model)
	if next.laneCursor != 1 || next.contentCursor != 0 {
		t.Fatalf("right lane/content = %d/%d, want 1/0", next.laneCursor, next.contentCursor)
	}

	nextModel, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	next = nextModel.(Model)
	if next.laneCursor != 1 || next.contentCursor != 1 {
		t.Fatalf("down lane/content = %d/%d, want 1/1", next.laneCursor, next.contentCursor)
	}

	nextModel, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	next = nextModel.(Model)
	if next.laneCursor != 0 || next.contentCursor != 0 {
		t.Fatalf("left lane/content = %d/%d, want 0/0", next.laneCursor, next.contentCursor)
	}
}

func TestMouseWheelAndClickNavigation(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.width = kanbanMinWidth
	model.height = 32

	nextModel, _ := model.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelRight})
	next := nextModel.(Model)
	if next.laneCursor != 0 {
		t.Fatalf("wheel right lane = %d, want 0", next.laneCursor)
	}

	nextModel, _ = next.handleMouse(tea.MouseMsg{X: 35, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	next = nextModel.(Model)
	if next.laneCursor != 1 || next.contentCursor != 0 {
		t.Fatalf("click lane/content = %d/%d, want 1/0", next.laneCursor, next.contentCursor)
	}

	nextModel, _ = next.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	next = nextModel.(Model)
	if next.laneCursor != 1 || next.contentCursor != 1 {
		t.Fatalf("wheel down lane/content = %d/%d, want 1/1", next.laneCursor, next.contentCursor)
	}
}

func TestKanbanHeaderCueDoesNotOffsetMouseHitRows(t *testing.T) {
	model := modelWithActiveBranches(t, 7, 70, 30)
	model.data.Status.UnassignedChanges = append(model.data.Status.UnassignedChanges, gitbutler.FileChange{
		CLIID:    "uv",
		FilePath: "internal/tui/second.go",
	})
	model.data.Lanes[0].ChangeCount++
	model.data.buildContents()
	model.laneCursor = 1
	model.contentCursor = 0
	model.focus = panelLanes
	for _, tc := range []struct {
		y    int
		want int
	}{
		{y: 4, want: 0},
		{y: 5, want: 1},
	} {
		clicked, _ := model.clickKanban(1, tc.y)
		got := clicked.(Model)
		if got.laneCursor != 0 || got.contentCursor != tc.want || got.focus != panelContents {
			t.Fatalf("row %d click = lane/content/focus %d/%d/%d, want 0/%d/%d", tc.y, got.laneCursor, got.contentCursor, got.focus, tc.want, panelContents)
		}
	}
}

func TestKanbanClicksRespectLeftAnchoredCappedBoard(t *testing.T) {
	for _, width := range []int{100, 200} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			model := newModel(gitbutler.NewClient(".", nil))
			model.loading = false
			model.width = width
			model.height = 32
			model.data.Lanes = []lane{
				{Key: "zz", ID: "zz", Name: "unassigned", Kind: laneUnassigned},
				{Key: "b1", ID: "b1", Name: "feature/ui", Kind: laneAppliedBranch},
			}
			count, columnWidth := model.kanbanGeometry(width)
			boardEnd := count * columnWidth

			model.laneCursor = 1
			model.focus = panelLanes
			clicked, _ := model.clickKanban(0, 5)
			insideFirst := clicked.(Model)
			if insideFirst.laneCursor != 0 || insideFirst.focus != panelContents {
				t.Fatalf("left edge click = lane %d focus %d, want zz contents", insideFirst.laneCursor, insideFirst.focus)
			}

			clicked, _ = model.clickKanban(boardEnd, 5)
			outsideRight := clicked.(Model)
			if outsideRight.laneCursor != 1 || outsideRight.focus != panelLanes {
				t.Fatalf("right gutter click changed selection/focus: lane=%d focus=%d", outsideRight.laneCursor, outsideRight.focus)
			}

			model.laneCursor = 0
			clicked, _ = model.clickKanban(columnWidth, 5)
			inside := clicked.(Model)
			if inside.laneCursor != 1 || inside.focus != panelContents {
				t.Fatalf("second lane click = lane %d focus %d, want lane 1 contents", inside.laneCursor, inside.focus)
			}
		})
	}
}

func TestAddBranchPickerAppliesInactiveBranch(t *testing.T) {
	statusRaw, err := os.ReadFile("../gitbutler/testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')

	runner := &actionRunner{outputs: map[string][]byte{
		"apply feature/unapplied --json --status-after": wrapped,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.mode = modeBranchPicker

	next, cmd := model.handleBranchPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected apply command, next=%#v", next)
	}
	_ = cmd()
	if !reflect.DeepEqual(runner.calls, [][]string{{"apply", "feature/unapplied", "--json", "--status-after"}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestAddBranchPickerLazyLoadsBranchList(t *testing.T) {
	branchesRaw, err := os.ReadFile("../gitbutler/testdata/branch_list.json")
	if err != nil {
		t.Fatal(err)
	}
	runner := &actionRunner{outputs: map[string][]byte{
		"branch list --json --all": branchesRaw,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(loadFixtureStatus(t), nil)

	nextModel, cmd := model.openBranchPicker()
	next := nextModel.(Model)
	if !next.loading || cmd == nil {
		t.Fatalf("expected lazy branch-list load, loading=%v cmd nil=%v", next.loading, cmd == nil)
	}
	msg, ok := cmd().(branchListMsg)
	if !ok {
		t.Fatalf("message = %T, want branchListMsg", msg)
	}
	openedModel, _ := next.Update(msg)
	opened := openedModel.(Model)
	if opened.mode != modeBranchPicker || len(opened.data.BranchOptions) == 0 {
		t.Fatalf("picker mode/options = %d/%d", opened.mode, len(opened.data.BranchOptions))
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"branch", "list", "--json", "--all"}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestBranchPickerMouseWheelMovesSelection(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.data.BranchOptions = append(model.data.BranchOptions, branchOption{Name: "feature/other"})
	model.mode = modeBranchPicker
	model.height = 24

	nextModel, _ := model.handleBranchPickerMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	next := nextModel.(Model)
	if next.branchCursor != 1 {
		t.Fatalf("branch cursor = %d, want 1", next.branchCursor)
	}
}

func TestActionDispatchRunsExpectedGitButlerCommands(t *testing.T) {
	statusRaw, err := os.ReadFile("../gitbutler/testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')

	tests := []struct {
		name          string
		id            actionID
		input         string
		focus         panel
		laneCursor    int
		contentCursor int
		outputs       map[string][]byte
		want          [][]string
	}{
		{
			name: "refresh",
			id:   actionRefresh,
			outputs: map[string][]byte{
				"status --json --upstream": statusRaw,
			},
			want: [][]string{{"status", "--json", "--upstream"}},
		},
		{
			name: "setup",
			id:   actionSetup,
			outputs: map[string][]byte{
				"setup --json --status-after": wrapped,
			},
			want: [][]string{{"setup", "--json", "--status-after"}},
		},
		{
			name:  "setup init",
			id:    actionSetupInit,
			input: "",
			outputs: map[string][]byte{
				"setup --init --json --status-after": wrapped,
			},
			want: [][]string{{"setup", "--init", "--json", "--status-after"}},
		},
		{
			name:  "new branch",
			id:    actionNewBranch,
			input: "feature/new",
			outputs: map[string][]byte{
				"branch new feature/new --json --status-after": wrapped,
			},
			want: [][]string{{"branch", "new", "feature/new", "--json", "--status-after"}},
		},
		{
			name:       "new stacked branch",
			id:         actionNewStacked,
			input:      "feature/child",
			laneCursor: 1,
			outputs: map[string][]byte{
				"branch new --anchor feature/ui feature/child --json --status-after": wrapped,
			},
			want: [][]string{{"branch", "new", "--anchor", "feature/ui", "feature/child", "--json", "--status-after"}},
		},
		{
			name:       "unapply applied branch",
			id:         actionApplyToggle,
			laneCursor: 1,
			outputs: map[string][]byte{
				"unapply feature/ui --json --status-after": wrapped,
			},
			want: [][]string{{"unapply", "feature/ui", "--json", "--status-after"}},
		},
		{
			name:          "commit current change",
			id:            actionCommit,
			input:         "commit msg",
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"commit -b feature/ui -m commit msg ae:sv --json --status-after": wrapped,
			},
			want: [][]string{{"commit", "-b", "feature/ui", "-m", "commit msg", "ae:sv", "--json", "--status-after"}},
		},
		{
			name:       "rename branch",
			id:         actionRename,
			input:      "feature/renamed",
			laneCursor: 1,
			outputs: map[string][]byte{
				"reword ma -m feature/renamed --json --status-after": wrapped,
			},
			want: [][]string{{"reword", "ma", "-m", "feature/renamed", "--json", "--status-after"}},
		},
		{
			name:       "delete branch",
			id:         actionDelete,
			laneCursor: 1,
			outputs: map[string][]byte{
				"discard feature/ui --json --status-after": wrapped,
			},
			want: [][]string{{"discard", "feature/ui", "--json", "--status-after"}},
		},
		{
			name:          "discard change",
			id:            actionDiscard,
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"discard ae:sv --json --status-after": wrapped,
			},
			want: [][]string{{"discard", "ae:sv", "--json", "--status-after"}},
		},
		{
			name:          "amend change",
			id:            actionAmend,
			input:         "c1",
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"amend -t c1 ae:sv --json --status-after": wrapped,
			},
			want: [][]string{{"amend", "-t", "c1", "ae:sv", "--json", "--status-after"}},
		},
		{
			name:       "absorb",
			id:         actionAbsorb,
			laneCursor: 1,
			outputs: map[string][]byte{
				"absorb --json --status-after": wrapped,
			},
			want: [][]string{{"absorb", "--json", "--status-after"}},
		},
		{
			name:          "squash current commit",
			id:            actionSquash,
			input:         "c2",
			laneCursor:    1,
			contentCursor: 1,
			outputs: map[string][]byte{
				"squash c1 -t c2 --use-target-message --json --status-after": wrapped,
			},
			want: [][]string{{"squash", "c1", "-t", "c2", "--use-target-message", "--json", "--status-after"}},
		},
		{
			name:          "uncommit",
			id:            actionUncommit,
			laneCursor:    1,
			contentCursor: 1,
			outputs: map[string][]byte{
				"uncommit c1 --json --status-after": wrapped,
			},
			want: [][]string{{"uncommit", "c1", "--json", "--status-after"}},
		},
		{
			name:          "move commit",
			id:            actionMove,
			input:         "feature/target",
			focus:         panelContents,
			laneCursor:    1,
			contentCursor: 1,
			outputs: map[string][]byte{
				"move c1 -b feature/target --json --status-after": wrapped,
			},
			want: [][]string{{"move", "c1", "-b", "feature/target", "--json", "--status-after"}},
		},
		{
			name:          "unstack branch",
			id:            actionMove,
			input:         "zz",
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"move ma --unstack --json --status-after": wrapped,
			},
			want: [][]string{{"move", "ma", "--unstack", "--json", "--status-after"}},
		},
		{
			name:       "land",
			id:         actionLand,
			laneCursor: 1,
			outputs: map[string][]byte{
				"land feature/ui --yes --json --status-after": wrapped,
			},
			want: [][]string{{"land", "feature/ui", "--yes", "--json", "--status-after"}},
		},
		{
			name: "pull check",
			id:   actionPullCheck,
			outputs: map[string][]byte{
				"pull --check": []byte("ok"),
			},
			want: [][]string{{"pull", "--check"}},
		},
		{
			name: "pull",
			id:   actionPull,
			outputs: map[string][]byte{
				"pull --json --status-after": wrapped,
			},
			want: [][]string{{"pull", "--json", "--status-after"}},
		},
		{
			name:       "push",
			id:         actionPush,
			laneCursor: 1,
			outputs: map[string][]byte{
				"push feature/ui":          []byte("pushed"),
				"status --json --upstream": statusRaw,
			},
			want: [][]string{{"push", "feature/ui"}, {"status", "--json", "--upstream"}},
		},
		{
			name:       "push dry-run",
			id:         actionPushDryRun,
			laneCursor: 1,
			outputs: map[string][]byte{
				"push feature/ui --dry-run": []byte("ok"),
			},
			want: [][]string{{"push", "feature/ui", "--dry-run"}},
		},
		{
			name:       "force push",
			id:         actionForcePush,
			laneCursor: 1,
			outputs: map[string][]byte{
				"push feature/ui --skip-force-push-protection": []byte("pushed"),
				"status --json --upstream":                     statusRaw,
			},
			want: [][]string{{"push", "feature/ui", "--skip-force-push-protection"}, {"status", "--json", "--upstream"}},
		},
		{
			name:       "new pr",
			id:         actionNewPR,
			laneCursor: 1,
			outputs: map[string][]byte{
				"pr new feature/ui --default": []byte("ok"),
			},
			want: [][]string{{"pr", "new", "feature/ui", "--default"}},
		},
		{
			name:       "new draft pr",
			id:         actionNewDraftPR,
			laneCursor: 1,
			outputs: map[string][]byte{
				"pr new feature/ui --default --draft": []byte("ok"),
			},
			want: [][]string{{"pr", "new", "feature/ui", "--default", "--draft"}},
		},
		{
			name:       "set pr draft",
			id:         actionPRDraft,
			laneCursor: 1,
			outputs: map[string][]byte{
				"pr set-draft feature/ui --json --status-after": wrapped,
			},
			want: [][]string{{"pr", "set-draft", "feature/ui", "--json", "--status-after"}},
		},
		{
			name:       "set pr ready",
			id:         actionPRReady,
			laneCursor: 1,
			outputs: map[string][]byte{
				"pr set-ready feature/ui --json --status-after": wrapped,
			},
			want: [][]string{{"pr", "set-ready", "feature/ui", "--json", "--status-after"}},
		},
		{
			name: "resolve status",
			id:   actionResolveStatus,
			outputs: map[string][]byte{
				"resolve status": []byte("ok"),
			},
			want: [][]string{{"resolve", "status"}},
		},
		{
			name:       "resolve finish",
			id:         actionResolveFinish,
			laneCursor: 1,
			outputs: map[string][]byte{
				"resolve finish --json --status-after": wrapped,
			},
			want: [][]string{{"resolve", "finish", "--json", "--status-after"}},
		},
		{
			name:       "resolve cancel",
			id:         actionResolveCancel,
			laneCursor: 1,
			outputs: map[string][]byte{
				"resolve cancel --json --status-after": wrapped,
			},
			want: [][]string{{"resolve", "cancel", "--json", "--status-after"}},
		},
		{
			name: "undo",
			id:   actionUndo,
			outputs: map[string][]byte{
				"undo --json --status-after": wrapped,
			},
			want: [][]string{{"undo", "--json", "--status-after"}},
		},
		{
			name:       "snapshot",
			id:         actionSnapshot,
			input:      "checkpoint",
			laneCursor: 1,
			outputs: map[string][]byte{
				"oplog snapshot -m checkpoint": []byte("ok"),
			},
			want: [][]string{{"oplog", "snapshot", "-m", "checkpoint"}},
		},
		{
			name:  "restore",
			id:    actionRestore,
			input: "snap",
			outputs: map[string][]byte{
				"oplog restore snap --json --status-after": wrapped,
			},
			want: [][]string{{"oplog", "restore", "snap", "--json", "--status-after"}},
		},
		{
			name: "clean dry-run",
			id:   actionCleanDryRun,
			outputs: map[string][]byte{
				"clean --dry-run": []byte("ok"),
			},
			want: [][]string{{"clean", "--dry-run"}},
		},
		{
			name: "clean",
			id:   actionClean,
			outputs: map[string][]byte{
				"clean --json --status-after": wrapped,
			},
			want: [][]string{{"clean", "--json", "--status-after"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &actionRunner{outputs: tc.outputs}
			model := newModel(gitbutler.NewClient(".", runner))
			model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
			model.focus = tc.focus
			model.laneCursor = tc.laneCursor
			model.contentCursor = tc.contentCursor

			next, cmd := model.execute(action{ID: tc.id}, tc.input)
			if cmd == nil {
				t.Fatalf("expected command for %s, next=%#v", tc.id, next)
			}
			msg := cmd()
			if batch, ok := msg.(tea.BatchMsg); ok && tc.id == actionRefresh {
				if len(batch) != 2 {
					t.Fatalf("refresh batch length = %d, want 2", len(batch))
				}
				msg = batch[1]()
			}
			switch msg.(type) {
			case loadedMsg, mutationMsg, textMsg:
			default:
				t.Fatalf("unexpected message %T", msg)
			}
			if !reflect.DeepEqual(runner.calls, tc.want) {
				t.Fatalf("calls = %#v, want %#v", runner.calls, tc.want)
			}
		})
	}
}

func TestPushSetsContextualLoadingState(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1

	nextModel, cmd := model.execute(action{ID: actionPush}, "")
	if cmd == nil {
		t.Fatal("expected push command")
	}
	next := nextModel.(Model)
	if !next.loading || next.loadingAction != actionPush || next.loadingBranch != "feature/ui" {
		t.Fatalf("loading context = loading:%v action:%q branch:%q", next.loading, next.loadingAction, next.loadingBranch)
	}
}

func TestPullSetsContextualLoadingState(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))

	nextModel, cmd := model.execute(action{ID: actionPull}, "")
	if cmd == nil {
		t.Fatal("expected pull command")
	}
	next := nextModel.(Model)
	if !next.loading || next.loadingAction != actionPull || next.loadingBranch != "" {
		t.Fatalf("loading context = loading:%v action:%q branch:%q", next.loading, next.loadingAction, next.loadingBranch)
	}
}

func TestUpstreamUpdateSummaryAndConflictToast(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	status := loadFixtureStatus(t)
	status.UpstreamState.Behind = 12
	status.UpstreamState.UpstreamCommits = []gitbutler.Commit{
		{CommitID: "one", Message: "upstream one"},
		{CommitID: "two", Message: "upstream two"},
	}
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))

	summary := model.upstreamUpdateSummary()
	for _, want := range []string{
		"Incoming target commits: 2",
		"Applied branches to update: feature/ui",
		"Known conflicts: feature/ui",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}

	text, kind := model.mutationToast("updated from upstream", model.data.Status)
	if kind != toastError || !strings.Contains(text, "conflicts detected") {
		t.Fatalf("toast = %q/%d", text, kind)
	}
}

// The footer must acknowledge key commands: a running action shows its label
// with a spinner, and a fresh toast shows there too — so a keypress is never
// silent.
func TestFooterShowsActionStatus(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.width = 120
	model.height = 30

	loading := model.startLoadingFor("creating PR", actionNewPR, "feature/ui")
	if bar := loading.renderHotbar(); !strings.Contains(bar, "creating PR") {
		t.Fatalf("footer missing loading label:\n%s", bar)
	}

	// Once the action finishes (loading cleared) its toast owns the footer.
	done := model.stopLoading()
	done.setToast("PR created", toastSuccess)
	if bar := done.renderHotbar(); !strings.Contains(bar, "PR created") {
		t.Fatalf("footer missing toast:\n%s", bar)
	}

	// Idle footer is just key hints — no stale status.
	idle := model.stopLoading()
	if bar := idle.renderHotbar(); strings.Contains(bar, "creating PR") || strings.Contains(bar, "working") {
		t.Fatalf("idle footer should carry no status:\n%s", bar)
	}
}

// Pressing an action key that runs async work must flip on a loading state
// immediately, so the footer reacts on the same frame as the keypress.
func TestAsyncActionStartsLoadingImmediately(t *testing.T) {
	runner := &actionRunner{outputs: map[string][]byte{}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1 // an applied branch, so PR actions are available

	next, cmd := model.execute(action{ID: actionNewDraftPR}, "")
	m := next.(Model)
	if !m.loading || m.loadingLabel == "" {
		t.Fatalf("create-draft-PR should start loading immediately: loading=%v label=%q", m.loading, m.loadingLabel)
	}
	if cmd == nil {
		t.Fatal("expected a command to run the PR creation")
	}
}

func TestHumanizeCLIError(t *testing.T) {
	forge := errors.New("Failed to create forge review for branch.\n\nCaused by:\n    No authenticated forge users found.\n    Run 'but config forge auth' to authenticate with GitHub.")
	if got := humanizeCLIError(forge); !strings.Contains(got, "ctrl+g") || strings.Contains(got, "\n") {
		t.Fatalf("forge-auth error not humanized: %q", got)
	}
	generic := errors.New("Error: something broke\nCaused by:\n    deeper detail")
	if got := humanizeCLIError(generic); got != "something broke" {
		t.Fatalf("generic error = %q, want %q", got, "something broke")
	}
	if got := humanizeCLIError(nil); got != "" {
		t.Fatalf("nil error = %q, want empty", got)
	}
}

// A generic PR/action error goes to a toast, never as a raw multi-line blob in
// the preview zone.
func TestGenericActionErrorGoesToToastNotPreview(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.mode = modeNormal
	model.preview = "existing diff"

	next, _ := model.Update(textMsg{target: "message", err: errors.New("Error: something broke\nCaused by:\n    detail")})
	m := next.(Model)
	if m.previewErr != nil || m.preview != "existing diff" {
		t.Fatalf("preview clobbered: err=%v body=%q", m.previewErr, m.preview)
	}
	if m.mode != modeNormal {
		t.Fatalf("generic error should not open a modal, mode=%d", m.mode)
	}
	if m.toastKind != toastError || !strings.Contains(m.toast, "something broke") {
		t.Fatalf("toast = %q/%d", m.toast, m.toastKind)
	}
}

func TestMutationErrorGoesToToastNotFlash(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	err := errors.New("exit status 1: Error: GitForcePushProtection\n\nCaused by:\n    remote commits would be overwritten")

	next, _ := model.Update(mutationMsg{err: err})
	m := next.(Model)
	if m.err != nil {
		t.Fatalf("mutation error leaked into flash: %v", m.err)
	}
	if m.toast != "remote commits would be overwritten — use force push only if intentional" || m.toastKind != toastError {
		t.Fatalf("toast = %q/%d", m.toast, m.toastKind)
	}
}

// A forge-auth failure opens the guided confirm that can launch the in-app
// login flow — not a fading toast the user might miss.
func TestForgeAuthErrorOpensGuidedConfirm(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.mode = modeNormal

	authErr := errors.New("Failed to create forge review.\n\nCaused by:\n    No authenticated forge users found.")
	next, _ := model.Update(textMsg{target: "message", err: authErr})
	m := next.(Model)
	if m.mode != modeConfirm || m.confirm.Action.ID != actionForgeAuth {
		t.Fatalf("forge-auth error should open the auth confirm, got mode=%d action=%q", m.mode, m.confirm.Action.ID)
	}

	// Accepting it must launch a command (the interactive login), not no-op.
	accepted, cmd := m.acceptConfirm()
	if cmd == nil {
		t.Fatal("accepting forge-auth confirm should return a command")
	}
	if accepted.(Model).mode != modeNormal {
		t.Fatalf("confirm should close on accept, mode=%d", accepted.(Model).mode)
	}
}

func TestIncomingCountIgnoresBehindWithoutCommitList(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	status := loadFixtureStatus(t)
	status.UpstreamState.Behind = 12
	status.UpstreamState.LatestCommit = status.MergeBase
	status.UpstreamState.UpstreamCommits = nil
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))

	if got := model.incomingChangeCount(); got != 0 {
		t.Fatalf("incoming changes = %d, want 0", got)
	}
	if top := model.renderTop(); strings.Contains(top, glyphBehind+" 12") {
		t.Fatalf("top bar should not show stale behind count: %q", top)
	}
}

// Older `but` CLIs never emit upstreamCommits: behind + an unmerged upstream
// tip is the only signal that remote changes exist, and it must count.
func TestIncomingCountFallsBackToBehindWhenTipUnmerged(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	status := loadFixtureStatus(t)
	status.UpstreamState.Behind = 1
	status.UpstreamState.LatestCommit = gitbutler.Commit{CommitID: "remote-tip", Message: "Merge pull request #808"}
	status.UpstreamState.UpstreamCommits = nil
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))

	if got := model.incomingChangeCount(); got != 1 {
		t.Fatalf("incoming changes = %d, want 1", got)
	}
}

func TestUpdateFromUpstreamRefreshesBeforeSayingNoUpdate(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	status := loadFixtureStatus(t)
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))
	if !actionIDs(model.availableActions())[actionPull] {
		t.Fatal("update from upstream should be available when target has incoming commits")
	}

	status = loadFixtureStatus(t)
	status.UpstreamState.Behind = 0
	status.UpstreamState.UpstreamCommits = nil
	statusRaw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	runner := &actionRunner{outputs: map[string][]byte{
		"status --json --upstream": statusRaw,
	}}
	model = newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))
	if !actionIDs(model.availableActions())[actionPull] {
		t.Fatal("update from upstream should remain available so it can refresh")
	}
	nextModel, cmd := model.startAction(action{ID: actionPull})
	next := nextModel.(Model)
	if !next.loading || cmd == nil {
		t.Fatalf("expected animated refresh, loading=%v cmd nil=%v", next.loading, cmd == nil)
	}
	msg, ok := cmd().(upstreamRefreshMsg)
	if !ok {
		t.Fatalf("message = %T, want upstreamRefreshMsg", msg)
	}
	nextModel, _ = next.Update(msg)
	next = nextModel.(Model)
	if next.toast != "no upstream update" {
		t.Fatalf("toast = %q", next.toast)
	}
	if next.mode != modeNormal {
		t.Fatalf("mode = %d, want normal", next.mode)
	}
}

func TestUpdateFromUpstreamOpensConfirmForMergedBranchCleanup(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	status := loadFixtureStatus(t)
	markFixtureBranchMerged(status)

	model.data = buildWorkspaceData(status, loadFixtureBranches(t))
	nextModel, cmd := model.startAction(action{ID: actionPull, ConfirmText: model.upstreamUpdateConfirmText()})
	next := nextModel.(Model)
	if cmd != nil {
		t.Fatal("merged branch cleanup should not refresh before opening the pull confirm")
	}
	if next.mode != modeConfirm || next.confirm.Action.ID != actionPull {
		t.Fatalf("mode/action = %d/%q, want pull confirm", next.mode, next.confirm.Action.ID)
	}
}

func TestPullCleansMergedBranchWithoutIncomingTargetCommits(t *testing.T) {
	status := loadFixtureStatus(t)
	markFixtureBranchMerged(status)
	wrapped := wrapStatusAfter(t, status)
	runner := &actionRunner{outputs: map[string][]byte{
		"pull --json --status-after": wrapped,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))

	_, cmd := model.execute(action{ID: actionPull}, "")
	if cmd == nil {
		t.Fatal("expected cleanup command")
	}
	msg, ok := cmd().(mutationMsg)
	if !ok {
		t.Fatalf("message = %T, want mutationMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("cleanup failed: %v", msg.err)
	}
	want := [][]string{{"pull", "--json", "--status-after"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestPullUpdatesThenCleansMergedBranchWithIncomingTargetCommits(t *testing.T) {
	status := loadFixtureStatus(t)
	markFixtureBranchMerged(status)
	status.UpstreamState.Behind = 1
	status.UpstreamState.LatestCommit = gitbutler.Commit{CommitID: "remote-tip", Message: "Merge pull request #825"}
	status.UpstreamState.UpstreamCommits = []gitbutler.Commit{status.UpstreamState.LatestCommit}
	wrapped := wrapStatusAfter(t, status)
	runner := &actionRunner{outputs: map[string][]byte{
		"pull --json --status-after": wrapped,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))

	_, cmd := model.execute(action{ID: actionPull}, "")
	if cmd == nil {
		t.Fatal("expected update command")
	}
	msg, ok := cmd().(mutationMsg)
	if !ok {
		t.Fatalf("message = %T, want mutationMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("update failed: %v", msg.err)
	}
	want := [][]string{
		{"pull", "--json", "--status-after"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestStartupRefreshMsgStartsInitialStatusLoad(t *testing.T) {
	statusRaw, err := json.Marshal(loadFixtureStatus(t))
	if err != nil {
		t.Fatal(err)
	}
	runner := &actionRunner{outputs: map[string][]byte{
		"status --json --upstream": statusRaw,
	}}
	model := newModel(gitbutler.NewClient(".", runner))

	nextModel, cmd := model.Update(startupRefreshMsg{})
	next := nextModel.(Model)
	if !next.loading || cmd == nil {
		t.Fatalf("startup refresh should start loading, loading=%v cmd nil=%v", next.loading, cmd == nil)
	}
	msg, ok := cmd().(tea.BatchMsg)
	if !ok || len(msg) != 2 {
		t.Fatalf("message = %T/%d, want two startup commands", msg, len(msg))
	}
	full, ok := msg[1]().(loadedMsg)
	if !ok {
		t.Fatalf("second command message = %T, want loadedMsg", full)
	}
	if full.err != nil || full.status == nil {
		t.Fatalf("startup refresh failed: status nil=%v err=%v", full.status == nil, full.err)
	}
	if got := strings.Join(runner.calls[0], " "); got != "status --json --upstream" {
		t.Fatalf("command = %q, want status --json --upstream", got)
	}
}

func TestFastStatusMsgDisplaysGitChangesWithoutSetupActions(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))

	nextModel, _ := model.Update(fastStatusMsg{changes: []gitbutler.FileChange{{
		CLIID:      "git:main.go",
		FilePath:   "main.go",
		ChangeType: "modified",
	}}})
	next := nextModel.(Model)
	if next.data.Status != nil || !next.data.Fast {
		t.Fatalf("fast status should stay preliminary: %#v", next.data)
	}
	if items := next.contents(); len(items) != 1 || items[0].Label != "main.go" {
		t.Fatalf("fast changes not visible: %#v", items)
	}
	actions := actionIDs(next.availableActions())
	if actions[actionSetup] || actions[actionCommit] {
		t.Fatalf("fast status should not expose GitButler mutations: %#v", actions)
	}
}

func actionIDs(actions []action) map[actionID]bool {
	out := map[actionID]bool{}
	for _, action := range actions {
		out[action.ID] = true
	}
	return out
}

func TestUpstreamConfirmNavigationAndDryCheck(t *testing.T) {
	runner := &actionRunner{outputs: map[string][]byte{
		"pull --check": []byte("clean"),
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.data.Lanes = append(model.data.Lanes, lane{Kind: laneAppliedBranch, Name: "feature/second"})
	model.mode = modeConfirm
	model.confirm = confirmState{Action: action{ID: actionPull}}

	nextModel, _ := model.handleUpstreamConfirmKey(tea.KeyMsg{Type: tea.KeyDown})
	next := nextModel.(Model)
	if next.confirm.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", next.confirm.Cursor)
	}

	nextModel, _ = next.handleConfirmMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	next = nextModel.(Model)
	if next.confirm.Cursor != 0 {
		t.Fatalf("cursor after wheel = %d, want 0", next.confirm.Cursor)
	}

	afterDryCheck, cmd := next.handleUpstreamConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if cmd == nil {
		t.Fatal("expected dry-check command")
	}
	_ = cmd()
	if afterDryCheck.(Model).mode != modeNormal {
		t.Fatalf("mode after dry-check = %d, want normal", afterDryCheck.(Model).mode)
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"pull", "--check"}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestAutoRefreshStatusOnly(t *testing.T) {
	statusRaw, err := os.ReadFile("../gitbutler/testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}

	runner := &actionRunner{outputs: map[string][]byte{
		"status --json --upstream": statusRaw,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.loading = false
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	next, cmd := model.requestAutoRefresh(false)
	if cmd == nil {
		t.Fatal("expected status auto-refresh command")
	}
	if !next.autoRefreshInFlight {
		t.Fatal("auto refresh should be marked in flight")
	}
	if _, ok := cmd().(autoRefreshMsg); !ok {
		t.Fatalf("unexpected message from auto refresh")
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"status", "--json", "--upstream"}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}

	// includeBranches=true must also reload the branch list — it carries PR
	// reviews and ahead/behind state that otherwise go permanently stale.
	branchesRaw, err := os.ReadFile("../gitbutler/testdata/branch_list.json")
	if err != nil {
		t.Fatal(err)
	}
	runner = &actionRunner{outputs: map[string][]byte{
		"status --json --upstream": statusRaw,
		"branch list --json --all": branchesRaw,
	}}
	model = newModel(gitbutler.NewClient(".", runner))
	model.loading = false
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	_, cmd = model.requestAutoRefresh(true)
	if cmd == nil {
		t.Fatal("expected coalesced auto-refresh command")
	}
	msg, ok := cmd().(autoRefreshMsg)
	if !ok {
		t.Fatalf("unexpected message from auto refresh")
	}
	if msg.branches == nil {
		t.Fatal("branch-including refresh should return a branch list")
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"status", "--json", "--upstream"}, {"branch", "list", "--json", "--all"}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestAutoRefreshCoalescesAndPauses(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.loading = false
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.autoRefreshInFlight = true

	next, cmd := model.requestAutoRefresh(true)
	if cmd != nil {
		t.Fatal("refresh should not overlap an in-flight refresh")
	}
	if !next.autoRefreshPending || !next.autoRefreshPendingBranches {
		t.Fatalf("pending flags = %v/%v", next.autoRefreshPending, next.autoRefreshPendingBranches)
	}

	next.loading = true
	next, cmd = next.requestAutoRefresh(false)
	if cmd != nil || !next.autoRefreshPending {
		t.Fatalf("loading refresh should stay paused without changing pending state")
	}
}

func TestAutoRefreshPreservesDataOnError(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.loading = false
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.autoRefreshInFlight = true
	oldStatus := model.data.Status

	nextModel, _ := model.Update(autoRefreshMsg{err: errors.New("boom")})
	next := nextModel.(Model)
	if next.data.Status != oldStatus {
		t.Fatal("background refresh error should preserve stale status")
	}
	if next.err != nil {
		t.Fatalf("background refresh should not replace foreground error: %v", next.err)
	}
	if next.toast == "" {
		t.Fatal("background refresh error should surface as a toast")
	}
}

func TestAutoRefreshPreservesSelectionByStableIDs(t *testing.T) {
	status := loadFixtureStatus(t)
	updated := *status
	updated.Stacks = append([]gitbutler.Stack(nil), status.Stacks...)
	updated.Stacks[0].AssignedChanges = append([]gitbutler.FileChange{
		{CLIID: "new", FilePath: "new.go"},
	}, status.Stacks[0].AssignedChanges...)

	model := newModel(gitbutler.NewClient(".", nil))
	model.loading = false
	model.data = buildWorkspaceData(status, loadFixtureBranches(t))
	model.laneCursor = 1
	model.contentCursor = 1

	nextModel, _ := model.Update(autoRefreshMsg{status: &updated, branches: loadFixtureBranches(t)})
	next := nextModel.(Model)
	item, ok := next.selectedContent()
	if !ok || item.ID != "c1" {
		t.Fatalf("selected item = %#v, ok=%v", item, ok)
	}
}

func TestLandShortcutDoesNotCollideWithEnter(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), nil)
	model.laneCursor = 1
	next, _ := model.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m"), Alt: true})
	if got := next.(Model); got.mode != modeConfirm || got.confirm.Action.ID != actionLand {
		t.Fatal("Alt+M should confirm landing")
	}
	next, _ = model.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(Model); got.mode == modeConfirm && got.confirm.Action.ID == actionLand {
		t.Fatal("Enter must not land a branch")
	}
}
