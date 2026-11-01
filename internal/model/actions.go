package model

import "time"

// Action is a reusable, locally stored sequence assigned to zero or more nodes.
type Action struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Description      string       `json:"description"`
	Steps            []ActionStep `json:"steps"`
	NodeIDs          []string     `json:"node_ids"`
	ConfirmBeforeRun bool         `json:"confirm_before_run"`
}

type ActionStep struct {
	Command        string `json:"command"`
	Name           string `json:"name"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	OnError        string `json:"on_error"`
}

// ActionRun retains the definition and node names as they were at launch.
// It deliberately has no database foreign keys to mutable catalog entries.
type ActionRun struct {
	ID         string             `json:"id"`
	ActionID   string             `json:"action_id"`
	ActionName string             `json:"action_name"`
	NodeID     string             `json:"node_id"`
	NodeName   string             `json:"node_name"`
	Status     string             `json:"status"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt *time.Time         `json:"finished_at"`
	DurationMS int64              `json:"duration_ms"`
	Error      string             `json:"error"`
	Steps      []ActionStepResult `json:"steps"`
}

type ActionStepResult struct {
	Command         string     `json:"command"`
	Name            string     `json:"name"`
	TimeoutSeconds  int        `json:"timeout_seconds"`
	OnError         string     `json:"on_error"`
	Status          string     `json:"status"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	DurationMS      int64      `json:"duration_ms"`
	ExitCode        *int       `json:"exit_code"`
	Stdout          string     `json:"stdout"`
	Stderr          string     `json:"stderr"`
	Error           string     `json:"error"`
	StdoutTruncated bool       `json:"stdout_truncated"`
	StderrTruncated bool       `json:"stderr_truncated"`
}
