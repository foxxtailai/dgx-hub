package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"dgx-hub/internal/model"
)

func TestActionCatalogAssignmentAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dgx-hub.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	elly, err := s.SaveNode(model.Node{Name: "Elly", SSHTarget: "elly"}, true)
	if err != nil {
		t.Fatal(err)
	}
	evie, err := s.SaveNode(model.Node{Name: "Evie", SSHTarget: "evie"}, true)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.SaveAction(model.Action{Name: "Check Samba", Description: "Check service", ConfirmBeforeRun: true,
		NodeIDs: []string{elly.ID, evie.ID}, Steps: []model.ActionStep{
			{Name: "Service", Command: "systemctl is-active smbd", TimeoutSeconds: 60, OnError: "stop"},
			{Command: "testparm -s", TimeoutSeconds: 30, OnError: "continue"},
		}}, true)
	if err != nil || a.ID == "" {
		t.Fatalf("create: %+v %v", a, err)
	}
	a.Name, a.Description = "Samba checks", "Updated description"
	a.Steps[0], a.Steps[1] = a.Steps[1], a.Steps[0]
	if _, err = s.SaveAction(a, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Action(a.ID)
	slices.Sort(a.NodeIDs)
	if err != nil || !reflect.DeepEqual(loaded, a) {
		t.Fatalf("persistence/order: got %+v want %+v err=%v", loaded, a, err)
	}
	all, err := s.Actions()
	if err != nil || len(all) != 1 || !reflect.DeepEqual(all[0], a) {
		t.Fatalf("catalog: %+v %v", all, err)
	}
	if err = s.DeleteNode(elly.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.Action(a.ID)
	if err != nil || !reflect.DeepEqual(loaded.NodeIDs, []string{evie.ID}) {
		t.Fatalf("delete node should only remove its assignment: %+v %v", loaded, err)
	}
	if err = s.DeleteAction(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Action(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted action: %v", err)
	}
	if err = s.DeleteAction(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing deletion: %v", err)
	}
	if _, err = s.Node(evie.ID); err != nil {
		t.Fatalf("action delete removed unrelated node: %v", err)
	}
}

func TestActionSaveIsAtomic(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "dgx-hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.SaveNode(model.Node{Name: "Elly", SSHTarget: "elly"}, true)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.SaveAction(model.Action{Name: "Original", NodeIDs: []string{n.ID}, Steps: []model.ActionStep{{Command: "true", TimeoutSeconds: 60, OnError: "stop"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	bad := a
	bad.Name, bad.NodeIDs = "Must roll back", []string{n.ID, "missing"}
	if _, err = s.SaveAction(bad, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid assigned node: %v", err)
	}
	loaded, err := s.Action(a.ID)
	if err != nil || !reflect.DeepEqual(loaded, a) {
		t.Fatalf("failed save changed existing action: %+v %v", loaded, err)
	}
	if _, err = s.SaveAction(bad, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid create: %v", err)
	}
	all, err := s.Actions()
	if err != nil || len(all) != 1 {
		t.Fatalf("invalid create left rows: %+v %v", all, err)
	}
	bad.NodeIDs = []string{n.ID, n.ID}
	if _, err = s.SaveAction(bad, false); err == nil {
		t.Fatal("duplicate assignment accepted")
	}
	bad.ID = "missing"
	if _, err = s.SaveAction(bad, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing update: %v", err)
	}
}

func TestActionHistorySnapshotAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dgx-hub.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	n, err := s.SaveNode(model.Node{Name: "Elly", SSHTarget: "elly"}, true)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.SaveAction(model.Action{Name: "Check", NodeIDs: []string{n.ID}, Steps: []model.ActionStep{{Command: "printf hello", TimeoutSeconds: 60, OnError: "stop"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-5 * time.Second)
	finish := start.Add(time.Second)
	code := 4
	run := model.ActionRun{ID: "history", ActionID: a.ID, ActionName: a.Name, NodeID: n.ID, NodeName: n.Name, Status: "failed", StartedAt: start,
		FinishedAt: &finish, DurationMS: 1000, Error: "Step failed", Steps: []model.ActionStepResult{{Command: a.Steps[0].Command, Name: "Snapshot", OnError: "stop", TimeoutSeconds: 60,
			Status: "failed", StartedAt: &start, FinishedAt: &finish, DurationMS: 1000, ExitCode: &code, Stdout: "hello", Stderr: "failure", Error: "exit status 4", StderrTruncated: true}}}
	if err = s.SaveActionRun(run); err != nil {
		t.Fatal(err)
	}
	active := model.ActionRun{ID: "interrupted", ActionID: a.ID, ActionName: a.Name, NodeID: n.ID, NodeName: n.Name, Status: "running", StartedAt: start,
		Steps: []model.ActionStepResult{{Status: "success", ExitCode: new(int)}, {Status: "running", StartedAt: &start, Stdout: "partial"}, {Status: "pending"}}}
	if err = s.SaveActionRun(active); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAction(a.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.ActionRun(run.ID)
	if err != nil || !reflect.DeepEqual(loaded, run) {
		t.Fatalf("history snapshot after catalog deletion and restart: %+v %v", loaded, err)
	}
	loaded, err = s.ActionRun(active.ID)
	if err != nil || loaded.Status != "cancelled" || loaded.FinishedAt == nil || loaded.DurationMS < 5000 || loaded.Error == "" {
		t.Fatalf("interruption not recorded: %+v %v", loaded, err)
	}
	if loaded.Steps[0].Status != "success" || loaded.Steps[1].Status != "cancelled" || loaded.Steps[1].Stdout != "partial" || loaded.Steps[1].DurationMS < 5000 || loaded.Steps[2].Status != "cancelled" {
		t.Fatalf("recovered steps: %+v", loaded.Steps)
	}
	runs, err := s.ActionRuns(n.ID, a.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("filtered history: %+v %v", runs, err)
	}
	runs, err = s.ActionRuns("unrelated", "")
	if err != nil || len(runs) != 0 {
		t.Fatalf("history filter: %+v %v", runs, err)
	}
	if _, err = s.ActionRun("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
}

func TestActionHistoryRetentionAndUpsert(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "dgx-hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now().UTC()
	active := model.ActionRun{ID: "active", Status: "running", StartedAt: start.Add(-time.Hour)}
	if err = s.SaveActionRun(active); err != nil {
		t.Fatal(err)
	}
	for i := range ActionHistoryLimit + 5 {
		run := model.ActionRun{ID: fmt.Sprintf("run-%03d", i), Status: "success", StartedAt: start.Add(time.Duration(i) * time.Second)}
		if err = s.SaveActionRun(run); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.ActionRuns("", "")
	if err != nil || len(runs) != ActionHistoryLimit || runs[0].ID != "run-104" || runs[len(runs)-1].ID != "run-005" {
		t.Fatalf("newest history: len=%d %+v %v", len(runs), runs, err)
	}
	if _, err = s.ActionRun("run-004"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old history not pruned: %v", err)
	}
	if _, err = s.ActionRun(active.ID); err != nil {
		t.Fatalf("active history pruned: %v", err)
	}
	latest, err := s.ActionRun("run-104")
	if err != nil {
		t.Fatal(err)
	}
	latest.Error = "updated result"
	if err = s.SaveActionRun(latest); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.ActionRun(latest.ID)
	if err != nil || loaded.Error != latest.Error {
		t.Fatalf("run upsert failed: %+v %v", loaded, err)
	}
}

func TestActionRunSummariesOmitOutputAndFilter(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "dgx-hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for i := range 4 {
		run := model.ActionRun{ID: fmt.Sprintf("summary-%d", i), ActionID: "action", ActionName: "Snapshot", NodeID: "node", NodeName: "Node snapshot", Status: "success",
			StartedAt: start.Add(time.Duration(i) * time.Second), DurationMS: 1000, Steps: []model.ActionStepResult{{Command: "printf output", Stdout: "output", Stderr: "diagnostics", Status: "success"}}}
		if i == 0 {
			run.NodeID = "other-node"
		}
		if i == 1 {
			run.ActionID = "other-action"
		}
		if err = s.SaveActionRun(run); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.ActionRunSummaries("node", "action")
	if err != nil || len(runs) != 2 || runs[0].ID != "summary-3" || runs[1].ID != "summary-2" {
		t.Fatalf("summary filter/order: %+v %v", runs, err)
	}
	for _, run := range runs {
		if run.Steps == nil || len(run.Steps) != 0 || run.ActionName != "Snapshot" || run.NodeName != "Node snapshot" || run.DurationMS != 1000 {
			t.Fatalf("unexpected summary: %+v", run)
		}
	}
	detail, err := s.ActionRun(runs[0].ID)
	if err != nil || len(detail.Steps) != 1 || detail.Steps[0].Stdout != "output" || detail.Steps[0].Stderr != "diagnostics" {
		t.Fatalf("summary damaged full details: %+v %v", detail, err)
	}
	all, err := s.ActionRuns("node", "action")
	if err != nil || len(all) != 2 || len(all[0].Steps) != 1 {
		t.Fatalf("full history list: %+v %v", all, err)
	}
}
