package validation

import (
	"strings"
	"testing"

	"dgx-hub/internal/model"
)

func TestActionDefaultsAndValidation(t *testing.T) {
	a := model.Action{Name: " Check ", Steps: []model.ActionStep{{Command: "printf 'a'\nprintf 'b'"}}, NodeIDs: []string{"one", "one", "two"}}
	if err := Action(&a); err != nil {
		t.Fatal(err)
	}
	if a.Name != "Check" || a.Steps[0].TimeoutSeconds != 60 || a.Steps[0].OnError != "stop" || len(a.NodeIDs) != 2 {
		t.Fatalf("defaults not applied: %+v", a)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*model.Action)
	}{
		{"empty name", func(a *model.Action) { a.Name = " " }},
		{"control name", func(a *model.Action) { a.Name = "bad\nname" }},
		{"no steps", func(a *model.Action) { a.Steps = nil }},
		{"too many steps", func(a *model.Action) { a.Steps = make([]model.ActionStep, 51) }},
		{"empty command", func(a *model.Action) { a.Steps[0].Command = " \n" }},
		{"NUL command", func(a *model.Action) { a.Steps[0].Command = "echo\x00" }},
		{"large command", func(a *model.Action) { a.Steps[0].Command = strings.Repeat("a", 16385) }},
		{"negative timeout", func(a *model.Action) { a.Steps[0].TimeoutSeconds = -1 }},
		{"large timeout", func(a *model.Action) { a.Steps[0].TimeoutSeconds = 86401 }},
		{"bad policy", func(a *model.Action) { a.Steps[0].OnError = "retry" }},
		{"empty assignment", func(a *model.Action) { a.NodeIDs = []string{""} }},
		{"large description", func(a *model.Action) { a.Description = strings.Repeat("x", 2001) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := model.Action{Name: "Test", Steps: []model.ActionStep{{Command: "true"}}}
			tt.mutate(&a)
			if err := Action(&a); err == nil {
				t.Fatal("accepted invalid definition")
			}
		})
	}
}
