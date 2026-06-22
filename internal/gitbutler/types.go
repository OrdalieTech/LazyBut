package gitbutler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type WorkspaceStatus struct {
	UnassignedChanges []FileChange  `json:"unassignedChanges"`
	Stacks            []Stack       `json:"stacks"`
	MergeBase         Commit        `json:"mergeBase"`
	UpstreamState     UpstreamState `json:"upstreamState"`
}

type UpstreamState struct {
	Behind          int      `json:"behind"`
	LatestCommit    Commit   `json:"latestCommit"`
	LastFetched     *string  `json:"lastFetched"`
	UpstreamCommits []Commit `json:"upstreamCommits"`
}

type Stack struct {
	CLIID           string       `json:"cliId"`
	AssignedChanges []FileChange `json:"assignedChanges"`
	Branches        []Branch     `json:"branches"`
}

type Branch struct {
	CLIID           string     `json:"cliId"`
	Name            string     `json:"name"`
	Commits         []Commit   `json:"commits"`
	UpstreamCommits []Commit   `json:"upstreamCommits"`
	BranchStatus    StatusText `json:"branchStatus"`
	ReviewID        *string    `json:"reviewId"`
	ReviewURL       *string    `json:"reviewUrl"`
	CI              *CI        `json:"ci"`
	MergeStatus     StatusText `json:"mergeStatus"`
}

type CI struct {
	OverallConclusion StatusText `json:"overallConclusion"`
	Pending           []string   `json:"pendingCheckTitles"`
	Passing           []string   `json:"passingCheckTitles"`
	Failing           []string   `json:"failingCheckTitles"`
}

type Commit struct {
	CLIID       string       `json:"cliId"`
	CommitID    string       `json:"commitId"`
	CreatedAt   string       `json:"createdAt"`
	Message     string       `json:"message"`
	AuthorName  string       `json:"authorName"`
	AuthorEmail string       `json:"authorEmail"`
	Conflicted  *bool        `json:"conflicted"`
	ReviewID    *string      `json:"reviewId"`
	Changes     []FileChange `json:"changes"`
}

type FileChange struct {
	CLIID      string     `json:"cliId"`
	FilePath   string     `json:"filePath"`
	ChangeType StatusText `json:"changeType"`
}

type BranchList struct {
	AppliedStacks []BranchListStack `json:"appliedStacks"`
	Branches      []BranchListItem  `json:"branches"`
	MoreBranches  *int              `json:"moreBranches"`
}

type BranchListStack struct {
	ID    *string          `json:"id"`
	Heads []BranchListHead `json:"heads"`
}

type BranchListHead struct {
	Name          string   `json:"name"`
	Reviews       []Review `json:"reviews"`
	LastCommitAt  uint64   `json:"lastCommitAt"`
	CommitsAhead  *int     `json:"commitsAhead"`
	LastAuthor    Author   `json:"lastAuthor"`
	MergesCleanly *bool    `json:"mergesCleanly"`
}

type BranchListItem struct {
	Name          string   `json:"name"`
	Reviews       []Review `json:"reviews"`
	HasLocal      bool     `json:"hasLocal"`
	LastCommitAt  uint64   `json:"lastCommitAt"`
	CommitsAhead  *int     `json:"commitsAhead"`
	LastAuthor    Author   `json:"lastAuthor"`
	MergesCleanly *bool    `json:"mergesCleanly"`
}

type Author struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

// OplogEntry is one row in `but oplog list -j`. CreatedAt is a unix millisecond
// timestamp (GitButler emits it as a number, not RFC3339).
type OplogEntry struct {
	ID        string       `json:"id"`
	CreatedAt int64        `json:"createdAt"`
	Details   OplogDetails `json:"details"`
}

type OplogDetails struct {
	Operation string  `json:"operation"`
	Title     string  `json:"title"`
	Body      *string `json:"body"`
}

type Review struct {
	Number uint64 `json:"number"`
	URL    string `json:"url"`
}

type StatusAfter struct {
	Result      json.RawMessage  `json:"result"`
	Status      *WorkspaceStatus `json:"status"`
	StatusError *CLIError        `json:"status_error"`
}

func (s *StatusAfter) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Result      json.RawMessage `json:"result"`
		Status      json.RawMessage `json:"status"`
		StatusError *CLIError       `json:"status_error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	s.Result = decoded.Result
	s.Status = nil
	s.StatusError = decoded.StatusError

	statusRaw := bytes.TrimSpace(decoded.Status)
	if len(statusRaw) == 0 || bytes.Equal(statusRaw, []byte("null")) {
		return nil
	}
	if statusRaw[0] == '"' {
		var text string
		if err := json.Unmarshal(statusRaw, &text); err != nil {
			return fmt.Errorf("parse status string: %w", err)
		}
		return nil
	}
	if statusRaw[0] != '{' {
		return fmt.Errorf("status field has unsupported JSON value")
	}
	var status WorkspaceStatus
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		return fmt.Errorf("parse structured status: %w", err)
	}
	s.Status = &status
	return nil
}

type CLIError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (e CLIError) Error() string {
	parts := []string{}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	if e.Hint != "" {
		parts = append(parts, e.Hint)
	}
	if len(parts) == 0 {
		return "gitbutler command failed"
	}
	return strings.Join(parts, ": ")
}

type StatusText string

func (s *StatusText) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		*s = ""
		return nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		*s = StatusText(value)
		return nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	for key := range value {
		*s = StatusText(key)
		return nil
	}
	*s = ""
	return nil
}

func (s StatusText) String() string {
	return string(s)
}
