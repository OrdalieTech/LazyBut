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

// Current `but` builds renamed the zz-lane key from `unassignedChanges` to
// `uncommittedChanges`; both must populate UnassignedChanges.
func TestWorkspaceStatusAcceptsUncommittedChangesKey(t *testing.T) {
	raw := []byte(`{"uncommittedChanges":[{"cliId":"yrp","filePath":"a.go","changeType":"added"},{"cliId":"ks","filePath":"b.go","changeType":"modified"}],"stacks":[]}`)
	var status WorkspaceStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if got := len(status.UnassignedChanges); got != 2 {
		t.Fatalf("unassigned changes = %d, want 2", got)
	}
	if status.UnassignedChanges[0].FilePath != "a.go" {
		t.Fatalf("file path = %q", status.UnassignedChanges[0].FilePath)
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

func TestCurrentStatusFields(t *testing.T) {
	var status WorkspaceStatus
	err := json.Unmarshal([]byte(`{"unassignedChanges":[{"filePath":"stale"}],"uncommittedChanges":[],"conflictedFiles":["conflict.txt"],"stacks":[{"branches":[{"ci":{"status":"inProgress","conclusion":"failure"}}]}]}`), &status)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.UnassignedChanges) != 0 || len(status.ConflictedFiles) != 1 {
		t.Fatalf("incorrect changes: %+v", status)
	}
	ci := status.Stacks[0].Branches[0].CI
	if ci.Status != "inProgress" || ci.Conclusion != "failure" {
		t.Fatalf("incorrect CI: %+v", ci)
	}
}
