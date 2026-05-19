package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

func loadFixtureStatus(t *testing.T) *gitbutler.WorkspaceStatus {
	t.Helper()
	raw, err := os.ReadFile("../gitbutler/testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	var status gitbutler.WorkspaceStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	return &status
}

func loadFixtureBranches(t *testing.T) *gitbutler.BranchList {
	t.Helper()
	raw, err := os.ReadFile("../gitbutler/testdata/branch_list.json")
	if err != nil {
		t.Fatal(err)
	}
	var branches gitbutler.BranchList
	if err := json.Unmarshal(raw, &branches); err != nil {
		t.Fatal(err)
	}
	return &branches
}

func TestBuildWorkspaceData(t *testing.T) {
	data := buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))

	if len(data.Lanes) != 2 {
		t.Fatalf("lanes = %d, want 2", len(data.Lanes))
	}
	if data.Lanes[0].Name != "unassigned changes" {
		t.Fatalf("first lane = %q", data.Lanes[0].Name)
	}
	if data.Lanes[1].Name != "feature/ui" || !data.Lanes[1].Applied {
		t.Fatalf("applied branch lane not built: %#v", data.Lanes[1])
	}
	if len(data.BranchOptions) != 1 || data.BranchOptions[0].Name != "feature/unapplied" {
		t.Fatalf("branch options not built: %#v", data.BranchOptions)
	}
}

func TestBuildFastWorkspaceData(t *testing.T) {
	data := buildFastWorkspaceData([]gitbutler.FileChange{{
		CLIID:      "git:main.go",
		FilePath:   "main.go",
		ChangeType: "modified",
	}})

	if data.Status != nil || !data.Fast {
		t.Fatalf("fast data should not pretend GitButler status is loaded: %#v", data)
	}
	if len(data.Lanes) != 1 || data.Lanes[0].ChangeCount != 1 {
		t.Fatalf("fast lane not built: %#v", data.Lanes)
	}
	items := data.ContentFor(0)
	if len(items) != 1 || items[0].ID != "git:main.go" || items[0].Label != "main.go" {
		t.Fatalf("fast content not built: %#v", items)
	}
}

func TestContentForAppliedBranch(t *testing.T) {
	data := buildWorkspaceData(loadFixtureStatus(t), loadFixtureBranches(t))
	items := data.ContentFor(1)

	if len(items) < 2 {
		t.Fatalf("content items = %d, want at least 2", len(items))
	}
	if items[0].Kind != contentChange || items[0].ID != "ae:sv" {
		t.Fatalf("first item = %#v", items[0])
	}
	if items[1].Kind != contentCommit || !strings.Contains(items[1].Label, "tui shell") {
		t.Fatalf("commit item = %#v", items[1])
	}
}
