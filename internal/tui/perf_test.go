package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

// The UI tick must stop when nothing animates and restart when something does —
// otherwise an idle lazybut re-renders at ~11fps forever.
func TestTickStopsWhenIdleAndRestartsOnActivity(t *testing.T) {
	m := newModel(nil)
	m.loading = false
	m.data = buildWorkspaceData(nil, nil)

	model, cmd := m.Update(tickMsg{})
	idle := model.(Model)
	if cmd != nil {
		t.Fatalf("idle tick should not reschedule")
	}
	if idle.ticking {
		t.Fatalf("ticking should be false after idle tick")
	}

	idle.setToast("hello", toastInfo)
	model, cmd = idle.Update(tickMsg{})
	if cmd == nil {
		t.Fatalf("tick should restart while a toast is visible")
	}
	if !model.(Model).ticking {
		t.Fatalf("ticking should be true while a toast is visible")
	}
}

// The kanban must stay O(visible) per frame: styling all 2,000 rows before
// windowing cost ~14ms/frame and ~11MB of garbage per frame on big repos.
func BenchmarkViewLargeWorkspace(b *testing.B) {
	status := &gitbutler.WorkspaceStatus{}
	for i := 0; i < 2000; i++ {
		status.UnassignedChanges = append(status.UnassignedChanges, gitbutler.FileChange{
			CLIID:    fmt.Sprintf("c%d", i),
			FilePath: fmt.Sprintf("internal/pkg%d/file%d.go", i%40, i),
		})
	}
	m := newModel(nil)
	m.loading = false
	m.width = 200
	m.height = 50
	m.data = buildWorkspaceData(status, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.View()
	}
}

// previewLines must style only the visible window, not the whole diff.
func BenchmarkPreviewLinesHugeDiff(b *testing.B) {
	m := newModel(nil)
	lines := make([]string, 20000)
	for i := range lines {
		lines[i] = "  42   43│+added line of code in a reasonably long file"
	}
	m.setPreview("x1", strings.Join(lines, "\n"), nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.previewLines(120, 10)
	}
}
