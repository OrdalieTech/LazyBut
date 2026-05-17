package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

type panel int

const (
	panelLanes panel = iota
	panelContents
	panelPreview
)

type mode int

const (
	modeNormal mode = iota
	modeInput
	modeConfirm
	modePalette
	modeBranchPicker
	modeHelp
	modeTargetPicker // generic picker for actions that need to choose from a list (assign, move, etc.)
)

const (
	uiTickInterval              = 90 * time.Millisecond
	autoStatusRefreshInterval   = 3 * time.Second
	autoBranchesRefreshInterval = 30 * time.Second
	autoRefreshTimeout          = 15 * time.Second
)

type actionID string

const (
	actionRefresh          actionID = "refresh"
	actionInstallGitButler actionID = "install_gitbutler"
	actionSetup            actionID = "setup"
	actionSetupInit        actionID = "setup_init"
	actionAddBranch        actionID = "add_branch"
	actionNewBranch        actionID = "new_branch"
	actionNewStacked       actionID = "new_stacked_branch"
	actionStage            actionID = "stage"
	actionApplyToggle      actionID = "apply_toggle"
	actionCommit           actionID = "commit"
	actionRename           actionID = "rename"
	actionDelete           actionID = "delete"
	actionDiscard          actionID = "discard"
	actionAmend            actionID = "amend"
	actionAbsorb           actionID = "absorb"
	actionSquash           actionID = "squash"
	actionUncommit         actionID = "uncommit"
	actionMove             actionID = "move"
	actionRub              actionID = "rub"
	actionMerge            actionID = "merge"
	actionPullCheck        actionID = "pull_check"
	actionPull             actionID = "pull"
	actionPush             actionID = "push"
	actionPushDryRun       actionID = "push_dry_run"
	actionForcePush        actionID = "force_push"
	actionNewPR            actionID = "new_pr"
	actionNewDraftPR       actionID = "new_draft_pr"
	actionPRDraft          actionID = "pr_draft"
	actionPRReady          actionID = "pr_ready"
	actionResolveStatus    actionID = "resolve_status"
	actionResolveFinish    actionID = "resolve_finish"
	actionResolveCancel    actionID = "resolve_cancel"
	actionUndo             actionID = "undo"
	actionSnapshot         actionID = "snapshot"
	actionRestore          actionID = "restore"
	actionCleanDryRun      actionID = "clean_dry_run"
	actionClean            actionID = "clean"
)

type action struct {
	ID          actionID
	Key         string
	Aliases     []string
	Label       string
	InputLabel  string
	ConfirmText string
	Dangerous   bool
}

type promptState struct {
	Action action
	Value  string
}

type confirmState struct {
	Action action
	Input  string
	Cursor int
}

type Model struct {
	client *gitbutler.Client

	width  int
	height int

	data          workspaceData
	loading       bool
	err           error
	toast         string
	toastKind     toastKind
	toastExpires  time.Time
	focus         panel
	mode          mode
	laneCursor    int
	contentCursor int
	previewScroll int
	filter        string
	previewTarget string
	preview       string
	previewErr    error
	selected      map[string]bool
	rangeAnchor   int
	spinnerFrame  int
	ticking       bool

	autoRefreshInFlight        bool
	autoRefreshPending         bool
	autoRefreshPendingBranches bool

	prompt        promptState
	confirm       confirmState
	palette       []action
	paletteCursor int
	branchCursor  int

	targetPicker targetPickerState
}

// targetPickerState backs modeTargetPicker — a generic "select one of these"
// modal used by actions whose input is a branch/commit (assign, move, rub,
// amend). Replaces a free-text prompt with an actual selector.
type targetPickerState struct {
	Title    string
	Action   action
	Items    []pickerItem
	Cursor   int
	Multi    bool         // multi-select mode (space toggles)
	Selected map[int]bool // set when Multi is true
}

func (t *targetPickerState) toggle(idx int) {
	if t.Selected == nil {
		t.Selected = map[int]bool{}
	}
	if t.Selected[idx] {
		delete(t.Selected, idx)
	} else {
		t.Selected[idx] = true
	}
}

func (t targetPickerState) selectedValues() []string {
	if !t.Multi || len(t.Selected) == 0 {
		return nil
	}
	out := make([]string, 0, len(t.Selected))
	for idx := range t.Selected {
		if idx >= 0 && idx < len(t.Items) {
			out = append(out, t.Items[idx].Value)
		}
	}
	return out
}

type pickerItem struct {
	Value string // payload passed to the action's execute()
	Label string // user-facing main text
	Meta  string // dim secondary text (optional)
}

type toastKind int

const (
	toastInfo toastKind = iota
	toastSuccess
	toastError
)

type tickMsg time.Time

type oplogLoadedMsg struct {
	entries []gitbutler.OplogEntry
	err     error
}

type autoRefreshTickMsg struct {
	branches bool
}

type loadedMsg struct {
	status   *gitbutler.WorkspaceStatus
	branches *gitbutler.BranchList
	err      error
}

type upstreamRefreshMsg struct {
	status   *gitbutler.WorkspaceStatus
	branches *gitbutler.BranchList
	err      error
}

type autoRefreshMsg struct {
	status   *gitbutler.WorkspaceStatus
	branches *gitbutler.BranchList
	err      error
}

type mutationMsg struct {
	status *gitbutler.WorkspaceStatus
	err    error
	label  string
}

type textMsg struct {
	target string
	body   string
	err    error
}

type installGitButlerMsg struct {
	body string
	err  error
}

func Run(client *gitbutler.Client) error {
	_, err := tea.NewProgram(newModel(client), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func newModel(client *gitbutler.Client) Model {
	return Model{
		client:      client,
		loading:     true,
		focus:       panelLanes,
		mode:        modeNormal,
		selected:    map[string]bool{},
		rangeAnchor: -1,
	}
}

func (m Model) maybePromptForBootstrap(err error) Model {
	if err == nil || m.mode != modeNormal {
		return m
	}
	if gitbutler.IsCLINotFound(err) {
		m.confirm = confirmState{Action: action{
			ID:          actionInstallGitButler,
			Key:         "i",
			Label:       "install GitButler CLI",
			ConfirmText: installGitButlerConfirmText(),
		}}
		m.mode = modeConfirm
	}
	if gitbutler.IsSetupRequired(err) {
		m.confirm = confirmState{Action: action{
			ID:          actionSetup,
			Key:         "g",
			Label:       "setup GitButler",
			ConfirmText: setupGitButlerConfirmText(err),
		}}
		m.mode = modeConfirm
	}
	return m
}

func isBootstrapError(err error) bool {
	return gitbutler.IsCLINotFound(err) || gitbutler.IsSetupRequired(err)
}

func (m Model) isBootstrapPrompt() bool {
	return m.mode == modeConfirm && isBootstrapAction(m.confirm.Action.ID) && isBootstrapError(m.err)
}

func (m Model) hasBootstrapIssue() bool {
	return isBootstrapError(m.err)
}

func isBootstrapAction(id actionID) bool {
	return id == actionInstallGitButler || id == actionSetup
}

func installGitButlerConfirmText() string {
	return "GitButler CLI (`but`) is required.\n\nCommand:\n  curl -fsSL https://gitbutler.com/install.sh | sh\n\nInstall it now?"
}

func setupGitButlerConfirmText(err error) string {
	lines := []string{"This repository is not set up for GitButler yet."}
	var cliErr gitbutler.CLIError
	if errors.As(err, &cliErr) {
		if cliErr.Message != "" {
			lines = append(lines, "", cliErr.Message)
		}
		if cliErr.Hint != "" {
			lines = append(lines, "Hint: "+cliErr.Hint)
		}
	}
	lines = append(lines, "", "Command:", "  but setup", "", "Run it here now?")
	return strings.Join(lines, "\n")
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), tickCmd(), autoRefreshTickCmd(false), autoRefreshTickCmd(true))
}

func tickCmd() tea.Cmd {
	return tea.Tick(uiTickInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func autoRefreshTickCmd(branches bool) tea.Cmd {
	interval := autoStatusRefreshInterval
	if branches {
		interval = autoBranchesRefreshInterval
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return autoRefreshTickMsg{branches: branches}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case loadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m = m.replaceData(msg.status, msg.branches)
			return m.withPreview()
		}
		if isBootstrapError(msg.err) {
			return m.maybePromptForBootstrap(msg.err), nil
		}
		m.setToast(msg.err.Error(), toastError)
		return m.maybePromptForBootstrap(msg.err), nil
	case upstreamRefreshMsg:
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			m.setToast(msg.err.Error(), toastError)
			return m, nil
		}
		m = m.replaceData(msg.status, msg.branches)
		if m.incomingChangeCount() == 0 {
			m.setToast("no upstream update", toastInfo)
			return m.withPreview()
		}
		m.confirm = confirmState{Action: action{ID: actionPull, Key: "U", Label: "update from upstream", ConfirmText: m.upstreamUpdateConfirmText()}}
		m.mode = modeConfirm
		return m, nil
	case mutationMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil && msg.status != nil {
			m = m.replaceData(msg.status, m.data.Branches)
			toastText, kind := m.mutationToast(msg.label, msg.status)
			m.setToast(toastText, kind)
			m, previewCmd := m.withPreview()
			return m, tea.Batch(m.branchListCmd(), previewCmd)
		}
		if msg.err != nil {
			m.setToast(msg.err.Error(), toastError)
		}
		return m, nil
	case installGitButlerMsg:
		m.loading = false
		if msg.err != nil {
			m.err = fmt.Errorf("%w: %s", msg.err, strings.TrimSpace(msg.body))
			m.setToast(m.err.Error(), toastError)
			return m, nil
		}
		m.err = nil
		m.setToast("GitButler CLI installed; refreshing", toastSuccess)
		return m.startLoading(), m.refreshCmd()
	case tickMsg:
		m.spinnerFrame++
		// Toast auto-fades after a few seconds.
		if m.toast != "" && !m.toastExpires.IsZero() && time.Now().After(m.toastExpires) {
			m.toast = ""
		}
		return m, tickCmd()
	case autoRefreshTickMsg:
		m, refreshCmd := m.requestAutoRefresh(msg.branches)
		return m, tea.Batch(autoRefreshTickCmd(msg.branches), refreshCmd)
	case autoRefreshMsg:
		m.autoRefreshInFlight = false
		if m.loading {
			m.autoRefreshPending = false
			m.autoRefreshPendingBranches = false
			return m, nil
		}

		cmds := []tea.Cmd{}
		if msg.status != nil {
			branches := m.data.Branches
			if msg.branches != nil {
				branches = msg.branches
			}
			m = m.replaceData(msg.status, branches)
			var previewCmd tea.Cmd
			m, previewCmd = m.withPreview()
			cmds = append(cmds, previewCmd)
		}
		if msg.err != nil {
			m.setToast("background refresh: "+msg.err.Error(), toastError)
			m.autoRefreshPending = false
			m.autoRefreshPendingBranches = false
			return m, tea.Batch(cmds...)
		}
		if m.autoRefreshPending {
			branches := m.autoRefreshPendingBranches
			m.autoRefreshPending = false
			m.autoRefreshPendingBranches = false
			var refreshCmd tea.Cmd
			m, refreshCmd = m.requestAutoRefresh(branches)
			cmds = append(cmds, refreshCmd)
		}
		return m, tea.Batch(cmds...)
	case textMsg:
		if msg.target == m.previewTarget {
			m.previewErr = msg.err
			if msg.err == nil {
				m.preview = msg.body
			}
		} else if msg.target == "message" {
			m.previewErr = msg.err
			m.preview = msg.body
			m.previewTarget = msg.target
		}
		return m, nil
	case oplogLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.setToast(msg.err.Error(), toastError)
			return m, nil
		}
		items := make([]pickerItem, 0, len(msg.entries))
		for _, e := range msg.entries {
			items = append(items, pickerItem{
				Value: e.ID,
				Label: oplogEntryLabel(e),
				Meta:  shortHash(e.ID),
			})
		}
		if len(items) == 0 {
			m.setToast("no oplog entries to restore", toastInfo)
			return m, nil
		}
		m.targetPicker = targetPickerState{
			Title: "restore oplog snapshot",
			Action: action{
				ID:          actionRestore,
				Label:       "restore oplog snapshot",
				Dangerous:   true,
				ConfirmText: "Restore this snapshot? Uncommitted changes will be replaced.",
			},
			Items: items,
		}
		m.mode = modeTargetPicker
		return m, nil
	case tea.MouseMsg:
		switch m.mode {
		case modeBranchPicker:
			return m.handleBranchPickerMouse(msg)
		case modePalette:
			return m.handlePaletteMouse(msg)
		case modeTargetPicker:
			return m.handleTargetPickerMouse(msg)
		case modeConfirm:
			return m.handleConfirmMouse(msg)
		case modeNormal:
			return m.handleMouse(msg)
		default:
			return m, nil
		}
	case tea.KeyMsg:
		return m.handleKey(msg)
	default:
		return m, nil
	}
}

func (m Model) handleMouse(mouse tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		if m.inPreviewZone(mouse.Y) {
			return m.scrollPreview(-3), nil
		}
		if m.usesKanbanLayout() {
			return m.moveKanbanItem(-3)
		}
		return m.move(-3)
	case tea.MouseButtonWheelDown:
		if m.inPreviewZone(mouse.Y) {
			return m.scrollPreview(3), nil
		}
		if m.usesKanbanLayout() {
			return m.moveKanbanItem(3)
		}
		return m.move(3)
	case tea.MouseButtonWheelLeft:
		if m.usesKanbanLayout() && !m.inPreviewZone(mouse.Y) {
			return m.moveLane(-1)
		}
	case tea.MouseButtonWheelRight:
		if m.usesKanbanLayout() && !m.inPreviewZone(mouse.Y) {
			return m.moveLane(1)
		}
	case tea.MouseButtonLeft:
		if mouse.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.click(mouse.X, mouse.Y)
	}
	return m, nil
}

// mainAreaHeight returns the row count consumed by top bar + main area
// (everything above the preview strip). 1 row for the top bar plus the
// main rendered box height.
func (m Model) mainAreaHeight() int {
	bodyH := max(1, m.height-2) // minus top bar + hotbar
	previewH := previewStripHeight(bodyH)
	mainH := max(4, bodyH-previewH)
	return 1 + mainH // top bar + main box
}

func (m Model) hasPreviewStrip() bool {
	return previewStripHeight(max(1, m.height-2)) > 0
}

func (m Model) inPreviewZone(y int) bool {
	if !m.hasPreviewStrip() {
		return false
	}
	mainEnd := m.mainAreaHeight()
	hotbarStart := m.height - 1
	return y >= mainEnd && y < hotbarStart
}

func (m Model) scrollPreview(delta int) Model {
	m.focus = panelPreview
	m.previewScroll += delta
	if m.previewScroll < 0 {
		m.previewScroll = 0
	}
	return m
}

func (m Model) click(x, y int) (tea.Model, tea.Cmd) {
	if y < 1 || y >= max(1, m.height-1) {
		return m, nil
	}
	if m.inPreviewZone(y) {
		mainEnd := m.mainAreaHeight()
		m.focus = panelPreview
		m.previewScroll = max(0, y-mainEnd-1)
		return m, nil
	}
	if m.usesKanbanLayout() {
		return m.clickKanban(x, y)
	}
	return m.clickList(x, y)
}

func (m Model) clickKanban(x, y int) (tea.Model, tea.Cmd) {
	lanes := m.filteredLanes()
	if len(lanes) == 0 {
		return m, nil
	}
	count, width := m.kanbanGeometry(m.width)
	colIdx := max(0, x) / max(1, width)
	var idx int
	if colIdx == 0 {
		idx = 0 // pinned zz
	} else if len(lanes) > 1 && count > 1 {
		rest := lanes[1:]
		slots := count - 1
		restStart := m.kanbanRestStart(len(rest), slots)
		idx = 1 + restStart + colIdx - 1
		if idx >= len(lanes) {
			return m, nil
		}
	} else {
		return m, nil
	}
	m.laneCursor = idx
	// rows inside the column: 1 (border) + 1 (title) + 1 (meta) + 1 (empty) = 4 above first item
	m.contentCursor = max(0, y-5)
	m.focus = panelContents
	m.clampCursors()
	return m.withPreview()
}

func (m Model) clickList(x, y int) (tea.Model, tea.Cmd) {
	// Single-panel main: clicks select either a lane (when focused on lanes)
	// or an item (when focused on contents). y=1 is the top bar.
	row := max(0, y-3)
	switch m.focus {
	case panelLanes:
		m.laneCursor = row
		m.contentCursor = 0
	case panelContents:
		m.contentCursor = row
	default:
		m.focus = panelLanes
		m.laneCursor = row
	}
	m.clampCursors()
	return m.withPreview()
}

func (m Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeInput:
		return m.handleInputKey(key)
	case modeConfirm:
		return m.handleConfirmKey(key)
	case modePalette:
		return m.handlePaletteKey(key)
	case modeBranchPicker:
		return m.handleBranchPickerKey(key)
	case modeTargetPicker:
		return m.handleTargetPickerKey(key)
	case modeHelp:
		if key.String() == "esc" || key.String() == "?" || key.String() == "q" {
			m.mode = modeNormal
		}
		return m, nil
	}

	switch key.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "1":
		m.focus = panelLanes
		return m, nil
	case "2":
		m.focus = panelContents
		return m, nil
	case "3":
		m.focus = panelPreview
		return m, nil
	case "?":
		m.mode = modeHelp
		return m, nil
	case ":":
		m.palette = m.availableActions()
		m.paletteCursor = 0
		m.mode = modePalette
		return m, nil
	case "/":
		m.prompt = promptState{Action: action{ID: actionID("filter"), InputLabel: "filter"}}
		m.prompt.Value = m.filter
		m.mode = modeInput
		return m, nil
	case "esc":
		m.toast = ""
		m.filter = ""
		m.selected = map[string]bool{}
		m.rangeAnchor = -1
		m.clampCursors()
		return m, nil
	case " ":
		return m.toggleSelection()
	case "v":
		return m.rangeSelection()
	case "r", "ctrl+r":
		return m.startLoading(), m.refreshCmd()
	case "ctrl+u":
		return m.scrollPreview(-5), nil
	case "ctrl+d":
		return m.scrollPreview(5), nil
	}

	if m.usesKanbanLayout() {
		switch key.String() {
		case "tab", "l", "right", "enter":
			return m.moveLane(1)
		case "shift+tab", "h", "left":
			return m.moveLane(-1)
		case "j", "down":
			return m.moveKanbanItem(1)
		case "k", "up":
			return m.moveKanbanItem(-1)
		case "pgdown":
			return m.moveKanbanItem(10)
		case "pgup":
			return m.moveKanbanItem(-10)
		}
	}

	switch key.String() {
	case "tab", "l", "right", "enter":
		m.focus = (m.focus + 1) % 3
		return m, nil
	case "shift+tab", "h", "left":
		m.focus = (m.focus + 2) % 3
		return m, nil
	case "j", "down":
		return m.move(1)
	case "k", "up":
		return m.move(-1)
	case "pgdown":
		return m.move(10)
	case "pgup":
		return m.move(-10)
	}

	if key.String() == "a" {
		if item, ok := m.selectedContent(); ok && item.Kind == contentChange {
			return m.startAction(m.actionByID(actionStage))
		}
	}

	for _, action := range m.availableActions() {
		if action.matches(key.String()) {
			return m.startAction(action)
		}
	}
	return m, nil
}

func (m Model) handleInputKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.prompt = promptState{}
		return m, nil
	case tea.KeyEnter:
		action := m.prompt.Action
		input := strings.TrimSpace(m.prompt.Value)
		m.mode = modeNormal
		m.prompt = promptState{}
		if action.ID == actionID("filter") {
			m.filter = input
			m.clampCursors()
			return m, nil
		}
		if action.Dangerous || action.ConfirmText != "" {
			m.confirm = confirmState{Action: action, Input: input}
			m.mode = modeConfirm
			return m, nil
		}
		return m.execute(action, input)
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.prompt.Value) > 0 {
			m.prompt.Value = m.prompt.Value[:len(m.prompt.Value)-1]
		}
	default:
		if key.Type == tea.KeyRunes {
			m.prompt.Value += key.String()
		}
	}
	return m, nil
}

func (m Model) handleConfirmKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm.Action.ID == actionPull {
		return m.handleUpstreamConfirmKey(key)
	}
	switch key.String() {
	case "y", "enter":
		return m.acceptConfirm()
	case "n", "esc":
		return m.cancelConfirm()
	default:
		return m, nil
	}
}

func (m Model) acceptConfirm() (tea.Model, tea.Cmd) {
	action := m.confirm.Action
	input := m.confirm.Input
	m.mode = modeNormal
	m.confirm = confirmState{}
	return m.execute(action, input)
}

func (m Model) cancelConfirm() (tea.Model, tea.Cmd) {
	actionID := m.confirm.Action.ID
	m.mode = modeNormal
	m.confirm = confirmState{}
	switch actionID {
	case actionInstallGitButler:
		m.setToast("GitButler install skipped; press i to install later", toastInfo)
	case actionSetup:
		m.setToast("GitButler setup skipped; press g to run setup later", toastInfo)
	}
	return m, nil
}

func (m Model) handleUpstreamConfirmKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	branchCount := len(m.upstreamBranchLanes())
	switch key.String() {
	case "j", "down":
		if branchCount > 0 {
			m.confirm.Cursor = min(branchCount-1, m.confirm.Cursor+1)
		}
		return m, nil
	case "k", "up":
		m.confirm.Cursor = max(0, m.confirm.Cursor-1)
		return m, nil
	case "pgdown":
		if branchCount > 0 {
			m.confirm.Cursor = min(branchCount-1, m.confirm.Cursor+5)
		}
		return m, nil
	case "pgup":
		m.confirm.Cursor = max(0, m.confirm.Cursor-5)
		return m, nil
	case "u":
		m.mode = modeNormal
		m.confirm = confirmState{}
		return m.execute(action{ID: actionPullCheck}, "")
	case "y", "enter":
		action := m.confirm.Action
		input := m.confirm.Input
		m.mode = modeNormal
		m.confirm = confirmState{}
		return m.execute(action, input)
	case "n", "esc":
		m.mode = modeNormal
		m.confirm = confirmState{}
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleConfirmMouse(mouse tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.confirm.Action.ID != actionPull {
		if mouse.Button == tea.MouseButtonLeft && mouse.Action == tea.MouseActionPress {
			confirm, cancel := m.genericConfirmFooterAt(mouse.X, mouse.Y)
			if confirm {
				return m.acceptConfirm()
			}
			if cancel {
				return m.cancelConfirm()
			}
		}
		return m, nil
	}
	lanes := m.upstreamBranchLanes()
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		m.confirm.Cursor = max(0, m.confirm.Cursor-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		if len(lanes) > 0 {
			m.confirm.Cursor = min(len(lanes)-1, m.confirm.Cursor+1)
		}
		return m, nil
	case tea.MouseButtonLeft:
		if mouse.Action != tea.MouseActionPress && mouse.Action != tea.MouseActionMotion {
			return m, nil
		}
		if row, ok := m.upstreamConfirmRowAt(mouse.Y); ok {
			m.confirm.Cursor = row
			return m, nil
		}
		if mouse.Action == tea.MouseActionPress && m.isUpstreamConfirmFooter(mouse.X, mouse.Y) {
			if mouse.X < m.width/3 {
				m.mode = modeNormal
				m.confirm = confirmState{}
				return m, nil
			}
			action := m.confirm.Action
			input := m.confirm.Input
			m.mode = modeNormal
			m.confirm = confirmState{}
			return m.execute(action, input)
		}
	}
	return m, nil
}

func (m Model) genericConfirmFooterAt(x, y int) (confirm bool, cancel bool) {
	startX, startY, width, height := overlayBounds(m.width, m.height, m.renderConfirm())
	if x < startX || x >= startX+width || y < startY || y >= startY+height {
		return false, false
	}
	if y < startY+height-4 {
		return false, false
	}
	if x < startX+width/2 {
		return true, false
	}
	return false, true
}

func (m Model) handlePaletteKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "j", "down":
		if m.paletteCursor < len(m.palette)-1 {
			m.paletteCursor++
		}
		return m, nil
	case "k", "up":
		if m.paletteCursor > 0 {
			m.paletteCursor--
		}
		return m, nil
	case "enter":
		if len(m.palette) == 0 {
			m.mode = modeNormal
			return m, nil
		}
		action := m.palette[m.paletteCursor]
		m.mode = modeNormal
		return m.startAction(action)
	default:
		for _, action := range m.palette {
			if action.matches(key.String()) {
				m.mode = modeNormal
				return m.startAction(action)
			}
		}
		return m, nil
	}
}

func (m Model) handleBranchPickerKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	branches := m.data.BranchOptions
	switch key.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "j", "down":
		if m.branchCursor < len(branches)-1 {
			m.branchCursor++
		}
		return m, nil
	case "k", "up":
		if m.branchCursor > 0 {
			m.branchCursor--
		}
		return m, nil
	case "n":
		m.mode = modeNormal
		return m.startAction(action{ID: actionNewBranch, Key: "n", Label: "new branch", InputLabel: "branch name"})
	case "enter":
		if len(branches) == 0 {
			m.mode = modeNormal
			return m, nil
		}
		if m.branchCursor >= len(branches) {
			m.branchCursor = len(branches) - 1
		}
		branch := branches[m.branchCursor].Name
		m.mode = modeNormal
		return m.startLoading(), m.mutationCmd("branch added", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Apply(context.Background(), branch)
		})
	default:
		return m, nil
	}
}

func (m Model) handleTargetPickerKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := m.targetPicker
	switch key.String() {
	case "esc":
		m.mode = modeNormal
		m.targetPicker = targetPickerState{}
		return m, nil
	case "j", "down":
		if picker.Cursor < len(picker.Items)-1 {
			m.targetPicker.Cursor = picker.Cursor + 1
		}
		return m, nil
	case "k", "up":
		if picker.Cursor > 0 {
			m.targetPicker.Cursor = picker.Cursor - 1
		}
		return m, nil
	case " ":
		if picker.Multi && len(picker.Items) > 0 {
			m.targetPicker.toggle(picker.Cursor)
		}
		return m, nil
	case "enter":
		if len(picker.Items) == 0 {
			m.mode = modeNormal
			return m, nil
		}
		action := picker.Action
		var value string
		if picker.Multi {
			vals := m.targetPicker.selectedValues()
			if len(vals) == 0 {
				vals = []string{picker.Items[picker.Cursor].Value}
			}
			value = strings.Join(vals, " ")
		} else {
			value = picker.Items[picker.Cursor].Value
		}
		m.mode = modeNormal
		m.targetPicker = targetPickerState{}
		if action.Dangerous || action.ConfirmText != "" {
			m.confirm = confirmState{Action: action, Input: value}
			m.mode = modeConfirm
			return m, nil
		}
		return m.execute(action, value)
	}
	return m, nil
}

func (m Model) handleTargetPickerMouse(mouse tea.MouseMsg) (tea.Model, tea.Cmd) {
	picker := m.targetPicker
	if len(picker.Items) == 0 {
		return m, nil
	}
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		if picker.Cursor > 0 {
			m.targetPicker.Cursor = picker.Cursor - 1
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		if picker.Cursor < len(picker.Items)-1 {
			m.targetPicker.Cursor = picker.Cursor + 1
		}
		return m, nil
	case tea.MouseButtonLeft:
		row, ok := paletteRowAt(m.height, len(picker.Items), mouse.Y)
		if !ok {
			if mouse.Action == tea.MouseActionPress {
				m.mode = modeNormal
				m.targetPicker = targetPickerState{}
			}
			return m, nil
		}
		switch mouse.Action {
		case tea.MouseActionMotion:
			m.targetPicker.Cursor = row
		case tea.MouseActionPress:
			m.targetPicker.Cursor = row
			if picker.Multi {
				// In multi-select, clicking toggles instead of confirming —
				// the user finalises with Enter so they can pick several rows.
				m.targetPicker.toggle(row)
				return m, nil
			}
			action := picker.Action
			value := picker.Items[row].Value
			m.mode = modeNormal
			m.targetPicker = targetPickerState{}
			if action.Dangerous || action.ConfirmText != "" {
				m.confirm = confirmState{Action: action, Input: value}
				m.mode = modeConfirm
				return m, nil
			}
			return m.execute(action, value)
		}
	}
	return m, nil
}

// handlePaletteMouse routes mouse events while the action palette is open.
// Hover moves the cursor; left-click selects + executes the action under the
// pointer. Wheel scrolls.
func (m Model) handlePaletteMouse(mouse tea.MouseMsg) (tea.Model, tea.Cmd) {
	if len(m.palette) == 0 {
		if mouse.Button == tea.MouseButtonLeft && mouse.Action == tea.MouseActionPress {
			m.mode = modeNormal
		}
		return m, nil
	}
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		if m.paletteCursor > 0 {
			m.paletteCursor--
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		if m.paletteCursor < len(m.palette)-1 {
			m.paletteCursor++
		}
		return m, nil
	}
	row, ok := paletteRowAt(m.height, len(m.palette), mouse.Y)
	if !ok {
		// Click outside the palette closes it.
		if mouse.Button == tea.MouseButtonLeft && mouse.Action == tea.MouseActionPress {
			m.mode = modeNormal
		}
		return m, nil
	}
	switch mouse.Action {
	case tea.MouseActionMotion:
		m.paletteCursor = row
		return m, nil
	case tea.MouseActionPress:
		if mouse.Button != tea.MouseButtonLeft {
			return m, nil
		}
		m.paletteCursor = row
		action := m.palette[row]
		m.mode = modeNormal
		return m.startAction(action)
	}
	return m, nil
}

// paletteRowAt translates a viewport y-coordinate to an item index within the
// rendered palette, returning false when the y is outside the palette body.
func paletteRowAt(totalHeight, itemCount, y int) (int, bool) {
	// modal layout (renderPalette): header (1) + blank (1) + body lines (n)
	// + blank (1) + footer (1) — wrapped by styleOverlay border + padding.
	// styleOverlay.Padding(1, 2): adds 1 row top/bottom, border adds 2.
	// Total internal vertical: 2 border + 2 padding + 1 header + 1 blank +
	// itemCount + 1 blank + 1 footer = 8 + itemCount rows.
	modalH := 8 + itemCount
	if modalH > totalHeight {
		modalH = totalHeight
	}
	startY := max(0, (totalHeight-modalH)/2)
	// First item row sits below: startY + border(1) + pad(1) + header(1) + blank(1) = startY + 4
	firstItem := startY + 4
	idx := y - firstItem
	if idx < 0 || idx >= itemCount {
		return 0, false
	}
	return idx, true
}

func (m Model) handleBranchPickerMouse(mouse tea.MouseMsg) (tea.Model, tea.Cmd) {
	branches := m.data.BranchOptions
	if len(branches) == 0 {
		return m, nil
	}
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		m.branchCursor--
		if m.branchCursor < 0 {
			m.branchCursor = 0
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		m.branchCursor++
		if m.branchCursor >= len(branches) {
			m.branchCursor = len(branches) - 1
		}
		return m, nil
	case tea.MouseButtonLeft:
		row, ok := branchPickerRowAt(m.height, len(branches), m.branchCursor, mouse.Y)
		if !ok {
			if mouse.Action == tea.MouseActionPress {
				m.mode = modeNormal
			}
			return m, nil
		}
		switch mouse.Action {
		case tea.MouseActionMotion:
			m.branchCursor = row
			return m, nil
		case tea.MouseActionPress:
			m.branchCursor = row
			branch := branches[row].Name
			m.mode = modeNormal
			return m.startLoading(), m.mutationCmd("branch added", func() (*gitbutler.WorkspaceStatus, error) {
				return m.client.Apply(context.Background(), branch)
			})
		}
	}
	return m, nil
}

// branchPickerRowAt maps a viewport y to an item index inside the branch picker
// modal, or returns false when the y is outside it.
func branchPickerRowAt(totalHeight, total, cursor, y int) (int, bool) {
	height := branchPickerWindowHeight(totalHeight)
	visible := min(total, height)
	modalH := visible + 8 // border 2 + pad 2 + header 1 + blank 1 + items + blank 1 + footer 1
	if modalH > totalHeight {
		modalH = totalHeight
	}
	startY := max(0, (totalHeight-modalH)/2)
	firstItem := startY + 4
	rowInView := y - firstItem
	if rowInView < 0 || rowInView >= visible {
		return 0, false
	}
	return windowStart(total, cursor, height) + rowInView, true
}

func branchPickerWindowHeight(height int) int {
	return min(12, max(4, height-10))
}

func (m Model) startAction(action action) (tea.Model, tea.Cmd) {
	// actionRestore needs to load the oplog list first, then show a picker.
	if action.ID == actionRestore {
		m = m.startLoading()
		return m, m.oplogListCmd()
	}
	if action.ID == actionPull && m.incomingChangeCount() == 0 {
		return m.startLoading(), m.upstreamRefreshCmd()
	}
	// Some actions are best served by a picker instead of free-text input.
	if picker, ok := m.pickerForAction(action); ok {
		m.targetPicker = picker
		m.mode = modeTargetPicker
		return m, nil
	}
	if action.InputLabel != "" {
		m.prompt = promptState{Action: action, Value: m.initialPromptValue(action)}
		m.mode = modeInput
		return m, nil
	}
	if action.Dangerous || action.ConfirmText != "" {
		m.confirm = confirmState{Action: action}
		m.mode = modeConfirm
		return m, nil
	}
	return m.execute(action, "")
}

// pickerForAction returns a populated targetPickerState for actions whose input
// is best chosen from a list. Returns false when the action should keep its
// free-text prompt (e.g. commit messages, branch names — new info, not picks).
func (m Model) pickerForAction(a action) (targetPickerState, bool) {
	switch a.ID {
	case actionStage:
		items := m.branchItems()
		if len(items) == 0 {
			return targetPickerState{}, false
		}
		return targetPickerState{Title: "assign to branch", Action: a, Items: items}, true
	case actionMove, actionRub:
		items := m.branchItems()
		// move/rub can also target zz (unassigned).
		items = append([]pickerItem{{Value: "zz", Label: "zz", Meta: "unassigned"}}, items...)
		if len(items) == 1 {
			return targetPickerState{}, false
		}
		title := "move target"
		if a.ID == actionRub {
			title = "rub into"
		}
		return targetPickerState{Title: title, Action: a, Items: items}, true
	case actionAmend:
		items := m.commitItems()
		if len(items) == 0 {
			return targetPickerState{}, false
		}
		return targetPickerState{Title: "amend into commit", Action: a, Items: items}, true
	case actionSquash:
		items := m.commitItems()
		if len(items) < 2 {
			return targetPickerState{}, false
		}
		// Pre-select any commits the user already batch-selected via space.
		preselected := map[int]bool{}
		for idx, item := range items {
			if m.selected[item.Value] {
				preselected[idx] = true
			}
		}
		return targetPickerState{Title: "squash commits (space to toggle)", Action: a, Items: items, Multi: true, Selected: preselected}, true
	}
	return targetPickerState{}, false
}

func oplogEntryLabel(e gitbutler.OplogEntry) string {
	title := e.Details.Title
	if title == "" {
		title = e.Details.Operation
	}
	if title == "" {
		title = "(no title)"
	}
	if e.CreatedAt > 0 {
		t := time.UnixMilli(e.CreatedAt)
		title = t.Format("Jan 02 15:04") + " · " + title
	}
	return title
}

func shortHash(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

func (m Model) oplogListCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entries, err := client.OplogList(ctx)
		return oplogLoadedMsg{entries: entries, err: err}
	}
}

func (m Model) branchItems() []pickerItem {
	out := []pickerItem{}
	for _, lane := range m.data.Lanes {
		if lane.Kind != laneAppliedBranch {
			continue
		}
		out = append(out, pickerItem{Value: lane.Name, Label: lane.Name, Meta: pickerBranchMeta(lane)})
	}
	return out
}

func pickerBranchMeta(lane lane) string {
	parts := []string{}
	if lane.CommitCount > 0 {
		parts = append(parts, fmt.Sprintf("%dc", lane.CommitCount))
	}
	if lane.ChangeCount > 0 {
		parts = append(parts, fmt.Sprintf("%df", lane.ChangeCount))
	}
	return strings.Join(parts, " ")
}

func (m Model) commitItems() []pickerItem {
	out := []pickerItem{}
	for _, item := range m.contents() {
		if item.Kind != contentCommit && item.Kind != contentUpstreamCommit {
			continue
		}
		out = append(out, pickerItem{Value: item.ID, Label: item.Label, Meta: item.ID})
	}
	return out
}

// initialPromptValue returns a sensible pre-filled value for an input prompt —
// for example the current branch name for rename, or a timestamp for snapshot —
// so the user doesn't have to type from scratch.
func (m Model) initialPromptValue(a action) string {
	lane, _ := m.selectedLane()
	switch a.ID {
	case actionRename:
		return lane.Name
	case actionSnapshot:
		return time.Now().Format("2006-01-02 15:04 — ")
	}
	return ""
}

func (a action) matches(key string) bool {
	if a.Key == key {
		return true
	}
	for _, alias := range a.Aliases {
		if alias == key {
			return true
		}
	}
	return false
}

func (m Model) actionByID(id actionID) action {
	for _, action := range m.availableActions() {
		if action.ID == id {
			return action
		}
	}
	return action{ID: id}
}

func (m Model) execute(action action, input string) (tea.Model, tea.Cmd) {
	ctx := context.Background()
	selectedLane, _ := m.selectedLane()
	selectedContent, hasContent := m.selectedContent()
	branchRef := selectedLane.Name
	changeIDs := m.selectedContentIDs(contentChange)
	if len(changeIDs) == 0 && hasContent && selectedContent.Kind == contentChange {
		changeIDs = []string{selectedContent.ID}
	}
	commitIDs := m.selectedContentIDs(contentCommit)
	if len(commitIDs) == 0 && hasContent && selectedContent.Kind == contentCommit {
		commitIDs = []string{selectedContent.ID}
	}

	switch action.ID {
	case actionAddBranch:
		return m.openBranchPicker()
	case actionRefresh:
		return m.startLoading(), m.refreshCmd()
	case actionInstallGitButler:
		return m.startLoading(), m.installGitButlerCmd()
	case actionSetup:
		return m.startLoading(), m.mutationCmd("GitButler setup complete", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Setup(ctx, false)
		})
	case actionSetupInit:
		return m.startLoading(), m.mutationCmd("GitButler setup complete", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Setup(ctx, true)
		})
	case actionNewBranch:
		return m.startLoading(), m.mutationCmd("branch created", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.NewBranch(ctx, input, "")
		})
	case actionNewStacked:
		anchor := selectedLane.Name
		return m.startLoading(), m.mutationCmd("stacked branch created", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.NewBranch(ctx, input, anchor)
		})
	case actionStage:
		return m.startLoading(), m.mutationCmd("change assigned", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.StageMany(ctx, changeIDs, input)
		})
	case actionApplyToggle:
		return m.startLoading(), m.mutationCmd("branch visibility changed", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Unapply(ctx, branchRef, false)
		})
	case actionCommit:
		return m.startLoading(), m.mutationCmd("committed", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Commit(ctx, branchRef, input, changeIDs, false)
		})
	case actionRename:
		return m.startLoading(), m.mutationCmd("renamed", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Reword(ctx, firstNonEmpty(selectedLane.ID, selectedLane.Name), input)
		})
	case actionDelete:
		return m.startLoading(), m.mutationCmd("branch deleted", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.DeleteBranch(ctx, branchRef)
		})
	case actionDiscard:
		return m.startLoading(), m.mutationCmd("discarded", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Discard(ctx, firstNonEmpty(first(changeIDs), selectedContent.ID))
		})
	case actionAmend:
		return m.startLoading(), m.mutationCmd("amended", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Amend(ctx, firstNonEmpty(first(changeIDs), selectedContent.ID), input)
		})
	case actionAbsorb:
		return m.startLoading(), m.mutationCmd("absorbed", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Absorb(ctx)
		})
	case actionSquash:
		return m.startLoading(), m.mutationCmd("squashed", func() (*gitbutler.WorkspaceStatus, error) {
			targets := strings.Fields(input)
			if len(targets) == 0 {
				targets = commitIDs
			}
			return m.client.Squash(ctx, targets...)
		})
	case actionUncommit:
		target := firstNonEmpty(selectedContent.ID, selectedLane.ID, selectedLane.Name)
		return m.startLoading(), m.mutationCmd("uncommitted", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Uncommit(ctx, target, false)
		})
	case actionMove:
		source := firstNonEmpty(selectedContent.ID, selectedLane.ID, selectedLane.Name)
		return m.startLoading(), m.mutationCmd("moved", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Move(ctx, source, input)
		})
	case actionRub:
		source := firstNonEmpty(selectedContent.ID, selectedLane.ID, selectedLane.Name)
		return m.startLoading(), m.mutationCmd("rubbed", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Rub(ctx, source, input)
		})
	case actionMerge:
		return m.startLoading(), m.mutationCmd("merged", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Merge(ctx, branchRef)
		})
	case actionPullCheck:
		summary := m.upstreamUpdateSummary()
		return m, m.textCmd("message", func() (string, error) {
			out, err := m.client.PullCheck(ctx)
			if err != nil {
				return "", err
			}
			return formatPullCheckOutput(summary, out), nil
		})
	case actionPull:
		return m.startLoading(), m.mutationCmd("updated from upstream", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Pull(ctx)
		})
	case actionPush:
		return m.startLoading(), m.mutationCmd("pushed", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Push(ctx, branchRef, false)
		})
	case actionPushDryRun:
		return m, m.textCmd("message", func() (string, error) { return m.client.PushDryRun(ctx, branchRef) })
	case actionForcePush:
		return m.startLoading(), m.mutationCmd("force pushed", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Push(ctx, branchRef, true)
		})
	case actionNewPR:
		return m, m.textCmd("message", func() (string, error) { return m.client.NewPR(ctx, branchRef, false) })
	case actionNewDraftPR:
		return m, m.textCmd("message", func() (string, error) { return m.client.NewPR(ctx, branchRef, true) })
	case actionPRDraft:
		return m.startLoading(), m.mutationCmd("PR set draft", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.SetPRDraft(ctx, branchRef)
		})
	case actionPRReady:
		return m.startLoading(), m.mutationCmd("PR set ready", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.SetPRReady(ctx, branchRef)
		})
	case actionResolveStatus:
		return m, m.textCmd("message", func() (string, error) { return m.client.ResolveStatus(ctx) })
	case actionResolveFinish:
		return m.startLoading(), m.mutationCmd("resolution finished", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.ResolveFinish(ctx)
		})
	case actionResolveCancel:
		return m.startLoading(), m.mutationCmd("resolution cancelled", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.ResolveCancel(ctx)
		})
	case actionUndo:
		return m.startLoading(), m.mutationCmd("undone", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Undo(ctx)
		})
	case actionSnapshot:
		return m, m.textCmd("message", func() (string, error) { return m.client.OplogSnapshot(ctx, input) })
	case actionRestore:
		return m.startLoading(), m.mutationCmd("snapshot restored", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.OplogRestore(ctx, input)
		})
	case actionCleanDryRun:
		return m, m.textCmd("message", func() (string, error) { return m.client.CleanDryRun(ctx) })
	case actionClean:
		return m.startLoading(), m.mutationCmd("cleaned", func() (*gitbutler.WorkspaceStatus, error) {
			return m.client.Clean(ctx)
		})
	default:
		m.toast = fmt.Sprintf("unknown action: %s", action.ID)
		return m, nil
	}
}

func (m Model) openBranchPicker() (tea.Model, tea.Cmd) {
	if len(m.data.BranchOptions) == 0 {
		m.toast = "no inactive branches to add"
		return m, nil
	}
	if m.branchCursor >= len(m.data.BranchOptions) {
		m.branchCursor = len(m.data.BranchOptions) - 1
	}
	if m.branchCursor < 0 {
		m.branchCursor = 0
	}
	m.mode = modeBranchPicker
	return m, nil
}

func (m Model) move(delta int) (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelLanes:
		m.laneCursor += delta
		m.contentCursor = 0
		m.selected = map[string]bool{}
		m.rangeAnchor = -1
	case panelContents:
		m.contentCursor += delta
	case panelPreview:
		m.previewScroll += delta
		if m.previewScroll < 0 {
			m.previewScroll = 0
		}
		return m, nil
	}
	m.clampCursors()
	return m.withPreview()
}

func (m Model) moveLane(delta int) (tea.Model, tea.Cmd) {
	m.laneCursor += delta
	m.contentCursor = 0
	m.focus = panelContents
	m.selected = map[string]bool{}
	m.rangeAnchor = -1
	m.clampCursors()
	return m.withPreview()
}

func (m Model) moveKanbanItem(delta int) (tea.Model, tea.Cmd) {
	m.contentCursor += delta
	m.focus = panelContents
	m.clampCursors()
	return m.withPreview()
}

func (m Model) toggleSelection() (tea.Model, tea.Cmd) {
	item, ok := m.selectedContent()
	if !ok || item.ID == "" {
		return m, nil
	}
	if m.selected == nil {
		m.selected = map[string]bool{}
	}
	if m.selected[item.ID] {
		delete(m.selected, item.ID)
	} else {
		m.selected[item.ID] = true
	}
	m.rangeAnchor = m.contentCursor
	return m, nil
}

func (m Model) rangeSelection() (tea.Model, tea.Cmd) {
	contents := m.contents()
	if len(contents) == 0 {
		return m, nil
	}
	if m.selected == nil {
		m.selected = map[string]bool{}
	}
	if m.rangeAnchor < 0 || m.rangeAnchor >= len(contents) {
		m.rangeAnchor = m.contentCursor
	}
	start, end := m.rangeAnchor, m.contentCursor
	if start > end {
		start, end = end, start
	}
	for _, item := range contents[start : end+1] {
		if item.ID != "" {
			m.selected[item.ID] = true
		}
	}
	return m, nil
}

func (m Model) selectedContentIDs(kind contentKind) []string {
	if len(m.selected) == 0 {
		return nil
	}
	ids := []string{}
	for _, item := range m.contents() {
		if item.Kind == kind && m.selected[item.ID] {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (m Model) refreshCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := client.Status(ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		branches, err := client.BranchList(ctx)
		return loadedMsg{status: status, branches: branches, err: err}
	}
}

func (m Model) installGitButlerCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", "curl -fsSL https://gitbutler.com/install.sh | sh")
		out, err := cmd.CombinedOutput()
		return installGitButlerMsg{body: string(out), err: err}
	}
}

func (m Model) branchListCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		branches, err := client.BranchList(ctx)
		return loadedMsg{status: m.data.Status, branches: branches, err: err}
	}
}

func (m Model) upstreamRefreshCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := client.Status(ctx)
		if err != nil {
			return upstreamRefreshMsg{err: err}
		}
		branches, err := client.BranchList(ctx)
		return upstreamRefreshMsg{status: status, branches: branches, err: err}
	}
}

func (m Model) autoRefreshCmd(includeBranches bool) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), autoRefreshTimeout)
		defer cancel()

		status, err := client.Status(ctx)
		if err != nil {
			return autoRefreshMsg{err: err}
		}

		var branches *gitbutler.BranchList
		if includeBranches {
			branches, err = client.BranchList(ctx)
		}
		return autoRefreshMsg{status: status, branches: branches, err: err}
	}
}

func (m Model) mutationCmd(label string, fn func() (*gitbutler.WorkspaceStatus, error)) tea.Cmd {
	return func() tea.Msg {
		status, err := fn()
		return mutationMsg{status: status, err: err, label: label}
	}
}

func (m Model) textCmd(target string, fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		body, err := fn()
		return textMsg{target: target, body: body, err: err}
	}
}

func (m Model) withPreview() (Model, tea.Cmd) {
	target := m.previewSelectionTarget()
	if target == "" {
		m.previewTarget = ""
		m.preview = ""
		m.previewErr = nil
		m.previewScroll = 0
		return m, nil
	}
	if target == m.previewTarget {
		return m, nil
	}
	m.previewTarget = target
	m.preview = ""
	m.previewErr = nil
	m.previewScroll = 0
	return m, m.previewCmdFor(target)
}

func (m Model) previewCmdFor(target string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		var body string
		var err error
		if strings.HasPrefix(target, "show:") {
			body, err = client.Show(ctx, strings.TrimPrefix(target, "show:"))
		} else {
			body, err = client.Diff(ctx, target)
		}
		return textMsg{target: target, body: body, err: err}
	}
}

func (m Model) previewSelectionTarget() string {
	lane, ok := m.selectedLane()
	if !ok {
		return ""
	}
	if item, ok := m.selectedContent(); ok && item.ID != "" {
		return item.ID
	}
	if lane.Kind == laneAppliedBranch {
		return "show:" + lane.Name
	}
	return ""
}

func (m Model) upstreamUpdateConfirmText() string {
	return "Fetch the target branch and rebase every applied branch on top of it.\n\n" +
		m.upstreamUpdateSummary() +
		"\n\nRun `u` first for a non-mutating conflict check, or confirm now to run `but pull`."
}

func (m Model) upstreamUpdateSummary() string {
	if m.data.Status == nil {
		return "Current status unavailable."
	}
	branches, conflicts := m.upstreamBranchSummary()

	lines := []string{}
	if m.data.Status.UpstreamState.Behind > 0 {
		lines = append(lines, fmt.Sprintf("Incoming target commits: %d", m.data.Status.UpstreamState.Behind))
	} else {
		lines = append(lines, "Incoming target commits: none detected")
	}
	lines = append(lines, "Applied branches to update: "+summaryList(branches, "none"))
	lines = append(lines, "Known conflicts: "+summaryList(conflicts, "none"))
	return strings.Join(lines, "\n")
}

func (m Model) upstreamBranchSummary() ([]string, []string) {
	branches := []string{}
	conflicts := []string{}
	for _, lane := range m.data.Lanes {
		if lane.Kind != laneAppliedBranch {
			continue
		}
		branches = append(branches, lane.Name)
		if lane.MergeClean != nil && !*lane.MergeClean {
			conflicts = append(conflicts, lane.Name)
		}
	}
	return branches, conflicts
}

func formatPullCheckOutput(summary, out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return summary
	}
	return summary + "\n\n`but pull --check`\n" + out
}

func summaryList(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	const maxSummaryItems = 4
	if len(values) > maxSummaryItems {
		return strings.Join(values[:maxSummaryItems], ", ") + fmt.Sprintf(", +%d more", len(values)-maxSummaryItems)
	}
	return strings.Join(values, ", ")
}

func (m Model) mutationToast(label string, status *gitbutler.WorkspaceStatus) (string, toastKind) {
	if label == "updated from upstream" && workspaceHasConflicts(status) {
		return "updated from upstream; conflicts detected", toastError
	}
	return label, toastSuccess
}

func workspaceHasConflicts(status *gitbutler.WorkspaceStatus) bool {
	if status == nil {
		return false
	}
	for _, stack := range status.Stacks {
		for _, branch := range stack.Branches {
			if branch.MergeStatus.String() == "conflicted" {
				return true
			}
			for _, commit := range branch.Commits {
				if commit.Conflicted != nil && *commit.Conflicted {
					return true
				}
			}
		}
	}
	return false
}

func (m Model) requestAutoRefresh(includeBranches bool) (Model, tea.Cmd) {
	if !m.canAutoRefresh() {
		return m, nil
	}
	if m.autoRefreshInFlight {
		m.autoRefreshPending = true
		m.autoRefreshPendingBranches = m.autoRefreshPendingBranches || includeBranches
		return m, nil
	}
	m.autoRefreshInFlight = true
	return m, m.autoRefreshCmd(includeBranches)
}

func (m Model) canAutoRefresh() bool {
	return m.client != nil && !m.loading && m.mode == modeNormal && m.data.Status != nil
}

func (m Model) replaceData(status *gitbutler.WorkspaceStatus, branches *gitbutler.BranchList) Model {
	laneKey := ""
	if lane, ok := m.selectedLane(); ok {
		laneKey = lane.Key
	}
	contentID := ""
	if item, ok := m.selectedContent(); ok {
		contentID = item.ID
	}
	m.data = buildWorkspaceData(status, branches)
	m.restoreCursors(laneKey, contentID)
	return m
}

func (m *Model) restoreCursors(laneKey, contentID string) {
	if laneKey != "" {
		for idx, lane := range m.filteredLanes() {
			if lane.Key == laneKey {
				m.laneCursor = idx
				break
			}
		}
	}
	m.clampCursors()
	if contentID != "" {
		for idx, item := range m.contents() {
			if item.ID == contentID {
				m.contentCursor = idx
				break
			}
		}
	}
	m.clampCursors()
}

func (m Model) startLoading() Model {
	m.loading = true
	m.err = nil
	// Keep an existing toast visible — don't blank user feedback when a follow-up
	// refresh kicks off automatically.
	return m
}

func (m *Model) setToast(text string, kind toastKind) {
	if text == "" {
		m.toast = ""
		return
	}
	m.toast = text
	m.toastKind = kind
	m.toastExpires = time.Now().Add(4 * time.Second)
}

func (m *Model) clampCursors() {
	lanes := m.filteredLanes()
	if len(lanes) == 0 {
		m.laneCursor = 0
		m.contentCursor = 0
		return
	}
	if m.laneCursor < 0 {
		m.laneCursor = 0
	}
	if m.laneCursor >= len(lanes) {
		m.laneCursor = len(lanes) - 1
	}
	contents := m.contents()
	if m.contentCursor < 0 {
		m.contentCursor = 0
	}
	if len(contents) == 0 {
		m.contentCursor = 0
		return
	}
	if m.contentCursor >= len(contents) {
		m.contentCursor = len(contents) - 1
	}
}

func (m Model) filteredLanes() []lane {
	if m.filter == "" {
		return m.data.Lanes
	}
	query := strings.ToLower(m.filter)
	out := make([]lane, 0, len(m.data.Lanes))
	for i, lane := range m.data.Lanes {
		// Always keep the zz workspace lane visible — it is the source of all
		// unassigned work and should never be filtered out.
		if i == 0 && lane.Kind == laneUnassigned {
			out = append(out, lane)
			continue
		}
		if strings.Contains(strings.ToLower(lane.Name), query) || strings.Contains(strings.ToLower(lane.ID), query) {
			out = append(out, lane)
		}
	}
	return out
}

func (m Model) contents() []contentItem {
	lanes := m.filteredLanes()
	if len(lanes) == 0 {
		return nil
	}
	return m.contentForLane(lanes[m.laneCursor])
}

func (m Model) contentForLane(selected lane) []contentItem {
	for idx, lane := range m.data.Lanes {
		if lane.Key == selected.Key {
			return m.data.ContentFor(idx)
		}
	}
	return nil
}

func (m Model) selectedLane() (lane, bool) {
	lanes := m.filteredLanes()
	if len(lanes) == 0 || m.laneCursor < 0 || m.laneCursor >= len(lanes) {
		return lane{}, false
	}
	return lanes[m.laneCursor], true
}

func (m Model) selectedContent() (contentItem, bool) {
	contents := m.contents()
	if len(contents) == 0 || m.contentCursor < 0 || m.contentCursor >= len(contents) {
		return contentItem{}, false
	}
	return contents[m.contentCursor], true
}
