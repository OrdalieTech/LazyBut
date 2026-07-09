package tui

import (
	"strings"
	"testing"
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
