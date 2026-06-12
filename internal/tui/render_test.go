package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

func init() {
	// Ensure deterministic color output in tests, so styled fragments can be
	// asserted regardless of whether the test binary attached a TTY.
	lipgloss.SetColorProfile(termenv.ANSI256)
}

// ansiPrefix returns the leading SGR escape a style emits (everything up to and
// including the first "m"), so tests can assert "this fragment is styled with
// style X" without hard-coding palette color codes.
func ansiPrefix(style lipgloss.Style) string {
	rendered := style.Render("x")
	if i := strings.Index(rendered, "m"); i >= 0 {
		return rendered[:i+1]
	}
	return rendered
}

func TestResponsiveRenderModes(t *testing.T) {
	base := newModel(gitbutler.NewClient(".", nil))
	base.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	base.loading = false

	for _, width := range []int{96, 60} {
		model := base
		model.width = width
		model.height = 32
		view := model.View()
		if !strings.Contains(view, "feature/ui") {
			t.Fatalf("width %d: view does not contain branch name\n%s", width, view)
		}
		if !strings.Contains(view, "preview") {
			t.Fatalf("width %d: view should expose preview strip\n%s", width, view)
		}
	}
}

func TestInitialLoadingStateDoesNotLookEmpty(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.loading = true

	lines := strings.Join(model.laneLines(100, 10), "\n")
	if !strings.Contains(lines, "loading GitButler status") {
		t.Fatalf("initial empty state should explain loading:\n%s", lines)
	}
	if strings.Contains(lines, "no branches") {
		t.Fatalf("initial empty state should not look like an empty workspace:\n%s", lines)
	}
}

func TestStatusErrorStateShowsRetry(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.loading = false
	model.err = errors.New("GitButler command timed out")

	lines := strings.Join(model.laneLines(100, 10), "\n")
	for _, want := range []string{"Could not load GitButler status", "GitButler command timed out", "retry"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("status error state missing %q:\n%s", want, lines)
		}
	}
}

func TestWideRenderUsesKanbanColumns(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 160
	model.height = 32

	view := model.View()
	for _, want := range []string{"unassigned changes", "feature/ui", "ae:sv"} {
		if !strings.Contains(view, want) {
			t.Fatalf("kanban view does not contain %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "drop/assign") {
		t.Fatalf("kanban view should not embed a redundant key hint per column\n%s", view)
	}
	if strings.Contains(view, "feature/unapplied") {
		t.Fatalf("inactive branches should stay out of the workspace kanban\n%s", view)
	}
}

func TestAddBranchModalShowsInactiveBranches(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 120
	model.height = 32
	model.mode = modeBranchPicker

	view := model.View()
	if !strings.Contains(view, "add branch to workspace") || !strings.Contains(view, "feature/unapplied") {
		t.Fatalf("branch picker should expose inactive branches:\n%s", view)
	}
	if got := strings.Count(view, "\n") + 1; got != model.height {
		t.Fatalf("branch picker should preserve terminal height: got %d, want %d\n%s", got, model.height, view)
	}
}

func TestNarrowFallsBackToFocusedList(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = kanbanMinWidth - 10 // <70 = no kanban
	model.height = 32

	view := model.View()
	if !strings.Contains(view, "preview") {
		t.Fatalf("narrow layout should still render preview strip\n%s", view)
	}
}

func TestRenderStaysWithinTerminalGeometry(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{180, 44},
		{140, 40},
		{120, 44},
		{96, 36},
		{80, 28},
		{60, 24},
	} {
		t.Run("kanban_off", func(t *testing.T) {
			model := newModel(gitbutler.NewClient(".", nil))
			model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
			model.loading = false
			model.width = size.width
			model.height = size.height
			view := model.View()
			assertGeometry(t, view, size.width, size.height)
		})
	}
}

func TestKanbanRenderStaysWithinTerminalGeometry(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{180, 44},
		{140, 40},
	} {
		model := newModel(gitbutler.NewClient(".", nil))
		model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
		model.loading = false
		model.width = size.width
		model.height = size.height
		view := model.View()
		if !model.usesKanbanLayout() {
			t.Fatalf("expected kanban layout at %dx%d", size.width, size.height)
		}
		assertGeometry(t, view, size.width, size.height)
	}
}

func TestOverlaysStayWithinTerminalGeometry(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{180, 44},
		{120, 32},
		{96, 28},
		{80, 24},
		{60, 20},
	} {
		for _, mode := range []mode{modeHelp, modeConfirm, modeInput, modePalette, modeBranchPicker, modeTargetPicker} {
			model := newModel(gitbutler.NewClient(".", nil))
			model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
			model.loading = false
			model.width = size.width
			model.height = size.height
			model.mode = mode
			model.palette = model.availableActions()
			model.confirm = confirmState{Action: action{Label: "destructive", ConfirmText: "Really?", Dangerous: true}}
			model.prompt = promptState{Action: action{Label: "stage", InputLabel: "target branch"}, Value: "feature/x"}
			model.targetPicker = targetPickerState{
				Title:  "assign to branch",
				Action: action{ID: actionStage},
				Items:  []pickerItem{{Value: "a", Label: "feature/a", Meta: "3c"}, {Value: "b", Label: "feature/b", Meta: "1c"}},
			}
			view := model.View()
			assertGeometry(t, view, size.width, size.height)
		}
	}
}

func TestStageActionUsesPicker(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 140
	model.height = 40
	model.laneCursor = 0 // zz so a change is selected
	model.contentCursor = 0

	stage := model.actionByID(actionStage)
	if stage.ID != actionStage {
		t.Fatalf("stage action not available: %#v", stage)
	}
	updated, _ := model.startAction(stage)
	m, ok := updated.(Model)
	if !ok {
		t.Fatalf("startAction did not return Model")
	}
	if m.mode != modeTargetPicker {
		t.Fatalf("stage should open the target picker, got mode %d", m.mode)
	}
	if len(m.targetPicker.Items) == 0 {
		t.Fatalf("target picker should be populated with applied branches")
	}
}

func TestRenamePromptIsPreFilledWithCurrentName(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.laneCursor = 1 // an applied branch

	rename := action{ID: actionRename, InputLabel: "new name"}
	updated, _ := model.startAction(rename)
	m, ok := updated.(Model)
	if !ok {
		t.Fatalf("startAction did not return Model")
	}
	if m.prompt.Value == "" || m.prompt.Value != "feature/ui" {
		t.Fatalf("rename prompt should be pre-filled with branch name, got %q", m.prompt.Value)
	}
}

func assertGeometry(t *testing.T, view string, width, height int) {
	t.Helper()
	gotH := lipgloss.Height(view)
	if gotH > height {
		t.Fatalf("rendered height %d > terminal height %d\n%s", gotH, height, view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line %d width %d > terminal width %d\n%q\nfull:\n%s", i, w, width, line, view)
		}
	}
}

func TestFitNeverExceedsWidth(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4, 12, 24} {
		got := fit("apps/api/app/features/ai/orchestratorV4/headless.go", width)
		if lipgloss.Width(got) > width {
			t.Fatalf("fit width %d produced %q width %d", width, got, lipgloss.Width(got))
		}
	}
}

func TestFitTreatsAnsiAsZeroWidth(t *testing.T) {
	// A title that already carries ANSI styling (sync chip + accent name). The
	// previous one-pass fit counted bytes inside `\x1b[...m` as visible chars and
	// truncated the name to nothing — this regression guard prevents that.
	styled := styleErr.Render("↑4!") + " " + styleTitle.Render("fix/workflow-v3-quality-pass")
	for _, width := range []int{32, 40, 60} {
		got := fit(styled, width)
		if !strings.Contains(got, "fix/workflow-v3-quality-pass") {
			t.Fatalf("fit at width %d should keep the visible name: got %q", width, got)
		}
		if w := lipgloss.Width(got); w > width {
			t.Fatalf("fit width %d produced visible width %d", width, w)
		}
	}
}

func TestRenderShowsBranchStatus(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))

	lines := strings.Join(model.laneLines(100, 10), "\n")
	// Branch in fixture is "unpushedCommits" with 1 local commit + conflict.
	if !strings.Contains(lines, "↑1") {
		t.Fatalf("lane lines should expose ahead count via sync chip:\n%s", lines)
	}
	if !strings.Contains(lines, glyphConflict) {
		t.Fatalf("lane lines should expose conflict glyph:\n%s", lines)
	}
}

func TestSyncChipShapes(t *testing.T) {
	mk := func(pushStatus string, commits, upstream int) lane {
		return lane{Kind: laneAppliedBranch, PushStatus: pushStatus, CommitCount: commits, UpstreamCount: upstream}
	}
	cases := []struct {
		name   string
		lane   lane
		expect string // unstyled fragments
	}{
		{"synced", mk("nothingToPush", 3, 0), glyphCheck},
		{"to push", mk("unpushedCommits", 3, 0), "↑3"},
		{"force required", mk("unpushedCommitsRequiringForce", 2, 0), "↑2!"},
		{"to pull", mk("nothingToPush", 0, 4), "↓4"},
		{"both", mk("unpushedCommits", 2, 3), "↓3"},
		{"integrated", mk("integrated", 1, 0), "merged"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := syncChip(c.lane)
			if !strings.Contains(got, c.expect) {
				t.Fatalf("syncChip(%+v) = %q, expected fragment %q", c.lane, got, c.expect)
			}
		})
	}
}

func TestUpstreamConfirmMirrorsGitButlerDialogShape(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.width = 110
	model.height = 36

	view := model.renderUpstreamConfirm()
	// The sobered modal keeps the essential information without the chunky
	// buttons/badges/pills of the original.
	for _, want := range []string{"update from upstream", "incoming change", "branches to rebase", "feature/ui", "y/enter"} {
		if !strings.Contains(view, want) {
			t.Fatalf("upstream confirm should contain %q:\n%s", want, view)
		}
	}
}

func TestRenderKeepsCLIIDsVisible(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 120
	model.height = 28
	model.focus = panelContents
	model.laneCursor = 1

	view := model.View()
	if !strings.Contains(view, "ae:sv") {
		t.Fatalf("view should keep cli id visible:\n%s", view)
	}
}

func TestPadRightExactWidth(t *testing.T) {
	got := padRight("ab", 5)
	if lipgloss.Width(got) != 5 {
		t.Fatalf("padRight = %q width %d, want 5", got, lipgloss.Width(got))
	}
	got = padRight("abcdef", 4)
	if lipgloss.Width(got) != 6 {
		t.Fatalf("padRight should not truncate, got width %d", lipgloss.Width(got))
	}
}

func TestKanbanColumnHasSectionDividerBetweenFilesAndCommits(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 160
	model.height = 32
	model.laneCursor = 1 // focus the active branch which has both files + commits

	view := model.View()
	// The fixture branch (feature/ui) has 1 assigned change + 1 commit. The
	// section divider should sit between them with a "1 commit" label.
	if !strings.Contains(view, "1 commit") {
		t.Fatalf("expected section divider to label the commit section:\n%s", view)
	}
	if !strings.Contains(view, "─") {
		t.Fatalf("expected horizontal divider line between sections:\n%s", view)
	}
}

func TestPreviewHeaderRendersBeforeDiffLoads(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.laneCursor = 1
	model.contentCursor = 0 // a change in the applied branch

	rows := model.previewHeaderRows(60)
	if len(rows) == 0 {
		t.Fatalf("expected sync preview header for a file change, got none")
	}
	joined := strings.Join(rows, "\n")
	// Path is split by ANSI styling (dir dim, basename bold) — assert on parts.
	for _, want := range []string{"internal/tui/", "model.go", "added", "ae:sv"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("file header should expose %q:\n%s", want, joined)
		}
	}

	model.contentCursor = 1 // the first commit
	rows = model.previewHeaderRows(60)
	joined = strings.Join(rows, "\n")
	if !strings.Contains(joined, "build tui shell") {
		t.Fatalf("commit header should expose commit message:\n%s", joined)
	}
}

func TestPluralFormatting(t *testing.T) {
	if got := plural(1, "file", "files"); got != "1 file" {
		t.Fatalf("plural(1) = %q", got)
	}
	if got := plural(7, "file", "files"); got != "7 files" {
		t.Fatalf("plural(7) = %q", got)
	}
}

func TestPreviewZoneDetection(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 160
	model.height = 40
	// previewStripHeight(38) = clamp(38*0.3, 6, 14) = 11
	// mainAreaHeight = 1 (top) + (38-11) = 28 — preview rows are 28..38
	if !model.inPreviewZone(30) {
		t.Fatalf("y=30 should be in preview zone")
	}
	if model.inPreviewZone(10) {
		t.Fatalf("y=10 should NOT be in preview zone")
	}
}

func TestSmallHeightHidesPreview(t *testing.T) {
	if got := previewStripHeight(10); got != 0 {
		t.Fatalf("body height 10 should hide preview, got %d", got)
	}
	if got := previewStripHeight(14); got == 0 {
		t.Fatalf("body height 14 should still expose preview")
	}
}

func TestDiffLineClassification(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string // ANSI fragments that must appear (color codes)
	}{
		// Expected fragments are derived from the styles themselves so palette
		// tweaks don't require hand-editing color codes here.
		{"added", "      22│+	orchestratorV4 \"foo\"", []string{ansiPrefix(styleDiffAdd)}},
		{"removed", "      22│-	old line", []string{ansiPrefix(styleDiffRem)}},
		{"context", "   19 19│ 	keep line", []string{ansiPrefix(styleDiffGutter)}}, // gutter is dim
		{"header", "x9 apps/api/main.go│", []string{ansiPrefix(styleDiffHeader)}},  // header is bold accent
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := styleDiffLine(c.line)
			for _, frag := range c.want {
				if !strings.Contains(out, frag) {
					t.Fatalf("%s: expected %q in %q", c.name, frag, out)
				}
			}
		})
	}
}

func TestBoxDecorationStripping(t *testing.T) {
	if !isBoxDecoration("─────────────╮") {
		t.Fatalf("box header line should be detected as decoration")
	}
	if !isBoxDecoration("─────────────╯") {
		t.Fatalf("box footer line should be detected as decoration")
	}
	if isBoxDecoration("   19 19│ orchestrator x") {
		t.Fatalf("diff line should NOT be detected as decoration")
	}
	if isBoxDecoration("x9 apps/api/main.go│") {
		t.Fatalf("file header line should NOT be detected as decoration")
	}
}

func TestHotbarKeepsMetaKeysAtNarrowWidth(t *testing.T) {
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 60
	model.height = 24

	view := model.View()
	// ANSI escape codes sit between key and label, so check each label alone.
	for _, want := range []string{"quit", "help", "actions", "filter"} {
		if !strings.Contains(view, want) {
			t.Fatalf("hotbar should always keep meta label %q at narrow width\n%s", want, view)
		}
	}
}
