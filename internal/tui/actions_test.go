package tui

import (
	"context"
	"encoding/json"
	"errors"
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

func TestBootstrapPromptForGitButlerSetup(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))

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
}

func TestBranchActionsIncludeDryRunAndPRLifecycle(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.laneCursor = 1

	seen := map[actionID]bool{}
	for _, action := range model.availableActions() {
		seen[action.ID] = true
	}
	for _, want := range []actionID{actionAddBranch, actionPushDryRun, actionNewDraftPR, actionPRDraft, actionPRReady, actionMerge} {
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
	if !seen[actionStage].matches("a") {
		t.Fatalf("stage should accept lazygit-style a alias")
	}
	if !seen[actionAmend].matches("A") || !seen[actionAmend].matches("i") {
		t.Fatalf("amend aliases missing: %#v", seen[actionAmend])
	}
	if !seen[actionDiscard].matches("d") || !seen[actionDiscard].matches("X") {
		t.Fatalf("discard aliases missing: %#v", seen[actionDiscard])
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
	if next.laneCursor != 1 {
		t.Fatalf("wheel right lane = %d, want 1", next.laneCursor)
	}

	nextModel, _ = next.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	next = nextModel.(Model)
	if next.contentCursor != 1 {
		t.Fatalf("wheel down content = %d, want 1", next.contentCursor)
	}

	nextModel, _ = next.handleMouse(tea.MouseMsg{X: 0, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	next = nextModel.(Model)
	if next.laneCursor != 0 {
		t.Fatalf("click lane = %d, want 0", next.laneCursor)
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
		"apply feature/unapplied -j --status-after": wrapped,
	}}
	model := newModel(gitbutler.NewClient(".", runner))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.mode = modeBranchPicker

	next, cmd := model.handleBranchPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected apply command, next=%#v", next)
	}
	_ = cmd()
	if !reflect.DeepEqual(runner.calls, [][]string{{"apply", "feature/unapplied", "-j", "--status-after"}}) {
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
	branchesRaw, err := os.ReadFile("../gitbutler/testdata/branch_list.json")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := append([]byte(`{"result":{},"status":`), statusRaw...)
	wrapped = append(wrapped, '}')

	tests := []struct {
		name          string
		id            actionID
		input         string
		laneCursor    int
		contentCursor int
		outputs       map[string][]byte
		want          [][]string
	}{
		{
			name: "refresh",
			id:   actionRefresh,
			outputs: map[string][]byte{
				"status -j":            statusRaw,
				"branch list -j --all": branchesRaw,
			},
			want: [][]string{{"status", "-j"}, {"branch", "list", "-j", "--all"}},
		},
		{
			name: "setup",
			id:   actionSetup,
			outputs: map[string][]byte{
				"setup -j --status-after": wrapped,
			},
			want: [][]string{{"setup", "-j", "--status-after"}},
		},
		{
			name:  "setup init",
			id:    actionSetupInit,
			input: "",
			outputs: map[string][]byte{
				"setup --init -j --status-after": wrapped,
			},
			want: [][]string{{"setup", "--init", "-j", "--status-after"}},
		},
		{
			name:  "new branch",
			id:    actionNewBranch,
			input: "feature/new",
			outputs: map[string][]byte{
				"branch new feature/new -j --status-after": wrapped,
			},
			want: [][]string{{"branch", "new", "feature/new", "-j", "--status-after"}},
		},
		{
			name:       "new stacked branch",
			id:         actionNewStacked,
			input:      "feature/child",
			laneCursor: 1,
			outputs: map[string][]byte{
				"branch new --anchor feature/ui feature/child -j --status-after": wrapped,
			},
			want: [][]string{{"branch", "new", "--anchor", "feature/ui", "feature/child", "-j", "--status-after"}},
		},
		{
			name:          "stage current change",
			id:            actionStage,
			input:         "feature/ui",
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"stage ae:sv feature/ui -j --status-after": wrapped,
			},
			want: [][]string{{"stage", "ae:sv", "feature/ui", "-j", "--status-after"}},
		},
		{
			name:       "unapply applied branch",
			id:         actionApplyToggle,
			laneCursor: 1,
			outputs: map[string][]byte{
				"unapply feature/ui -j --status-after": wrapped,
			},
			want: [][]string{{"unapply", "feature/ui", "-j", "--status-after"}},
		},
		{
			name:          "commit current change",
			id:            actionCommit,
			input:         "commit msg",
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"commit feature/ui -m commit msg --changes ae:sv -j --status-after": wrapped,
			},
			want: [][]string{{"commit", "feature/ui", "-m", "commit msg", "--changes", "ae:sv", "-j", "--status-after"}},
		},
		{
			name:       "rename branch",
			id:         actionRename,
			input:      "feature/renamed",
			laneCursor: 1,
			outputs: map[string][]byte{
				"reword ma -m feature/renamed -j --status-after": wrapped,
			},
			want: [][]string{{"reword", "ma", "-m", "feature/renamed", "-j", "--status-after"}},
		},
		{
			name:       "delete branch",
			id:         actionDelete,
			laneCursor: 1,
			outputs: map[string][]byte{
				"branch delete feature/ui -j --status-after": wrapped,
			},
			want: [][]string{{"branch", "delete", "feature/ui", "-j", "--status-after"}},
		},
		{
			name:          "discard change",
			id:            actionDiscard,
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"discard ae:sv -j --status-after": wrapped,
			},
			want: [][]string{{"discard", "ae:sv", "-j", "--status-after"}},
		},
		{
			name:          "amend change",
			id:            actionAmend,
			input:         "c1",
			laneCursor:    1,
			contentCursor: 0,
			outputs: map[string][]byte{
				"amend ae:sv c1 -j --status-after": wrapped,
			},
			want: [][]string{{"amend", "ae:sv", "c1", "-j", "--status-after"}},
		},
		{
			name:       "absorb",
			id:         actionAbsorb,
			laneCursor: 1,
			outputs: map[string][]byte{
				"absorb -j --status-after": wrapped,
			},
			want: [][]string{{"absorb", "-j", "--status-after"}},
		},
		{
			name:          "squash current commit",
			id:            actionSquash,
			laneCursor:    1,
			contentCursor: 1,
			outputs: map[string][]byte{
				"squash c1 -j --status-after": wrapped,
			},
			want: [][]string{{"squash", "c1", "-j", "--status-after"}},
		},
		{
			name:          "uncommit",
			id:            actionUncommit,
			laneCursor:    1,
			contentCursor: 1,
			outputs: map[string][]byte{
				"uncommit c1 -j --status-after": wrapped,
			},
			want: [][]string{{"uncommit", "c1", "-j", "--status-after"}},
		},
		{
			name:          "move commit",
			id:            actionMove,
			input:         "feature/target",
			laneCursor:    1,
			contentCursor: 1,
			outputs: map[string][]byte{
				"move c1 feature/target -j --status-after": wrapped,
			},
			want: [][]string{{"move", "c1", "feature/target", "-j", "--status-after"}},
		},
		{
			name:          "rub commit",
			id:            actionRub,
			input:         "zz",
			laneCursor:    1,
			contentCursor: 1,
			outputs: map[string][]byte{
				"rub c1 zz -j --status-after": wrapped,
			},
			want: [][]string{{"rub", "c1", "zz", "-j", "--status-after"}},
		},
		{
			name:       "merge",
			id:         actionMerge,
			laneCursor: 1,
			outputs: map[string][]byte{
				"merge feature/ui -j --status-after": wrapped,
			},
			want: [][]string{{"merge", "feature/ui", "-j", "--status-after"}},
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
				"pull -j --status-after": wrapped,
			},
			want: [][]string{{"pull", "-j", "--status-after"}},
		},
		{
			name:       "push",
			id:         actionPush,
			laneCursor: 1,
			outputs: map[string][]byte{
				"push feature/ui": []byte("pushed"),
				"status -j":       statusRaw,
			},
			want: [][]string{{"push", "feature/ui"}, {"status", "-j"}},
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
				"push feature/ui --with-force": []byte("pushed"),
				"status -j":                    statusRaw,
			},
			want: [][]string{{"push", "feature/ui", "--with-force"}, {"status", "-j"}},
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
				"pr set-draft feature/ui -j --status-after": wrapped,
			},
			want: [][]string{{"pr", "set-draft", "feature/ui", "-j", "--status-after"}},
		},
		{
			name:       "set pr ready",
			id:         actionPRReady,
			laneCursor: 1,
			outputs: map[string][]byte{
				"pr set-ready feature/ui -j --status-after": wrapped,
			},
			want: [][]string{{"pr", "set-ready", "feature/ui", "-j", "--status-after"}},
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
				"resolve finish -j --status-after": wrapped,
			},
			want: [][]string{{"resolve", "finish", "-j", "--status-after"}},
		},
		{
			name:       "resolve cancel",
			id:         actionResolveCancel,
			laneCursor: 1,
			outputs: map[string][]byte{
				"resolve cancel -j --status-after": wrapped,
			},
			want: [][]string{{"resolve", "cancel", "-j", "--status-after"}},
		},
		{
			name: "undo",
			id:   actionUndo,
			outputs: map[string][]byte{
				"undo -j --status-after": wrapped,
			},
			want: [][]string{{"undo", "-j", "--status-after"}},
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
				"oplog restore snap --force -j --status-after": wrapped,
			},
			want: [][]string{{"oplog", "restore", "snap", "--force", "-j", "--status-after"}},
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
				"clean -j --status-after": wrapped,
			},
			want: [][]string{{"clean", "-j", "--status-after"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &actionRunner{outputs: tc.outputs}
			model := newModel(gitbutler.NewClient(".", runner))
			model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
			model.laneCursor = tc.laneCursor
			model.contentCursor = tc.contentCursor

			next, cmd := model.execute(action{ID: tc.id}, tc.input)
			if cmd == nil {
				t.Fatalf("expected command for %s, next=%#v", tc.id, next)
			}
			msg := cmd()
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

func TestUpstreamUpdateSummaryAndConflictToast(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))

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
	branchesRaw, err := os.ReadFile("../gitbutler/testdata/branch_list.json")
	if err != nil {
		t.Fatal(err)
	}
	runner := &actionRunner{outputs: map[string][]byte{
		"status -j":            statusRaw,
		"branch list -j --all": branchesRaw,
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

func TestAutoRefreshStatusOnlyAndBranchRefresh(t *testing.T) {
	statusRaw, err := os.ReadFile("../gitbutler/testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	branchesRaw, err := os.ReadFile("../gitbutler/testdata/branch_list.json")
	if err != nil {
		t.Fatal(err)
	}

	runner := &actionRunner{outputs: map[string][]byte{
		"status -j": statusRaw,
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
	if !reflect.DeepEqual(runner.calls, [][]string{{"status", "-j"}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}

	runner = &actionRunner{outputs: map[string][]byte{
		"status -j":            statusRaw,
		"branch list -j --all": branchesRaw,
	}}
	model = newModel(gitbutler.NewClient(".", runner))
	model.loading = false
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	_, cmd = model.requestAutoRefresh(true)
	if cmd == nil {
		t.Fatal("expected branch auto-refresh command")
	}
	if _, ok := cmd().(autoRefreshMsg); !ok {
		t.Fatalf("unexpected message from branch auto refresh")
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"status", "-j"}, {"branch", "list", "-j", "--all"}}) {
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
