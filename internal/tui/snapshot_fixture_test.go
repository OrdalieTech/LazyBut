package tui

import (
	"os"
	"testing"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

// TestDumpFixtureRender writes a fixture-driven view to /tmp/lazybut-vis/fixture.txt
// when LAZYBUT_DUMP=1 is set. This lets manual visual review reproduce the
// "files + commits in the same column" layout from the synthetic fixture even
// when the live repo has no such stack at the moment.
func TestDumpFixtureRender(t *testing.T) {
	if os.Getenv("LAZYBUT_DUMP") == "" {
		t.Skip("set LAZYBUT_DUMP=1 to write fixture render")
	}
	model := newModel(gitbutler.NewClient(".", nil))
	model.data = buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	model.loading = false
	model.width = 160
	model.height = 32
	model.laneCursor = 1
	if err := os.MkdirAll("/tmp/lazybut-vis", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/lazybut-vis/fixture.txt", []byte(model.View()), 0o644); err != nil {
		t.Fatal(err)
	}
}
