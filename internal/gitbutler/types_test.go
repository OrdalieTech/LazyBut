package gitbutler

import (
	"encoding/json"
	"os"
	"testing"
)

func TestWorkspaceStatusUnmarshal(t *testing.T) {
	raw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}

	var status WorkspaceStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}

	if got := len(status.UnassignedChanges); got != 1 {
		t.Fatalf("unassigned changes = %d, want 1", got)
	}
	branch := status.Stacks[0].Branches[0]
	if branch.BranchStatus.String() != "unpushedCommits" {
		t.Fatalf("branch status = %q", branch.BranchStatus)
	}
	if branch.MergeStatus.String() != "conflicted" {
		t.Fatalf("merge status = %q", branch.MergeStatus)
	}
	if branch.CI == nil || branch.CI.OverallConclusion.String() != "success" {
		t.Fatalf("ci was not parsed: %#v", branch.CI)
	}
}

func TestBranchListUnmarshal(t *testing.T) {
	raw, err := os.ReadFile("testdata/branch_list.json")
	if err != nil {
		t.Fatal(err)
	}

	var branches BranchList
	if err := json.Unmarshal(raw, &branches); err != nil {
		t.Fatal(err)
	}

	if got := branches.Branches[0].Name; got != "feature/unapplied" {
		t.Fatalf("branch name = %q", got)
	}
	if branches.Branches[0].CommitsAhead == nil || *branches.Branches[0].CommitsAhead != 3 {
		t.Fatalf("commits ahead was not parsed")
	}
}
