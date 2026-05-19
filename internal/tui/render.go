package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/ansi"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

var (
	border = lipgloss.RoundedBorder()

	// Git-inspired palette: green = added/synced, red = removed/force/conflict,
	// yellow = modified/ahead/warn, cyan = info/accent, purple = merged/integrated.
	colAccent   = lipgloss.Color("81")  // bright cyan
	colAccent2  = lipgloss.Color("117") // soft cyan
	colMuted    = lipgloss.Color("245")
	colFaint    = lipgloss.Color("243") // border of non-focused boxes
	colDeep     = lipgloss.Color("238") // very dim separators
	colOk       = lipgloss.Color("114") // git green (added)
	colWarn     = lipgloss.Color("222") // git yellow (modified)
	colErr      = lipgloss.Color("203") // git red (removed / force / conflict)
	colPurple   = lipgloss.Color("141") // merged / integrated
	colMagenta  = lipgloss.Color("177") // untracked / renamed
	colSelectBg = lipgloss.Color("237")
	colFg       = lipgloss.Color("252")

	styleDim    = lipgloss.NewStyle().Foreground(colMuted)
	styleFaint  = lipgloss.NewStyle().Foreground(colFaint)
	styleAccent = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleWarn   = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	styleErr    = lipgloss.NewStyle().Foreground(colErr).Bold(true)
	styleOk     = lipgloss.NewStyle().Foreground(colOk).Bold(true)

	styleTitle     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleTitleBlur = lipgloss.NewStyle().Foreground(colMuted).Bold(true)

	styleFocus   = lipgloss.NewStyle().Border(border).BorderForeground(colAccent)
	styleBlur    = lipgloss.NewStyle().Border(border).BorderForeground(colFaint)
	styleOverlay = lipgloss.NewStyle().Border(border).BorderForeground(colAccent).Padding(1, 2)

	styleSelectedRow = lipgloss.NewStyle().Background(colSelectBg).Foreground(colFg).Bold(true)
	styleMarked      = lipgloss.NewStyle().Foreground(colWarn).Bold(true)

	styleBadgeZZ       = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	styleBadgeOn       = lipgloss.NewStyle().Foreground(colOk).Bold(true)
	styleBadgeConflict = lipgloss.NewStyle().Foreground(colErr).Bold(true)
	styleMerged        = lipgloss.NewStyle().Foreground(colPurple).Bold(true)

	styleKindFile     = lipgloss.NewStyle().Foreground(colAccent2)
	styleKindCommit   = lipgloss.NewStyle().Foreground(colOk)
	styleKindUpstream = lipgloss.NewStyle().Foreground(colWarn)
	styleKindInfo     = styleDim

	styleHotKey   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleHotLabel = lipgloss.NewStyle().Foreground(colMuted)
	styleHotSep   = lipgloss.NewStyle().Foreground(colDeep)

	styleNodeCommit   = lipgloss.NewStyle().Foreground(colOk).Bold(true)
	styleNodeUpstream = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	styleNodeFile     = lipgloss.NewStyle().Foreground(colAccent2).Bold(true)
	styleIDDim        = lipgloss.NewStyle().Foreground(colMuted)
	stylePathDim      = lipgloss.NewStyle().Foreground(colMuted)
	styleFileName     = lipgloss.NewStyle().Foreground(colFg).Bold(true)

	// Diff styling.
	styleDiffAdd    = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))                                  // green
	styleDiffRem    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))                                  // red
	styleDiffAddBg  = lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Background(lipgloss.Color("22")) // bright green, dark green bg
	styleDiffRemBg  = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Background(lipgloss.Color("52")) // bright red, dark red bg
	styleDiffCtx    = lipgloss.NewStyle().Foreground(colFg)
	styleDiffGutter = lipgloss.NewStyle().Foreground(colFaint)
	styleDiffHeader = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
)

const (
	kanbanMinWidth = 70 // narrower than before — single-column kanban still works
	sepDot         = "·"
	sepBullet      = "•"
	glyphCommit    = "●"
	glyphUpstream  = "○"
	glyphFile      = "◆"
	glyphAhead     = "↑"
	glyphBehind    = "↓"
	glyphConflict  = "⚠"
	glyphCheck     = "✓"
	glyphCross     = "✗"
	glyphMerged    = "⊕"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m Model) View() string {
	if m.width == 0 {
		return "loading lazybut..."
	}

	top := m.renderTop()
	hotbar := m.renderHotbar()
	bodyHeight := max(1, m.height-lipgloss.Height(top)-lipgloss.Height(hotbar))
	body := m.renderBody(m.width, bodyHeight)
	view := lipgloss.JoinVertical(lipgloss.Left, top, body, hotbar)

	switch m.mode {
	case modeInput:
		return overlay(view, m.width, m.height, m.renderPrompt())
	case modeConfirm:
		return overlay(view, m.width, m.height, m.renderConfirm())
	case modePalette:
		return overlay(view, m.width, m.height, m.renderPalette())
	case modeBranchPicker:
		return overlay(view, m.width, m.height, m.renderBranchPicker())
	case modeTargetPicker:
		return overlay(view, m.width, m.height, m.renderTargetPicker())
	case modeHelp:
		return overlay(view, m.width, m.height, m.renderHelp())
	default:
		return view
	}
}

func (m Model) renderTop() string {
	segs := []string{styleAccent.Render("lazybut")}
	if m.client != nil && m.client.Dir != "" {
		segs = append(segs, styleDim.Render(shortRepoPath(m.client.Dir)))
	}
	if m.loading {
		segs = append(segs, styleWarn.Render(spinnerFrame(m.spinnerFrame)+" working"))
	}
	if m.data.Status != nil {
		segs = append(segs, chip("stacks", fmt.Sprintf("%d", len(m.data.Status.Stacks))))
		segs = append(segs, chip("zz", fmt.Sprintf("%d", len(m.data.Status.UnassignedChanges))))
		if m.data.Status.UpstreamState.Behind > 0 {
			segs = append(segs, styleWarn.Render(fmt.Sprintf("%s %d", glyphBehind, m.data.Status.UpstreamState.Behind)))
		}
		if fetched := fetchedAgo(m.data.Status.UpstreamState.LastFetched); fetched != "" {
			segs = append(segs, styleDim.Render(fetched))
		}
		if lane, ok := m.selectedLane(); ok {
			lanes := m.filteredLanes()
			segs = append(segs, styleDim.Render("on ")+styleAccent.Render(lane.Name)+styleDim.Render(fmt.Sprintf(" (%d/%d)", m.laneCursor+1, len(lanes))))
			// Show hidden-branch count when the kanban windowing hides some lanes.
			if m.usesKanbanLayout() && len(lanes) > 1 {
				cc, _ := m.kanbanGeometry(m.width)
				slots := cc - 1
				if slots > 0 && slots < len(lanes)-1 {
					hidden := (len(lanes) - 1) - slots
					segs = append(segs, styleHotKey.Render(fmt.Sprintf("+%d more", hidden)))
				}
			}
		}
	}
	if m.toast != "" {
		segs = append(segs, renderToast(m.toast, m.toastKind))
	}
	if m.err != nil && m.hasBootstrapIssue() {
		if !m.isBootstrapPrompt() {
			segs = append(segs, styleWarn.Render("GitButler setup needed"))
		}
	} else if m.err != nil {
		segs = append(segs, styleErr.Render(glyphCross+" "+m.err.Error()))
	}
	return fit(strings.Join(segs, " "+styleHotSep.Render(sepBullet)+" "), m.width)
}

// fetchedAgo returns a human-friendly "fetched 5m ago" string from
// UpstreamState.LastFetched (RFC3339-ish). Empty for nil/invalid input.
func fetchedAgo(raw *string) string {
	if raw == nil || *raw == "" {
		return ""
	}
	candidates := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
	}
	var t time.Time
	var err error
	for _, layout := range candidates {
		t, err = time.Parse(layout, *raw)
		if err == nil {
			break
		}
	}
	if err != nil {
		return ""
	}
	elapsed := time.Since(t)
	switch {
	case elapsed < 0:
		return "fetched now"
	case elapsed < time.Minute:
		return fmt.Sprintf("fetched %ds ago", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("fetched %dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("fetched %dh ago", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("fetched %dd ago", int(elapsed.Hours()/24))
	}
}

func spinnerFrame(frame int) string {
	if len(spinnerFrames) == 0 {
		return ""
	}
	return spinnerFrames[((frame%len(spinnerFrames))+len(spinnerFrames))%len(spinnerFrames)]
}

func renderToast(text string, kind toastKind) string {
	switch kind {
	case toastSuccess:
		return styleOk.Render(glyphCheck + " " + text)
	case toastError:
		return styleErr.Render(glyphCross + " " + text)
	default:
		return styleAccent.Render(text)
	}
}

func chip(label, value string) string {
	return styleDim.Render(label+":") + styleAccent.Render(value)
}

func (m Model) renderBody(width, height int) string {
	previewH := previewStripHeight(height)
	mainH := max(4, height-previewH)
	main := m.renderMain(width, mainH)
	if previewH == 0 {
		return main
	}
	preview := m.renderPreview(width, previewH)
	return lipgloss.JoinVertical(lipgloss.Left, main, preview)
}

func previewStripHeight(bodyHeight int) int {
	if bodyHeight < 14 {
		return 0
	}
	want := bodyHeight * 30 / 100
	if want < 6 {
		want = 6
	}
	if want > 14 {
		want = 14
	}
	return want
}

func (m Model) renderMain(width, height int) string {
	if m.usesKanbanLayout() && len(m.filteredLanes()) > 0 {
		return m.renderKanban(width, height)
	}
	if m.focus == panelContents {
		return m.renderContents(width, height)
	}
	return m.renderLanes(width, height)
}

func (m Model) usesKanbanLayout() bool {
	if m.width < kanbanMinWidth {
		return false
	}
	return len(m.filteredLanes()) > 0
}

func shortRepoPath(dir string) string {
	clean := filepath.Clean(dir)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, clean); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
	}
	return clean
}

func (m Model) renderKanban(width, height int) string {
	lanes := m.filteredLanes()
	if len(lanes) == 0 {
		hint := styleDim.Render("no active branches yet — press ") + styleHotKey.Render("+") + styleDim.Render(" or ") + styleHotKey.Render("B") + styleDim.Render(" to apply one")
		return box("kanban workspace", hint, width, height, true)
	}

	columnCount, columnWidth := m.kanbanGeometry(width)

	// Pin lane 0 (zz unassigned) in the leftmost slot — it's the workspace context
	// and must remain visible regardless of how far the user scrolls.
	columns := []string{m.renderKanbanColumn(lanes[0], 0, columnWidth, height)}

	if columnCount > 1 && len(lanes) > 1 {
		rest := lanes[1:]
		slots := columnCount - 1
		restStart := m.kanbanRestStart(len(rest), slots)
		end := restStart + slots
		if end > len(rest) {
			end = len(rest)
		}
		for i := restStart; i < end; i++ {
			columns = append(columns, m.renderKanbanColumn(rest[i], i+1, columnWidth, height))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, columns...)
}

// kanbanRestStart computes the window offset for the active branches (lanes[1:])
// so the focused lane stays centered. When the cursor is on zz, the window stays
// at the start.
func (m Model) kanbanRestStart(total, visible int) int {
	if visible >= total {
		return 0
	}
	cursorInRest := m.laneCursor - 1
	if cursorInRest < 0 {
		return 0
	}
	if cursorInRest >= total {
		cursorInRest = total - 1
	}
	start := cursorInRest - visible/2
	if start < 0 {
		return 0
	}
	if start+visible > total {
		return total - visible
	}
	return start
}

func (m Model) kanbanGeometry(width int) (int, int) {
	lanes := m.filteredLanes()
	if len(lanes) == 0 {
		return 1, width
	}
	// Aim for ~36 char columns; clamp to keep them readable but allow tighter widths.
	columnCount := min(len(lanes), max(1, width/32))
	columnWidth := width / columnCount
	if columnWidth > 48 {
		columnWidth = 48
		columnCount = min(len(lanes), max(1, width/columnWidth))
	}
	return max(1, columnCount), max(22, columnWidth)
}

func (m Model) renderKanbanColumn(lane lane, index, width, height int) string {
	innerW := contentWidth(width)
	rows := []string{
		laneMetaLine(lane, innerW),
		"",
	}
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
	title := m.laneKanbanTitle(lane, index)
	body := strings.Join(windowRows(rows, m.kanbanColumnCursor(index), max(1, contentHeight(height)-1)), "\n")
	return box(title, body, width, height, index == m.laneCursor)
}

func isFileCommitBoundary(items []contentItem, idx int) bool {
	return idx+1 < len(items) && items[idx].Kind == contentChange && items[idx+1].Kind != contentChange
}

func countContent(items []contentItem) (files, commits int) {
	for _, item := range items {
		switch item.Kind {
		case contentChange:
			files++
		case contentCommit, contentUpstreamCommit:
			commits++
		}
	}
	return
}

// sectionDivider returns a faint horizontal line with a centred label like
// "── 5 commits ──". Helps the eye separate files from commits inside a column.
func sectionDivider(width, files, commits int) string {
	label := plural(commits, "commit", "commits")
	_ = files
	core := " " + label + " "
	rem := width - lipgloss.Width(core)
	if rem < 4 {
		return styleFaint.Render(strings.Repeat("─", max(1, width)))
	}
	left := rem / 2
	right := rem - left
	return styleFaint.Render(strings.Repeat("─", left)) + styleDim.Render(core) + styleFaint.Render(strings.Repeat("─", right))
}

func (m Model) kanbanItemLine(item contentItem, laneIndex, itemIndex, width int) string {
	isCursor := laneIndex == m.laneCursor && itemIndex == m.contentCursor
	return renderItemRow(item, m.selected[item.ID], isCursor, width)
}

func renderItemRow(item contentItem, isSelected, isCursor bool, width int) string {
	mark := " "
	if isSelected {
		mark = "*"
	}
	glyph := itemGlyph(item.Kind)
	// Conflicted commits override the normal glyph with a warning sigil.
	if item.Conflicted {
		glyph = glyphConflict
	}
	id := displayItemID(item)
	if id == "" {
		id = "-"
	}
	label := item.Label
	if label == "" {
		label = item.Detail
	}
	prSuffix := ""
	if item.ReviewID != "" && item.Kind != contentChange {
		prSuffix = " " + cleanReviewID(item.ReviewID)
	}
	// Compact layout: [*]◆ <id> <label>[ #pr] — the cursor row's background
	// highlight indicates focus, so no leading arrow is needed.
	prefix := fmt.Sprintf("%s%s ", mark, glyph)
	idPart := id + " "
	remaining := max(1, width-lipgloss.Width(prefix)-lipgloss.Width(idPart)-lipgloss.Width(prSuffix))
	labelPart := fit(label, remaining)
	raw := prefix + idPart + labelPart + prSuffix
	raw = fit(raw, width)

	if isCursor {
		return styleSelectedRow.Render(padRight(raw, width))
	}
	out := raw
	if mark == "*" {
		out = strings.Replace(out, "*", styleMarked.Render("*"), 1)
	}
	if item.Conflicted {
		out = strings.Replace(out, glyphConflict, styleErr.Render(glyphConflict), 1)
	} else {
		out = styleGlyph(out, glyph, item.Kind, item.Detail)
	}
	out = colorizeID(out, id, item.Kind, item.Detail)
	if item.Kind == contentChange {
		out = colorizeFilePath(out, label, item.Detail)
	}
	if prSuffix != "" {
		clean := strings.TrimSpace(prSuffix)
		out = strings.Replace(out, clean, styleHotKey.Render(clean), 1)
	}
	return out
}

func itemGlyph(kind contentKind) string {
	switch kind {
	case contentChange:
		return glyphFile
	case contentCommit:
		return glyphCommit
	case contentUpstreamCommit:
		return glyphUpstream
	}
	return "·"
}

func styleGlyph(line, glyph string, kind contentKind, detail string) string {
	style := styleKindInfo
	switch kind {
	case contentChange:
		style = fileChangeStyle(detail)
	case contentCommit:
		style = styleNodeCommit
	case contentUpstreamCommit:
		style = styleNodeUpstream
	}
	idx := strings.Index(line, glyph)
	if idx < 0 {
		return line
	}
	return line[:idx] + style.Render(glyph) + line[idx+len(glyph):]
}

func displayItemID(item contentItem) string {
	if strings.HasPrefix(item.ID, "git:") {
		return "git"
	}
	return item.ID
}

// changeTypeColor maps a git change-type string (added/modified/deleted/…)
// to the canonical git-status color. Returns an empty Color sentinel for
// unknown types so callers can fall back to a neutral style.
func changeTypeColor(detail string) lipgloss.Color {
	switch strings.ToLower(detail) {
	case "added", "add", "a", "new":
		return colOk
	case "modified", "modify", "m":
		return colWarn
	case "deleted", "delete", "d", "removed":
		return colErr
	case "renamed", "rename", "r", "copied", "c":
		return colAccent2
	case "untracked", "u":
		return colMagenta
	case "conflicted", "conflict":
		return colErr
	}
	return ""
}

func fileChangeStyle(detail string) lipgloss.Style {
	if c := changeTypeColor(detail); c != "" {
		return lipgloss.NewStyle().Foreground(c).Bold(true)
	}
	return styleNodeFile
}

func fileNameStyle(detail string) lipgloss.Style {
	if c := changeTypeColor(detail); c != "" {
		return lipgloss.NewStyle().Foreground(c).Bold(true)
	}
	return styleFileName
}

func fileIDStyle(detail string) lipgloss.Style {
	if c := changeTypeColor(detail); c != "" {
		return lipgloss.NewStyle().Foreground(c)
	}
	return styleIDDim
}

func colorizeID(line, id string, kind contentKind, detail string) string {
	if id == "" || id == "-" {
		return line
	}
	style := styleIDDim
	if kind == contentChange {
		style = fileIDStyle(detail)
	}
	idx := strings.Index(line, id+" ")
	if idx < 0 {
		return line
	}
	return line[:idx] + style.Render(id) + line[idx+len(id):]
}

func colorizeFilePath(line, label, detail string) string {
	if label == "" {
		return line
	}
	nameStyle := fileNameStyle(detail)
	base := filepath.Base(label)
	if base == label || base == "" {
		idx := strings.Index(line, label)
		if idx < 0 {
			return line
		}
		return line[:idx] + nameStyle.Render(label) + line[idx+len(label):]
	}
	dir := strings.TrimSuffix(label, base)
	idx := strings.Index(line, label)
	if idx < 0 {
		// Label was truncated — tint just the basename fragment if visible.
		bi := strings.Index(line, base)
		if bi < 0 {
			return line
		}
		return line[:bi] + nameStyle.Render(base) + line[bi+len(base):]
	}
	return line[:idx] + stylePathDim.Render(dir) + nameStyle.Render(base) + line[idx+len(label):]
}

func (m Model) kanbanColumnCursor(index int) int {
	if index != m.laneCursor {
		return 0
	}
	base := m.contentCursor + 2
	// Off-by-one: the rendered column inserts a section divider after the last
	// file when commits follow. Bump base when the cursor sits past that divider.
	contents := m.contents()
	if boundary, hasBoundary := lastFileBoundary(contents); hasBoundary && m.contentCursor > boundary {
		base++
	}
	return base
}

// lastFileBoundary returns the index of the last file row in the content list
// when commits follow it (i.e. the row above the section divider). When there
// is no boundary (all files, all commits, or empty) it returns false.
func lastFileBoundary(items []contentItem) (int, bool) {
	fileEnd := -1
	for i, item := range items {
		if item.Kind == contentChange {
			fileEnd = i
		}
	}
	if fileEnd < 0 || fileEnd >= len(items)-1 {
		return 0, false
	}
	return fileEnd, true
}

func (m Model) kanbanStart(total, visible int) int {
	if visible >= total {
		return 0
	}
	start := m.laneCursor - visible/2
	if start < 0 {
		return 0
	}
	if start+visible > total {
		return total - visible
	}
	return start
}

func (m Model) renderLanes(width, height int) string {
	rows := m.laneLines(max(1, width-4), max(1, height-3))
	title := titleSpan("workspace", m.focus == panelLanes)
	if m.filter != "" {
		title += "  " + styleHotKey.Render("/"+m.filter)
	}
	return box(title, strings.Join(rows, "\n"), width, height, m.focus == panelLanes)
}

func (m Model) renderContents(width, height int) string {
	rows := m.contentLines(max(1, width-4), max(1, height-3))
	label := "contents"
	if lane, ok := m.selectedLane(); ok {
		label = lane.Name
	}
	title := titleSpan(label, m.focus == panelContents)
	return box(title, strings.Join(rows, "\n"), width, height, m.focus == panelContents)
}

func (m Model) renderPreview(width, height int) string {
	rows := m.previewLines(max(1, width-4), max(1, height-3))
	return box(m.previewTitle(m.focus == panelPreview), strings.Join(rows, "\n"), width, height, m.focus == panelPreview)
}

func (m Model) previewTitle(focused bool) string {
	return titleSpan("preview", focused)
}

func (m Model) laneLines(width, height int) []string {
	lanes := m.filteredLanes()
	if len(lanes) == 0 {
		if m.data.Status == nil && m.hasBootstrapIssue() {
			if m.isBootstrapPrompt() {
				return []string{""}
			}
			return m.bootstrapLines(width, height)
		}
		if m.data.Status == nil && m.loading {
			return m.loadingLines(width, height)
		}
		if m.data.Status == nil && m.err != nil {
			return m.statusErrorLines(width, height)
		}
		return []string{styleDim.Render("no branches")}
	}
	rows := make([]string, 0, len(lanes))
	for idx, lane := range lanes {
		rows = append(rows, m.formatLaneLine(lane, idx, width))
	}
	return windowRows(rows, m.laneCursor, height)
}

func (m Model) loadingLines(width, height int) []string {
	return fitStateLines([]string{
		styleAccent.Render(spinnerFrame(m.spinnerFrame) + " loading GitButler status"),
		styleDim.Render("LazyBut is open; `but status -j` is running in the background."),
		styleDim.Render("Huge repositories can take a while; the UI should stay responsive."),
		"",
		styleHotKey.Render("q") + " " + styleHotLabel.Render("quit"),
	}, width, height)
}

func (m Model) statusErrorLines(width, height int) []string {
	return fitStateLines([]string{
		styleErr.Render("Could not load GitButler status"),
		styleDim.Render(m.err.Error()),
		"",
		styleHotKey.Render("r") + " " + styleHotLabel.Render("retry status load"),
		styleHotKey.Render("q") + " " + styleHotLabel.Render("quit"),
	}, width, height)
}

func (m Model) bootstrapLines(width, height int) []string {
	var lines []string
	if gitbutler.IsCLINotFound(m.err) {
		lines = []string{
			styleWarn.Render("GitButler CLI is required"),
			styleDim.Render("LazyBut delegates Git operations to the official `but` CLI."),
			"",
			styleHotKey.Render("i") + " " + styleHotLabel.Render("install GitButler CLI"),
			styleHotKey.Render("r") + " " + styleHotLabel.Render("refresh after installing"),
		}
	} else {
		lines = []string{
			styleWarn.Render("Repository is not set up for GitButler"),
			styleDim.Render("LazyBut can configure this checkout and then load the workspace."),
			"",
			styleHotKey.Render("g") + " " + styleHotLabel.Render("run `but setup`"),
			styleHotKey.Render("G") + " " + styleHotLabel.Render("run `but setup --init`"),
			styleHotKey.Render("r") + " " + styleHotLabel.Render("refresh after manual setup"),
		}
	}
	lines = append(lines, "", styleDim.Render("Press : to open all actions, or q to quit."))
	for idx := range lines {
		lines[idx] = fit(lines[idx], width)
	}
	if len(lines) > height {
		return lines[:height]
	}
	return lines
}

func fitStateLines(lines []string, width, height int) []string {
	for idx := range lines {
		lines[idx] = fit(lines[idx], width)
	}
	if len(lines) > height {
		return lines[:height]
	}
	return lines
}

func (m Model) formatLaneLine(lane lane, idx, width int) string {
	isCursor := idx == m.laneCursor
	cursor := " "
	if isCursor {
		cursor = "▸"
	}
	prefix := strings.Repeat("  ", lane.Depth)
	// Use the zz badge for the workspace lane; for applied branches the sync
	// chip takes the lead position (no redundant "on" tag).
	leadPlain := ""
	if lane.Kind == laneUnassigned {
		leadPlain = laneBadgeText(lane)
	} else {
		leadPlain = syncChipPlain(lane)
	}
	meta := laneMetaParts(lane)
	plain := cursor
	if leadPlain != "" {
		plain += " " + leadPlain
	}
	plain += " " + prefix + lane.Name
	if meta != "" {
		plain += " " + meta
	}
	plain = fit(plain, width)
	if isCursor {
		return styleSelectedRow.Render(padRight(plain, width))
	}
	if lane.Kind == laneUnassigned {
		return colorizeBadge(plain, leadPlain, lane)
	}
	return colorizeSyncChip(plain, leadPlain, lane)
}

func colorizeSyncChip(line, chip string, lane lane) string {
	if chip == "" {
		return line
	}
	idx := strings.Index(line, chip)
	if idx < 0 {
		return line
	}
	styled := syncChip(lane)
	if styled == "" {
		styled = chip
	}
	return line[:idx] + styled + line[idx+len(chip):]
}

func laneBadgeText(lane lane) string {
	switch lane.Kind {
	case laneUnassigned:
		return "zz"
	case laneAppliedBranch:
		return "on"
	}
	return "  "
}

func laneBadgeStyle(lane lane) lipgloss.Style {
	if lane.MergeClean != nil && !*lane.MergeClean {
		return styleBadgeConflict
	}
	switch lane.Kind {
	case laneUnassigned:
		return styleBadgeZZ
	case laneAppliedBranch:
		return styleBadgeOn
	}
	return styleDim
}

func colorizeBadge(line, badge string, lane lane) string {
	if badge == "" || badge == "  " {
		return line
	}
	idx := strings.Index(line, badge)
	if idx < 0 {
		return line
	}
	return line[:idx] + laneBadgeStyle(lane).Render(badge) + line[idx+len(badge):]
}

func laneMetaParts(lane lane) string {
	parts := []string{}
	if lane.ChangeCount > 0 {
		parts = append(parts, fmt.Sprintf("%df", lane.ChangeCount))
	}
	if lane.CommitCount > 0 {
		parts = append(parts, fmt.Sprintf("%dc", lane.CommitCount))
	}
	if lane.Ahead != nil && *lane.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%s%d", glyphAhead, *lane.Ahead))
	}
	if lane.MergeClean != nil && !*lane.MergeClean {
		parts = append(parts, glyphConflict)
	}
	return strings.Join(parts, " ")
}

// syncChipPlain is the unstyled counterpart of syncChip — used inline in plain
// rows where the surrounding badge/cursor styling owns the color palette.
func syncChipPlain(lane lane) string {
	return formatSyncChip(lane, false)
}

func laneMetaLine(lane lane, width int) string {
	parts := []string{}
	if lane.ChangeCount > 0 {
		parts = append(parts, styleDim.Render(plural(lane.ChangeCount, "file", "files")))
	}
	if lane.CommitCount > 0 {
		parts = append(parts, styleDim.Render(plural(lane.CommitCount, "commit", "commits")))
	}
	if lane.MergeClean != nil && !*lane.MergeClean {
		parts = append(parts, styleErr.Render(glyphConflict+" conflict"))
	}
	if chip := ciChip(lane); chip != "" {
		parts = append(parts, chip)
	}
	if lane.ReviewID != "" {
		parts = append(parts, styleHotKey.Render("PR "+cleanReviewID(lane.ReviewID)))
	}
	if len(parts) == 0 {
		return styleDim.Render(fit("up to date", width))
	}
	sep := styleDim.Render(" " + sepDot + " ")
	joined := strings.Join(parts, sep)
	return fit(joined, width)
}

// ciChip renders a compact CI status badge using Branch.CI counts. Pending in
// yellow, passing in green, failing in red — same convention as GitHub.
func ciChip(lane lane) string {
	if !lane.CIPresent {
		return ""
	}
	parts := []string{}
	if lane.CIFailing > 0 {
		parts = append(parts, styleErr.Render(fmt.Sprintf("%s%d", glyphCross, lane.CIFailing)))
	}
	if lane.CIPending > 0 {
		parts = append(parts, styleWarn.Render(fmt.Sprintf("…%d", lane.CIPending)))
	}
	if lane.CIPassing > 0 {
		parts = append(parts, styleOk.Render(fmt.Sprintf("%s%d", glyphCheck, lane.CIPassing)))
	}
	if len(parts) == 0 {
		return styleDim.Render("CI ?")
	}
	return strings.Join(parts, " ")
}

// cleanReviewID strips parentheses that GitButler sometimes wraps PR ids in
// (e.g. "(#736)" → "#736").
func cleanReviewID(raw string) string {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "(")
	cleaned = strings.TrimSuffix(cleaned, ")")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return raw
	}
	if !strings.HasPrefix(cleaned, "#") {
		cleaned = "#" + cleaned
	}
	return cleaned
}

func plural(n int, singular, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (m Model) contentLines(width, height int) []string {
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

func (m Model) formatContentLine(item contentItem, idx, width int) string {
	isCursor := idx == m.contentCursor
	return renderItemRow(item, m.selected[item.ID], isCursor, width)
}

func (m Model) previewLines(width, height int) []string {
	header := m.previewHeaderRows(width)
	body := m.previewBodyRows(width)
	rows := append([]string{}, header...)
	if len(header) > 0 && len(body) > 0 {
		rows = append(rows, "")
	}
	rows = append(rows, body...)
	if len(rows) == 0 {
		return []string{styleDim.Render("select an item to preview")}
	}
	return windowRows(rows, m.previewScroll, height)
}

// previewHeaderRows builds the always-visible identity card for the focused
// item using metadata we already hold locally — no async wait needed.
func (m Model) previewHeaderRows(width int) []string {
	item, ok := m.selectedContent()
	if !ok || item.ID == "" {
		return nil
	}
	switch item.Kind {
	case contentChange:
		return previewFileHeader(item, width)
	case contentCommit, contentUpstreamCommit:
		return previewCommitHeader(item, width)
	}
	return nil
}

// previewBodyRows renders the async-loaded body (diff or commit summary). It
// strips `but diff`'s own decorative file-header so the path isn't shown twice.
func (m Model) previewBodyRows(width int) []string {
	if m.previewErr != nil {
		return splitLines(styleErr.Render(m.previewErr.Error()))
	}
	if m.preview == "" {
		if m.previewSelectionTarget() != "" {
			return []string{styleDim.Render("loading diff…")}
		}
		return nil
	}
	rows := splitLines(m.preview)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if isBoxDecoration(row) || isPreviewDuplicateFileHeader(row) {
			continue
		}
		out = append(out, styleDiffLine(fit(row, width)))
	}
	if len(out) == 0 {
		return []string{styleDim.Render("(empty)")}
	}
	return out
}

// isPreviewDuplicateFileHeader matches `but diff`'s `<id> <path>│` line so we
// can skip it (our own header already shows path and id).
func isPreviewDuplicateFileHeader(line string) bool {
	if !strings.HasSuffix(line, "│") {
		return false
	}
	inner := strings.TrimSuffix(line, "│")
	return !looksLikeDiffGutter(inner)
}

func previewFileHeader(item contentItem, width int) []string {
	glyphStyle := fileChangeStyle(item.Detail)
	idStyle := fileIDStyle(item.Detail)
	parts := []string{glyphStyle.Render(glyphFile), idStyle.Render(displayItemID(item))}
	if t := strings.ToLower(item.Detail); t != "" {
		parts = append(parts, styleDim.Render(t))
	}
	if item.ReviewID != "" {
		parts = append(parts, styleHotKey.Render(cleanReviewID(item.ReviewID)))
	}
	row1 := fit(strings.Join(parts, " "), width)
	row2 := previewPathRow(item.Label, item.Detail, width)
	return []string{row1, row2}
}

func previewPathRow(label, detail string, width int) string {
	base := filepath.Base(label)
	if base == label || base == "" {
		return fit(fileNameStyle(detail).Render(label), width)
	}
	dir := strings.TrimSuffix(label, base)
	return fit(stylePathDim.Render(dir)+fileNameStyle(detail).Render(base), width)
}

func previewCommitHeader(item contentItem, width int) []string {
	glyph := glyphCommit
	gStyle := styleNodeCommit
	if item.Kind == contentUpstreamCommit {
		glyph = glyphUpstream
		gStyle = styleNodeUpstream
	}
	if item.Conflicted {
		glyph = glyphConflict
		gStyle = styleErr
	}
	row1Parts := []string{gStyle.Render(glyph), styleIDDim.Render(item.ID)}
	if item.ReviewID != "" {
		row1Parts = append(row1Parts, styleHotKey.Render(cleanReviewID(item.ReviewID)))
	}
	if item.Conflicted {
		row1Parts = append(row1Parts, styleErr.Render("conflicted"))
	}
	row1 := fit(strings.Join(row1Parts, " "), width)
	row2 := fit(styleTitle.Render(firstLine(item.Label)), width)
	metaParts := []string{}
	if item.Author != "" {
		metaParts = append(metaParts, item.Author)
	}
	if ago := compactAgo(item.Created); ago != "" {
		metaParts = append(metaParts, ago)
	}
	if h := shortHash(item.Hash); h != "" && h != item.ID {
		metaParts = append(metaParts, h)
	}
	rows := []string{row1, row2}
	if len(metaParts) > 0 {
		rows = append(rows, fit(styleDim.Render(strings.Join(metaParts, " "+sepDot+" ")), width))
	}
	return rows
}

// isBoxDecoration returns true for lines that are only ─╮╯└┘├┤┬┴┼ box-drawing
// characters (and whitespace). `but diff` wraps its file header in such borders;
// stripping them removes visual noise without losing information.
func isBoxDecoration(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.ContainsRune(trimmed, '─') {
		return false
	}
	for _, r := range trimmed {
		switch r {
		case '─', '╮', '╯', '┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼', '│':
			// ok
		default:
			return false
		}
	}
	return true
}

// styleDiffLine applies diff colors to a single line of `but diff` output.
//
//	<spaces><digits>?<spaces><digits>│ <ctx>     → context
//	<spaces><digits>│+<text>                    → addition
//	<digits><spaces>│-<text>                    → removal
//	<text>│                                     → file header (no diff gutter)
func styleDiffLine(line string) string {
	if line == "" {
		return line
	}
	pipeIdx := strings.Index(line, "│")
	if pipeIdx < 0 {
		return styleDiffCtx.Render(line)
	}
	gutterRaw := line[:pipeIdx]
	rest := line[pipeIdx+len("│"):]

	if !looksLikeDiffGutter(gutterRaw) {
		// File header `x9 path/to/file.go│` — drop the trailing │ separator.
		if rest == "" {
			return styleDiffHeader.Render(gutterRaw)
		}
		return styleDiffHeader.Render(gutterRaw) + styleDiffGutter.Render("│"+rest)
	}

	gutter := styleDiffGutter.Render(line[:pipeIdx+len("│")])
	if rest == "" {
		return gutter
	}
	switch rest[0] {
	case '+':
		return gutter + styleDiffAdd.Render(rest)
	case '-':
		return gutter + styleDiffRem.Render(rest)
	default:
		return gutter + styleDiffCtx.Render(rest)
	}
}

func looksLikeDiffGutter(s string) bool {
	if s == "" {
		return false
	}
	hasDigit := false
	for _, r := range s {
		if unicode.IsDigit(r) {
			hasDigit = true
			continue
		}
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return hasDigit
}

func (m Model) renderHotbar() string {
	actions := []hint{}
	for _, a := range m.contextActions() {
		actions = append(actions, hint{a.Key, actionShortLabel(a)})
	}
	meta := []hint{
		{":", "actions"},
		{"/", "filter"},
		{"?", "help"},
		{"q", "quit"},
	}
	sep := " " + styleHotSep.Render(sepDot) + " "
	render := func(hints []hint) string {
		parts := make([]string, 0, len(hints))
		for _, h := range hints {
			parts = append(parts, styleHotKey.Render(h.key)+" "+styleHotLabel.Render(h.label))
		}
		return strings.Join(parts, sep)
	}
	// Drop trailing actions until the full bar fits — meta keys are always preserved.
	for {
		line := render(append(append([]hint{}, actions...), meta...))
		if lipgloss.Width(line) <= m.width {
			return line
		}
		if len(actions) == 0 {
			// Even meta overflows — just truncate.
			return fit(line, m.width)
		}
		actions = actions[:len(actions)-1]
	}
}

type hint struct {
	key   string
	label string
}

func (m Model) contextActions() []action {
	all := m.availableActions()
	pinned := []actionID{
		actionRefresh,
		actionInstallGitButler,
		actionSetup,
		actionSetupInit,
		actionAddBranch,
		actionNewBranch,
		actionStage,
		actionCommit,
		actionAmend,
		actionDiscard,
		actionPush,
		actionPull,
		actionUndo,
	}
	byID := map[actionID]action{}
	for _, a := range all {
		byID[a.ID] = a
	}
	out := []action{}
	for _, id := range pinned {
		if a, ok := byID[id]; ok && a.Key != "" {
			out = append(out, a)
		}
	}
	return out
}

func actionShortLabel(a action) string {
	switch a.ID {
	case actionRefresh:
		return "refresh"
	case actionAddBranch:
		return "branch"
	case actionNewBranch:
		return "new"
	case actionNewStacked:
		return "stack"
	case actionStage:
		return "assign"
	case actionCommit:
		return "commit"
	case actionAmend:
		return "amend"
	case actionAbsorb:
		return "absorb"
	case actionDiscard:
		return "discard"
	case actionDelete:
		return "delete"
	case actionRename:
		return "rename"
	case actionApplyToggle:
		return "unapply"
	case actionPush:
		return "push"
	case actionPushDryRun:
		return "push?"
	case actionForcePush:
		return "fpush"
	case actionPull:
		return "update"
	case actionPullCheck:
		return "check"
	case actionMerge:
		return "merge"
	case actionMove:
		return "move"
	case actionRub:
		return "rub"
	case actionSquash:
		return "squash"
	case actionUncommit:
		return "uncommit"
	case actionUndo:
		return "undo"
	case actionSnapshot:
		return "snap"
	case actionRestore:
		return "restore"
	case actionClean:
		return "clean"
	case actionCleanDryRun:
		return "clean?"
	case actionNewPR:
		return "PR"
	case actionNewDraftPR:
		return "draftPR"
	case actionPRDraft:
		return "PR draft"
	case actionPRReady:
		return "PR ready"
	case actionResolveStatus:
		return "resolve"
	case actionResolveFinish:
		return "finish"
	case actionResolveCancel:
		return "cancel"
	case actionInstallGitButler:
		return "install"
	case actionSetup:
		return "setup"
	case actionSetupInit:
		return "init"
	}
	if a.Label == "" {
		return string(a.ID)
	}
	return a.Label
}

func (m Model) renderPrompt() string {
	label := m.prompt.Action.InputLabel
	if label == "" {
		label = "value"
	}
	width := min(70, max(34, m.width-8))
	header := styleAccent.Render(m.prompt.Action.Label)
	if header == "" {
		header = styleAccent.Render(label)
	}
	caret := "▏"
	if m.spinnerFrame%2 == 0 {
		caret = " "
	}
	body := styleDim.Render(label) + "\n\n" + m.prompt.Value + styleAccent.Render(caret)
	footer := styleHotKey.Render("enter") + " " + styleHotLabel.Render("submit") + "   " + styleHotKey.Render("esc") + " " + styleHotLabel.Render("cancel")
	return styleOverlay.Width(width).Render(header + "\n\n" + body + "\n\n" + footer)
}

func (m Model) renderConfirm() string {
	if m.confirm.Action.ID == actionPull {
		return m.renderUpstreamConfirm()
	}
	text := m.confirm.Action.ConfirmText
	if text == "" {
		text = "Confirm this action?"
	}
	width := min(72, max(38, m.width-8))
	header := styleWarn.Render(m.confirm.Action.Label)
	if m.confirm.Action.Dangerous {
		header = styleErr.Render(m.confirm.Action.Label + " (destructive)")
	}
	if m.confirm.Action.Label == "" {
		header = styleWarn.Render("confirm")
	}
	body := text
	if m.confirm.Input != "" {
		body += "\n\n" + styleDim.Render(m.confirm.Input)
	}
	confirmLabel, cancelLabel := "confirm", "cancel"
	switch m.confirm.Action.ID {
	case actionInstallGitButler:
		confirmLabel, cancelLabel = "install", "later"
	case actionSetup:
		confirmLabel, cancelLabel = "run setup", "later"
	}
	footer := styleHotKey.Render("enter/y") + " " + styleHotLabel.Render(confirmLabel) + "   " + styleHotKey.Render("esc/n") + " " + styleHotLabel.Render(cancelLabel)
	return styleOverlay.Width(width).Render(header + "\n\n" + body + "\n\n" + footer)
}

func (m Model) renderUpstreamConfirm() string {
	width := m.modalWidth(80, 48)
	innerW := width - 6
	lanes := m.upstreamBranchLanes()
	incoming := m.incomingChangeCount()
	if len(lanes) > 0 && m.confirm.Cursor >= len(lanes) {
		m.confirm.Cursor = len(lanes) - 1
	}
	hasConflicts := m.hasUpstreamConflicts()

	title := styleAccent.Render("update from upstream")
	subtitle := styleDim.Render(plural(incoming, "incoming change", "incoming changes"))

	rows := []string{subtitle, "", m.renderIncomingCard(innerW)}
	rows = append(rows, "", styleDim.Render("branches to rebase"))
	rows = append(rows, m.renderUpstreamBranchList(lanes, innerW, m.upstreamBranchRowBudget(len(lanes)), m.confirm.Cursor))
	if hasConflicts {
		rows = append(rows, "", styleErr.Render(glyphConflict+" known conflicts — review before applying"))
	}
	footer := modalFooter(
		keyHint("y/enter", "update"),
		keyHint("u", "dry-check"),
		keyHint("n/esc", "cancel"),
	)
	return renderModal(width, title, strings.Join(rows, "\n"), footer)
}

func (m Model) renderIncomingCard(width int) string {
	commit := m.incomingCommit()
	title := firstLine(commit.Message)
	if title == "" {
		title = "Latest target update"
	}
	meta := []string{}
	if hash := shortHash(firstNonEmpty(commit.CLIID, commit.CommitID)); hash != "" {
		meta = append(meta, hash)
	}
	if commit.ReviewID != nil && *commit.ReviewID != "" {
		meta = append(meta, "PR "+cleanReviewID(*commit.ReviewID))
	}
	if ago := compactAgo(commit.CreatedAt); ago != "" {
		meta = append(meta, ago)
	}
	if commit.AuthorName != "" {
		meta = append(meta, commit.AuthorName)
	}
	bodyW := max(1, width-3)
	headline := styleNodeUpstream.Render(glyphUpstream+" ") + fit(title, bodyW)
	if len(meta) == 0 {
		return headline
	}
	return headline + "\n" + styleDim.Render("  "+fit(strings.Join(meta, " "+sepDot+" "), bodyW))
}

func (m Model) renderUpstreamBranchList(lanes []lane, width, maxRows, cursor int) string {
	if len(lanes) == 0 {
		return styleDim.Render("  no active branches")
	}
	maxRows = max(1, maxRows)
	rows := []string{}
	start := windowStart(len(lanes), cursor, maxRows)
	end := min(len(lanes), start+maxRows)
	for idx := start; idx < end; idx++ {
		rows = append(rows, upstreamBranchRow(lanes[idx], width, idx == cursor))
	}
	if end < len(lanes) {
		rows = append(rows, styleDim.Render(fmt.Sprintf("  +%d more", len(lanes)-end)))
	}
	return strings.Join(rows, "\n")
}

func upstreamBranchRow(lane lane, width int, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "▸ "
	}
	name := fit(lane.Name, max(1, width-lipgloss.Width(prefix)-12))
	right := ""
	switch {
	case lane.MergeClean != nil && !*lane.MergeClean:
		right = styleErr.Render(glyphConflict + " conflict")
	case lane.PushStatus == "integrated":
		right = styleMerged.Render(glyphMerged + " merged")
	}
	line := prefix + name
	if right != "" {
		gap := max(1, width-lipgloss.Width(line)-lipgloss.Width(right))
		line += strings.Repeat(" ", gap) + right
	}
	if selected {
		return styleSelectedRow.Render(padRight(line, width))
	}
	return line
}

func (m Model) upstreamBranchRowBudget(total int) int {
	if total <= 0 {
		return 1
	}
	// fixed rows above the branch list: title, blank, subtitle, blank, card(2),
	// blank, branches-label = 8 — plus blank+footer below = +2, plus border+pad.
	fixedRows := 14
	contentRows := max(1, m.height-4)
	return max(1, min(total, contentRows-fixedRows))
}

func (m Model) upstreamConfirmRowAt(y int) (int, bool) {
	lanes := m.upstreamBranchLanes()
	if len(lanes) == 0 {
		return 0, false
	}
	visible := m.upstreamBranchRowBudget(len(lanes))
	// Modal layout (renderUpstreamConfirm): border(1) + pad(1) + title(1) +
	// blank(1) + subtitle(1) + blank(1) + card(2) + blank(1) + label(1) +
	// rows(visible) + (conflict line?) + blank(1) + footer(1) + pad(1) + border(1)
	fixedAbove := 10 // up to and including the "branches to rebase" label
	modalH := fixedAbove + visible + 5
	startY := max(0, (m.height-modalH)/2)
	firstRow := startY + fixedAbove
	row := y - firstRow
	if row < 0 || row >= visible {
		return 0, false
	}
	return windowStart(len(lanes), m.confirm.Cursor, visible) + row, true
}

func (m Model) isUpstreamConfirmFooter(x, y int) bool {
	visible := m.upstreamBranchRowBudget(len(m.upstreamBranchLanes()))
	fixedAbove := 10
	conflictRows := 0
	if m.hasUpstreamConflicts() {
		conflictRows = 2
	}
	modalH := fixedAbove + visible + conflictRows + 3
	startY := max(0, (m.height-modalH)/2)
	footerY := startY + fixedAbove + visible + conflictRows + 1
	return y == footerY && x >= 0 && x < m.width
}

func (m Model) upstreamBranchLanes() []lane {
	out := []lane{}
	for _, lane := range m.data.Lanes {
		if lane.Kind == laneAppliedBranch {
			out = append(out, lane)
		}
	}
	return out
}

func (m Model) hasUpstreamConflicts() bool {
	for _, lane := range m.upstreamBranchLanes() {
		if lane.MergeClean != nil && !*lane.MergeClean {
			return true
		}
	}
	return false
}

func (m Model) incomingChangeCount() int {
	if m.data.Status == nil {
		return 0
	}
	if n := m.data.Status.UpstreamState.Behind; n > 0 {
		return n
	}
	return len(m.data.Status.UpstreamState.UpstreamCommits)
}

func (m Model) incomingCommit() gitbutler.Commit {
	if m.data.Status == nil {
		return gitbutler.Commit{}
	}
	commit := m.data.Status.UpstreamState.LatestCommit
	if commit.Message != "" || commit.CommitID != "" || commit.CLIID != "" {
		return commit
	}
	if len(m.data.Status.UpstreamState.UpstreamCommits) > 0 {
		return m.data.Status.UpstreamState.UpstreamCommits[0]
	}
	return gitbutler.Commit{}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func compactAgo(raw string) string {
	if raw == "" {
		return ""
	}
	candidates := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05"}
	var t time.Time
	var err error
	for _, layout := range candidates {
		t, err = time.Parse(layout, raw)
		if err == nil {
			break
		}
	}
	if err != nil {
		return ""
	}
	elapsed := time.Since(t)
	switch {
	case elapsed < 0:
		return "now"
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
	}
}

// modalWidth clamps the modal width to a readable range. All overlay pickers
// share the same sizing strategy: prefer prefMax, never go below prefMin, and
// always leave a small margin around the terminal edges.
func (m Model) modalWidth(prefMax, prefMin int) int {
	return min(prefMax, max(prefMin, m.width-8))
}

func keyHint(key, label string) string {
	return styleHotKey.Render(key) + " " + styleHotLabel.Render(label)
}

func modalFooter(hints ...string) string {
	return strings.Join(hints, "   ")
}

// renderModal composes a centred overlay box: title, blank, body, blank,
// footer. All fragments must already be styled.
func renderModal(width int, title, body, footer string) string {
	sections := []string{title}
	if body != "" {
		sections = append(sections, "", body)
	}
	if footer != "" {
		sections = append(sections, "", footer)
	}
	return styleOverlay.Width(width).Render(strings.Join(sections, "\n"))
}

// cursorRow applies the selected-row highlight to an already-fit row, replacing
// the leading char with `▸` so the cursor still reads on monochrome terminals.
func cursorRow(line string, innerW int) string {
	if line == "" {
		return styleSelectedRow.Render(padRight("▸", innerW))
	}
	return styleSelectedRow.Render(padRight("▸"+line[1:], innerW))
}

func (m Model) renderPalette() string {
	width := m.modalWidth(72, 40)
	innerW := width - 6
	title := styleAccent.Render("actions")
	if len(m.palette) == 0 {
		return renderModal(width, title, styleDim.Render("no actions"), "")
	}
	rows := make([]string, 0, len(m.palette))
	for idx, action := range m.palette {
		key := action.Key
		if key == "" {
			key = "enter"
		}
		raw := fit(fmt.Sprintf("  %-8s %s", key, action.Label), innerW)
		if idx == m.paletteCursor {
			rows = append(rows, cursorRow(raw, innerW))
			continue
		}
		rows = append(rows, strings.Replace(raw, action.Key, styleHotKey.Render(action.Key), 1))
	}
	footer := modalFooter(keyHint("enter", "run"), keyHint("j/k", "move"), keyHint("esc", "close"))
	return renderModal(width, title, strings.Join(rows, "\n"), footer)
}

func (m Model) renderTargetPicker() string {
	width := m.modalWidth(80, 44)
	innerW := width - 6
	title := styleAccent.Render(m.targetPicker.Title)
	if len(m.targetPicker.Items) == 0 {
		return renderModal(width, title, styleDim.Render("no targets"), keyHint("esc", "cancel"))
	}
	rows := make([]string, 0, len(m.targetPicker.Items))
	for idx, item := range m.targetPicker.Items {
		meta := ""
		if item.Meta != "" {
			meta = " " + styleDim.Render(item.Meta)
		}
		check := ""
		if m.targetPicker.Multi {
			if m.targetPicker.Selected[idx] {
				check = styleOk.Render("[✓] ")
			} else {
				check = styleFaint.Render("[ ] ")
			}
		}
		fitted := fit("  "+check+item.Label, innerW-lipgloss.Width(meta))
		if idx == m.targetPicker.Cursor {
			rows = append(rows, styleSelectedRow.Render(padRight("▸ "+fitted[2:]+meta, innerW)))
			continue
		}
		rows = append(rows, fitted+meta)
	}
	hints := []string{}
	if m.targetPicker.Multi {
		hints = append(hints, keyHint("space", "toggle"), keyHint("enter", "apply"))
	} else {
		hints = append(hints, keyHint("enter", "select"))
	}
	hints = append(hints, keyHint("j/k", "move"), keyHint("esc", "cancel"))
	return renderModal(width, title, strings.Join(rows, "\n"), modalFooter(hints...))
}

func (m Model) renderBranchPicker() string {
	width := m.modalWidth(72, 40)
	innerW := width - 6
	title := styleAccent.Render("add branch to workspace")
	if len(m.data.BranchOptions) == 0 {
		return renderModal(width, title, styleDim.Render("no inactive branches"),
			modalFooter(keyHint("n", "new branch"), keyHint("esc", "cancel")))
	}
	rows := make([]string, 0, len(m.data.BranchOptions))
	for idx, branch := range m.data.BranchOptions {
		meta := []string{}
		if branch.Ahead != nil {
			meta = append(meta, fmt.Sprintf("+%d", *branch.Ahead))
		}
		if branch.MergeClean != nil && !*branch.MergeClean {
			meta = append(meta, "conflict")
		}
		if branch.HasLocal {
			meta = append(meta, "local")
		}
		raw := "  " + branch.Name
		if len(meta) > 0 {
			raw += "  " + strings.Join(meta, " ")
		}
		raw = fit(raw, innerW)
		if idx == m.branchCursor {
			rows = append(rows, cursorRow(raw, innerW))
			continue
		}
		rows = append(rows, raw)
	}
	rows = windowRows(rows, m.branchCursor, branchPickerWindowHeight(m.height))
	footer := modalFooter(keyHint("enter", "apply"), keyHint("j/k", "move"), keyHint("n", "new"), keyHint("esc", "cancel"))
	return renderModal(width, title, strings.Join(rows, "\n"), footer)
}

func (m Model) renderHelp() string {
	width := min(86, max(48, m.width-8))
	header := styleAccent.Render("lazybut · help")
	body := strings.Join([]string{
		styleDim.Render("Navigation"),
		"  " + styleHotKey.Render("h/l") + " " + styleHotLabel.Render("columns") + "   " + styleHotKey.Render("j/k") + " " + styleHotLabel.Render("items") + "   " + styleHotKey.Render("tab") + " " + styleHotLabel.Render("focus") + "   " + styleHotKey.Render("/") + " " + styleHotLabel.Render("filter"),
		"  " + styleHotKey.Render("ctrl+u/d") + " " + styleHotLabel.Render("scroll preview") + "   " + styleHotLabel.Render("(mouse wheel works on every panel)"),
		"",
		styleDim.Render("Workspace"),
		"  " + styleHotLabel.Render("kanban shows zz + active branches; ") + styleHotKey.Render("+") + " " + styleHotLabel.Render("or") + " " + styleHotKey.Render("B") + " " + styleHotLabel.Render("opens inactive branches"),
		"  " + styleHotKey.Render("u") + " " + styleHotLabel.Render("checks upstream update; ") + styleHotKey.Render("U") + " " + styleHotLabel.Render("updates/rebases all applied branches"),
		"",
		styleDim.Render("Actions"),
		"  " + styleHotKey.Render(":") + " " + styleHotLabel.Render("action palette") + "   " + styleHotKey.Render("space/v") + " " + styleHotLabel.Render("select"),
		"  " + styleHotLabel.Render("destructive actions ask confirmation before running ") + styleAccent.Render("but"),
		"",
		styleDim.Render("Layout"),
		"  " + styleHotLabel.Render("kanban above ~70 cols · focused list below · preview strip docks at the bottom"),
		"",
		styleHotKey.Render("esc") + " " + styleHotLabel.Render("closes this help"),
	}, "\n")
	return styleOverlay.Width(width).Render(header + "\n\n" + body)
}

func (m Model) laneKanbanTitle(lane lane, index int) string {
	prefix := ""
	if index == m.laneCursor {
		prefix = styleAccent.Render("▸ ")
	}
	// For zz keep the workspace badge as the lead; for applied branches the
	// sync chip stands in for the redundant "on" tag.
	var lead string
	if lane.Kind == laneUnassigned {
		lead = styleBadgeZZ.Render(laneBadgeText(lane))
	} else {
		lead = syncChip(lane)
	}
	parts := prefix
	if lead != "" {
		parts += lead + " "
	}
	parts += styleTitle.Render(lane.Name)
	if m.filter != "" && index == m.laneCursor {
		parts += " " + styleHotKey.Render("/"+m.filter)
	}
	return parts
}

// syncSummary derives the LazyGit-style ahead/behind state for a branch lane
// from GitButler's branchStatus enum and the local/upstream commit arrays.
// behind is exact (len of UpstreamCommits); ahead is the local commit count
// when branchStatus signals unpushed work, else zero.
func syncSummary(lane lane) (behind, ahead int, forceRequired, synced, integrated bool) {
	if lane.Kind != laneAppliedBranch {
		return 0, 0, false, false, false
	}
	behind = lane.UpstreamCount
	switch lane.PushStatus {
	case "integrated":
		// Branch has been merged into the target — no push needed, branch is shippable.
		integrated = true
	case "nothingToPush", "fullyPushed", "":
		synced = behind == 0
	case "completelyUnpushed":
		ahead = lane.CommitCount
	case "unpushedCommitsRequiringForce":
		ahead = lane.CommitCount
		forceRequired = true
	case "unpushedCommits":
		ahead = lane.CommitCount
	default:
		if lane.CommitCount > 0 {
			ahead = lane.CommitCount
		}
	}
	return
}

// syncChip mirrors LazyGit's ↓N↑M compact indicator with color.
func syncChip(lane lane) string {
	return formatSyncChip(lane, true)
}

func formatSyncChip(lane lane, styled bool) string {
	behind, ahead, forceRequired, synced, integrated := syncSummary(lane)
	style := func(s lipgloss.Style, text string) string {
		if styled {
			return s.Render(text)
		}
		return text
	}
	if integrated {
		return style(styleMerged, glyphMerged+" merged")
	}
	parts := []string{}
	if behind > 0 {
		parts = append(parts, style(styleWarn, fmt.Sprintf("%s%d", glyphBehind, behind)))
	}
	if ahead > 0 {
		text := fmt.Sprintf("%s%d", glyphAhead, ahead)
		if forceRequired {
			text += "!"
			parts = append(parts, style(styleErr, text))
		} else {
			parts = append(parts, style(styleWarn, text))
		}
	}
	if len(parts) == 0 && synced {
		return style(styleOk, glyphCheck)
	}
	return strings.Join(parts, " ")
}

func titleSpan(label string, focused bool) string {
	if focused {
		return styleTitle.Render(label)
	}
	return styleTitleBlur.Render(label)
}

func box(title, body string, width, height int, focused bool) string {
	style := styleBlur
	if focused {
		style = styleFocus
	}
	innerW := contentWidth(width)
	innerH := contentHeight(height)
	header := fit(title, innerW)
	lines := []string{header}
	for _, line := range splitLines(body) {
		if len(lines) >= innerH {
			break
		}
		lines = append(lines, fit(line, innerW))
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	return style.Width(innerW).Height(innerH).Render(strings.Join(lines, "\n"))
}

// overlay places `content` centred over `base`, preserving the visible base
// content on either side of the modal — so columns/preview stay readable
// around an open dialog.
func overlay(base string, width, height int, content string) string {
	lines := splitLines(base)
	if height <= 0 {
		return strings.Join(lines, "\n")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	contentLines := splitLines(content)
	startX, startY, contentW, contentH := overlayBounds(width, height, content)
	if len(contentLines) > contentH {
		contentLines = contentLines[:contentH]
	}
	const ansiReset = "\x1b[0m"
	for idx, modal := range contentLines {
		y := startY + idx
		if y < 0 || y >= len(lines) {
			continue
		}
		base := lines[y]
		left, rest := ansiSplit(base, startX)
		if lw := lipgloss.Width(left); lw < startX {
			left += strings.Repeat(" ", startX-lw)
		}
		_, right := ansiSplit(rest, contentW)
		lines[y] = left + ansiReset + modal + ansiReset + right
	}
	return strings.Join(lines, "\n")
}

func overlayBounds(width, height int, content string) (startX, startY, contentW, contentH int) {
	contentLines := splitLines(content)
	if len(contentLines) > height {
		contentLines = contentLines[:height]
	}
	for _, l := range contentLines {
		if w := lipgloss.Width(l); w > contentW {
			contentW = w
		}
	}
	contentH = len(contentLines)
	startY = max(0, (height-contentH)/2)
	startX = max(0, (width-contentW)/2)
	return startX, startY, contentW, contentH
}

// ansiSplit returns the prefix of s spanning the first `cols` visible columns
// and the remainder. ANSI escape sequences pass through unchanged and do not
// count toward column width.
func ansiSplit(s string, cols int) (left, right string) {
	if cols <= 0 {
		return "", s
	}
	var leftBuf, rightBuf strings.Builder
	width := 0
	inEsc := false
	cut := false
	for _, r := range s {
		if cut {
			rightBuf.WriteRune(r)
			continue
		}
		if r == 0x1b {
			inEsc = true
			leftBuf.WriteRune(r)
			continue
		}
		if inEsc {
			leftBuf.WriteRune(r)
			if ansi.IsTerminator(r) {
				inEsc = false
			}
			continue
		}
		w := lipgloss.Width(string(r))
		if width+w > cols {
			cut = true
			rightBuf.WriteRune(r)
			continue
		}
		width += w
		leftBuf.WriteRune(r)
	}
	return leftBuf.String(), rightBuf.String()
}

func itemKindLabel(kind contentKind) string {
	switch kind {
	case contentChange:
		return "file"
	case contentCommit:
		return "commit"
	case contentUpstreamCommit:
		return "upstr"
	default:
		return "info"
	}
}

func preserveID(id, label string, maxLabelWidth int) string {
	if id == "-" {
		return fit(label, maxLabelWidth)
	}
	return id + " " + fit(label, max(1, maxLabelWidth-lipgloss.Width(id)-1))
}

func contentWidth(width int) int {
	return max(1, width-4)
}

func contentHeight(height int) int {
	return max(1, height-2)
}

func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	const ellipsis = "..."
	if width <= lipgloss.Width(ellipsis) {
		return strings.Repeat(".", width)
	}
	limit := width - lipgloss.Width(ellipsis)
	var out strings.Builder
	out.Grow(len(value))
	acc := 0
	inEsc := false
	for _, r := range value {
		if r == 0x1b {
			inEsc = true
			out.WriteRune(r)
			continue
		}
		if inEsc {
			out.WriteRune(r)
			if ansi.IsTerminator(r) {
				inEsc = false
			}
			continue
		}
		w := lipgloss.Width(string(r))
		if acc+w > limit {
			break
		}
		acc += w
		out.WriteRune(r)
	}
	// Reset any in-flight ANSI styling so the ellipsis isn't tinted by a
	// truncated style span.
	return out.String() + "\x1b[0m" + ellipsis
}

func padRight(value string, width int) string {
	w := lipgloss.Width(value)
	if w >= width {
		return value
	}
	return value + strings.Repeat(" ", width-w)
}

func splitLines(value string) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return []string{""}
	}
	return strings.Split(value, "\n")
}

func windowRows(rows []string, cursor, height int) []string {
	if height <= 0 || len(rows) <= height {
		return rows
	}
	start := windowStart(len(rows), cursor, height)
	return rows[start : start+height]
}

func windowStart(total, cursor, height int) int {
	if height <= 0 || total <= height {
		return 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	if start+height > total {
		start = total - height
	}
	return start
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
