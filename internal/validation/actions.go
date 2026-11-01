package validation

import (
	"errors"
	"fmt"
	"strings"

	"dgx-hub/internal/model"
)

// Action validates definitions only; saving or opening one never executes it.
func Action(a *model.Action) error {
	a.Name = strings.TrimSpace(a.Name)
	a.Description = strings.TrimSpace(a.Description)
	if a.Name == "" || !cleanText(a.Name, 100) {
		return errors.New("action name must contain 1–100 characters without control characters")
	}
	if len(a.Description) > 2000 || strings.ContainsRune(a.Description, 0) {
		return errors.New("action description must contain at most 2000 characters without NUL")
	}
	if len(a.Steps) == 0 || len(a.Steps) > 50 {
		return errors.New("an action must contain 1–50 steps")
	}
	for i := range a.Steps {
		step := &a.Steps[i]
		step.Name = strings.TrimSpace(step.Name)
		if !cleanText(step.Name, 100) {
			return fmt.Errorf("step %d: name must contain at most 100 characters without control characters", i+1)
		}
		if strings.TrimSpace(step.Command) == "" || len(step.Command) > 16384 || strings.ContainsRune(step.Command, 0) {
			return fmt.Errorf("step %d: command must contain 1–16384 characters without NUL", i+1)
		}
		if step.TimeoutSeconds == 0 {
			step.TimeoutSeconds = 60
		}
		if step.TimeoutSeconds < 1 || step.TimeoutSeconds > 86400 {
			return fmt.Errorf("step %d: timeout must be between 1 and 86400 seconds", i+1)
		}
		if step.OnError == "" {
			step.OnError = "stop"
		}
		if step.OnError != "stop" && step.OnError != "continue" {
			return fmt.Errorf("step %d: error policy must be stop or continue", i+1)
		}
	}
	if len(a.NodeIDs) > 500 {
		return errors.New("an action can be assigned to at most 500 nodes")
	}
	seen := make(map[string]bool)
	ids := make([]string, 0, len(a.NodeIDs))
	for _, id := range a.NodeIDs {
		if id == "" || len(id) > 128 {
			return errors.New("invalid assigned node ID")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	a.NodeIDs = ids
	return nil
}
