package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Prompts must accept spaces (Bubble Tea delivers them as KeySpace, not
// KeyRunes) and delete whole runes, not bytes.
func TestPromptInputSpacesAndRuneBackspace(t *testing.T) {
	m := newModel(nil)
	m.mode = modeInput
	m.prompt = promptState{Action: action{ID: actionCommit, InputLabel: "message"}}

	feed := func(msg tea.KeyMsg) {
		model, _ := m.handleInputKey(msg)
		m = model.(Model)
	}
	feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix")})
	feed(tea.KeyMsg{Type: tea.KeySpace})
	feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bug")})
	if m.prompt.Value != "fix bug" {
		t.Fatalf("prompt value = %q, want %q", m.prompt.Value, "fix bug")
	}

	feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" é")})
	feed(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.prompt.Value != "fix bug " {
		t.Fatalf("backspace over é left %q, want %q", m.prompt.Value, "fix bug ")
	}

	feed(tea.KeyMsg{Type: tea.KeyCtrlW})
	if m.prompt.Value != "fix " {
		t.Fatalf("ctrl+w left %q, want %q", m.prompt.Value, "fix ")
	}
}

// Navigation previews debounce: the subprocess only spawns once the cursor
// rests, and a stale debounce tick (cursor moved on) spawns nothing.
func TestPreviewNavDebounceAndStaleSeq(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 30)
	m.focus = panelContents
	m.contentCursor = 0

	next, cmd := m.withPreviewNav()
	m = next
	if m.previewTarget == "" {
		t.Fatalf("navigation should set a preview target")
	}
	if cmd == nil {
		t.Fatalf("cache miss should schedule a debounce tick")
	}
	msg, ok := cmd().(previewDebounceMsg)
	if !ok {
		t.Fatalf("debounce cmd should yield previewDebounceMsg, got %T", cmd())
	}
	if msg.seq != m.previewSeq || msg.target != m.previewTarget {
		t.Fatalf("debounce msg %v does not match model seq %d target %q", msg, m.previewSeq, m.previewTarget)
	}

	// Current seq + target → the diff subprocess command fires.
	model, fetch := m.update(msg)
	m = model.(Model)
	if fetch == nil {
		t.Fatalf("matching debounce tick should return the preview fetch cmd")
	}

	// Stale seq (cursor moved on) → nothing spawns.
	model, fetch = m.update(previewDebounceMsg{seq: msg.seq - 1, target: msg.target})
	if fetch != nil {
		t.Fatalf("stale debounce tick must not spawn a subprocess")
	}
	_ = model
}

// A cached preview body renders synchronously with no subprocess.
func TestPreviewNavCacheHit(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 30)
	m.focus = panelContents
	m.contentCursor = 0
	target := m.previewSelectionTarget()
	if target == "" {
		t.Fatalf("fixture should yield a preview target")
	}
	m.previewCache = map[string]string{target: "cached diff body"}

	next, cmd := m.withPreviewNav()
	if cmd != nil {
		t.Fatalf("cache hit should not schedule anything")
	}
	if next.preview != "cached diff body" {
		t.Fatalf("cache hit should render the cached body, got %q", next.preview)
	}
}

// Successful textMsg bodies populate the cache; data refresh clears it.
func TestPreviewCacheFillAndInvalidation(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 30)
	m.previewTarget = "x1"
	model, _ := m.update(textMsg{target: "x1", body: "diff body"})
	m = model.(Model)
	if m.previewCache["x1"] != "diff body" {
		t.Fatalf("textMsg should cache the body, got %v", m.previewCache)
	}
	m = m.replaceData(m.data.Status, m.data.Branches)
	if m.previewCache != nil {
		t.Fatalf("replaceData should clear the preview cache")
	}
	if m.previewTarget != "" || m.preview != "" {
		t.Fatalf("replaceData should force the selected preview to reload, got target %q body %q", m.previewTarget, m.preview)
	}
}

func TestStalePreviewResponseIgnoredAfterDataChange(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 30)
	m.previewTarget = "x1"
	m.previewSeq = 7
	stale := textMsg{target: "x1", body: "stale diff", seq: m.previewSeq}

	m = m.replaceData(m.data.Status, m.data.Branches)
	model, _ := m.update(stale)
	m = model.(Model)
	if m.preview != "" || m.previewCache != nil {
		t.Fatalf("stale response repopulated invalidated preview: body %q cache %v", m.preview, m.previewCache)
	}
}

// A pending CI check must keep the spinner alive on the slow tick, not the
// 90ms loop — and never let the tick die entirely.
func TestCIPendingKeepsSlowTickAlive(t *testing.T) {
	m := newModel(nil)
	m.loading = false
	m.ticking = false
	m.data = buildWorkspaceData(nil, nil)
	m.data.Lanes = []lane{{Key: "b1", CIPresent: true, CIConclusion: "pending"}}

	if m.needsFastTick() {
		t.Fatalf("CI-pending alone must not demand the fast tick")
	}
	model, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd == nil || !model.(Model).ticking {
		t.Fatalf("an incoming pending-CI state must restart the stopped tick: cmd nil=%v ticking=%v slow=%v", cmd == nil, model.(Model).ticking, model.(Model).needsSlowTick())
	}
	m = model.(Model)
	model, cmd = m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatalf("tick must reschedule while CI is pending")
	}
	if !model.(Model).ticking {
		t.Fatalf("ticking should stay true while CI is pending")
	}
}

// Enter-to-inspect while the diff is loading opens the viewer as soon as the
// diff arrives instead of bouncing with a "try again" toast.
func TestEnterToInspectWaitsForPendingDiff(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 30)
	m.previewTarget = "x1"
	m.preview = ""

	model, _ := m.enterDiffMode()
	m = model.(Model)
	if !m.diffPending {
		t.Fatalf("enter on a loading diff should set diffPending")
	}
	if m.mode == modeDiff {
		t.Fatalf("diff view must not open before the diff arrives")
	}

	model, _ = m.update(textMsg{target: "x1", body: "+added line"})
	m = model.(Model)
	if m.mode != modeDiff {
		t.Fatalf("diff view should open when the pending diff lands, mode = %v", m.mode)
	}
	if m.diffPending {
		t.Fatalf("diffPending should clear once honored")
	}
}

func TestPendingDiffDoesNotChangeAnotherMode(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 30)
	m.previewTarget = "x1"
	m.diffPending = true
	m.mode = modePalette

	model, _ := m.update(textMsg{target: "x1", body: "+added line"})
	m = model.(Model)
	if m.mode != modePalette || m.diffPending {
		t.Fatalf("diff response changed another mode: mode %v pending %v", m.mode, m.diffPending)
	}
}

// The palette windows around the cursor on short terminals instead of letting
// the overlay hard-truncate the bottom of the list — and mouse hit-testing
// stays in sync with the window offset.
func TestPaletteWindowsAroundCursor(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 18)
	m.mode = modePalette
	m.palette = m.availableActions()
	if len(m.palette) < 10 {
		t.Fatalf("fixture palette too small (%d) for a windowing test", len(m.palette))
	}
	m.paletteCursor = len(m.palette) - 1

	view := m.renderPalette()
	last := m.palette[len(m.palette)-1]
	if !strings.Contains(view, last.Label) {
		t.Fatalf("windowed palette should keep the cursor row visible:\n%s", view)
	}
	if strings.Contains(view, m.palette[0].Label) {
		t.Fatalf("rows far above the cursor window should be scrolled out:\n%s", view)
	}

	// Mouse mapping: the first visible row maps back to the window start.
	height := paletteWindowHeight(m.height)
	visible := min(len(m.palette), height)
	modalH := 8 + visible
	if modalH > m.height {
		modalH = m.height
	}
	firstItem := max(0, (m.height-modalH)/2) + 4
	idx, ok := paletteRowAt(m.height, len(m.palette), m.paletteCursor, firstItem)
	if !ok {
		t.Fatalf("first visible row should hit-test inside the palette")
	}
	if want := windowStart(len(m.palette), m.paletteCursor, height); idx != want {
		t.Fatalf("first visible row maps to %d, want window start %d", idx, want)
	}
}

// Preview j/k scrolling clamps at the end of the content, so scroll-up
// responds immediately instead of unwinding phantom over-scroll.
func TestPreviewScrollClampsViaMove(t *testing.T) {
	m := modelWithActiveBranches(t, 2, 100, 40)
	m.focus = panelPreview
	m.setPreview("x1", "one\ntwo\nthree", nil)

	for i := 0; i < 50; i++ {
		model, _ := m.move(1)
		m = model.(Model)
	}
	model, _ := m.move(-1)
	m = model.(Model)
	if m.previewScroll > 10 {
		t.Fatalf("previewScroll should be clamped near the content size, got %d", m.previewScroll)
	}
}

func naiveKanbanColumnRows(m Model, lane lane, index, innerW, availRows int) []string {
	rows := []string{laneMetaLine(lane, innerW)}
	contents := m.contentForLane(lane)
	if len(contents) == 0 {
		hint := "nothing here yet"
		if lane.Kind == laneAppliedBranch {
			hint = "nothing assigned — drop files or press c to commit"
		}
		rows = append(rows, styleDim.Render(hint))
	} else {
		fileCount, commitCount := countContent(contents)
		for itemIdx, item := range contents {
			rows = append(rows, m.kanbanItemLine(item, index, itemIdx, innerW))
			if isFileCommitBoundary(contents, itemIdx) {
				rows = append(rows, sectionDivider(innerW, fileCount, commitCount))
			}
		}
	}
	return windowRows(rows, m.kanbanColumnCursor(index), availRows)
}

func naiveContentLines(m Model, width, height int) []string {
	contents := m.contents()
	if len(contents) == 0 {
		return []string{styleDim.Render("no content")}
	}
	_, commitCount := countContent(contents)
	rows := make([]string, 0, len(contents)+1)
	rowCursor := m.contentCursor
	for idx, item := range contents {
		rows = append(rows, m.formatContentLine(item, idx, width))
		if isFileCommitBoundary(contents, idx) {
			rows = append(rows, sectionDivider(width, 0, commitCount))
			if m.contentCursor > idx {
				rowCursor++
			}
		}
	}
	return windowRows(rows, rowCursor, height)
}

func differentialContentCases(t *testing.T) []struct {
	name  string
	items []contentItem
} {
	t.Helper()
	fixture := buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t)).ContentFor(1)
	var file, commit contentItem
	for _, item := range fixture {
		if item.Kind == contentChange && file.ID == "" {
			file = item
		}
		if item.Kind != contentChange && commit.ID == "" {
			commit = item
		}
	}
	if file.ID == "" || commit.ID == "" {
		t.Fatalf("fixture must contain both a file and a commit: %#v", fixture)
	}
	repeat := func(prototype contentItem, n int) []contentItem {
		items := make([]contentItem, n)
		for i := range items {
			items[i] = prototype
			items[i].Key = fmt.Sprintf("%s-%02d", prototype.Key, i)
			items[i].ID = fmt.Sprintf("%s-%02d", prototype.ID, i)
			items[i].Label = fmt.Sprintf("%s %02d", prototype.Label, i)
		}
		return items
	}
	files := repeat(file, 50)
	commits := repeat(commit, 50)
	mixed := append(append([]contentItem{}, files[:25]...), commits[:25]...)
	return []struct {
		name  string
		items []contentItem
	}{
		{name: "empty"},
		{name: "single", items: fixture[:1]},
		{name: "fixture-files", items: []contentItem{file}},
		{name: "fixture-commits", items: []contentItem{commit}},
		{name: "fixture-files-and-commits", items: fixture},
		{name: "50-files", items: files},
		{name: "50-commits", items: commits},
		{name: "50-files-and-commits", items: mixed},
	}
}

func contentRowCount(items []contentItem, meta bool) int {
	if len(items) == 0 {
		if meta {
			return 2
		}
		return 1
	}
	n := len(items)
	if _, ok := lastFileBoundary(items); ok {
		n++
	}
	if meta {
		n++
	}
	return n
}

func TestKanbanColumnRowsMatchesNaiveReference(t *testing.T) {
	for _, tc := range differentialContentCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(nil)
			m.loading = false
			m.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
			m.data.Contents[1] = tc.items
			lane := m.data.Lanes[1]
			for _, laneCursor := range []int{1, 0} {
				m.laneCursor = laneCursor
				for cursor := 0; cursor <= len(tc.items); cursor++ {
					m.contentCursor = cursor
					for availRows := 1; availRows <= contentRowCount(tc.items, true)+2; availRows++ {
						got := m.kanbanColumnRows(lane, 1, 48, availRows)
						want := naiveKanbanColumnRows(m, lane, 1, 48, availRows)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("laneCursor=%d cursor=%d rows=%d\ngot:\n%s\nwant:\n%s", laneCursor, cursor, availRows, strings.Join(got, "\n"), strings.Join(want, "\n"))
						}
					}
				}
			}
		})
	}
}

func TestContentLinesMatchesNaiveReference(t *testing.T) {
	for _, tc := range differentialContentCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(nil)
			m.loading = false
			m.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
			m.data.Contents[1] = tc.items
			m.laneCursor = 1
			for _, focus := range []panel{panelContents, panelLanes} {
				m.focus = focus
				for cursor := 0; cursor <= len(tc.items); cursor++ {
					m.contentCursor = cursor
					for availRows := 1; availRows <= contentRowCount(tc.items, false)+2; availRows++ {
						got := m.contentLines(48, availRows)
						want := naiveContentLines(m, 48, availRows)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("focus=%d cursor=%d rows=%d\ngot:\n%s\nwant:\n%s", focus, cursor, availRows, strings.Join(got, "\n"), strings.Join(want, "\n"))
						}
					}
				}
			}
		})
	}
}

func TestPaletteAndTargetPickerHitTestingMatchesRender(t *testing.T) {
	for _, height := range []int{12, 14, 20, 24} {
		for _, targetPicker := range []bool{false, true} {
			m := newModel(nil)
			m.loading = false
			m.width = 100
			m.height = height
			labels := make([]string, 20)
			for i := range labels {
				labels[i] = fmt.Sprintf("choice-%02d", i)
				if targetPicker {
					m.targetPicker.Items = append(m.targetPicker.Items, pickerItem{Label: labels[i], Value: labels[i]})
				} else {
					m.palette = append(m.palette, action{ID: actionRefresh, Key: fmt.Sprintf("k%d", i), Label: labels[i]})
				}
			}
			for cursor := range labels {
				if targetPicker {
					m.mode = modeTargetPicker
					m.targetPicker.Cursor = cursor
				} else {
					m.mode = modePalette
					m.paletteCursor = cursor
				}
				cursorSeen := false
				for y, line := range strings.Split(m.View(), "\n") {
					idx, ok := paletteRowAt(height, len(labels), cursor, y)
					if ok && !strings.Contains(line, labels[idx]) {
						t.Fatalf("height=%d target=%v cursor=%d y=%d maps to %d, row=%q", height, targetPicker, cursor, y, idx, line)
					}
					cursorSeen = cursorSeen || strings.Contains(line, labels[cursor])
				}
				if !cursorSeen {
					t.Fatalf("height=%d target=%v cursor=%d was clipped out of the rendered window", height, targetPicker, cursor)
				}
			}
		}
	}
}

func TestHelpFitsViewport(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {60, 20}} {
		m := modelWithActiveBranches(t, 2, size.width, size.height)
		m.mode = modeHelp
		help := m.renderHelp()
		if rows := len(splitLines(help)); rows > size.height {
			t.Fatalf("help at %dx%d is %d rows tall", size.width, size.height, rows)
		}
		view := m.View()
		if !strings.Contains(view, "closes this help") {
			t.Fatalf("help footer was truncated at %dx%d:\n%s", size.width, size.height, view)
		}
		if (len(m.availableActions())+1)/2 > max(1, size.height-18) && !strings.Contains(view, "more") {
			t.Fatalf("help overflow line was truncated at %dx%d:\n%s", size.width, size.height, view)
		}
	}
}
