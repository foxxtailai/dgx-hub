package api

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dgx-hub/internal/actions"
	"dgx-hub/internal/model"
)

type actionMetrics struct {
	fakeMetrics
	status string
	stale  bool
}

func (m actionMetrics) Get(string) model.Metrics {
	return model.Metrics{Status: m.status, Stale: m.stale}
}

type apiActionExecutor struct {
	calls   atomic.Int32
	started chan struct{}
}

func (e *apiActionExecutor) Execute(ctx context.Context, node model.Node, step model.ActionStep, stdout, stderr io.Writer) (*int, error) {
	e.calls.Add(1)
	io.WriteString(stdout, "live output from "+node.Name)
	io.WriteString(stderr, "diagnostic")
	if e.started != nil {
		select {
		case e.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	zero := 0
	return &zero, nil
}

func actionSetup(t *testing.T, blocking bool) (*Server, model.Node, *apiActionExecutor) {
	t.Helper()
	s, _ := setup(t)
	s.actions.Close()
	exec := &apiActionExecutor{}
	if blocking {
		exec.started = make(chan struct{}, 1)
	}
	s.actions = actions.New(s.store, exec)
	t.Cleanup(s.Close)
	s.metrics = actionMetrics{status: "online"}
	n, err := s.store.SaveNode(model.Node{Name: "Mock node", SSHTarget: "mock.invalid"}, true)
	if err != nil {
		t.Fatal(err)
	}
	return s, n, exec
}

func apiAction(t *testing.T, s *Server, node model.Node, confirm bool) model.Action {
	t.Helper()
	body, _ := json.Marshal(model.Action{Name: "Check test", NodeIDs: []string{node.ID}, ConfirmBeforeRun: confirm,
		Steps: []model.ActionStep{{Command: "echo test"}, {Command: "echo second"}}})
	w := request(s, "POST", "/api/actions", string(body), true)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var a model.Action
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func waitAPIRun(t *testing.T, s *Server, id string) model.ActionRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w := request(s, "GET", "/api/action-runs/"+id, "", false)
		var run model.ActionRun
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		if run.Status != "running" {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return model.ActionRun{}
}

func TestActionAPIExplicitRunAndHistory(t *testing.T) {
	s, n, exec := actionSetup(t, false)
	a := apiAction(t, s, n, true)
	for _, path := range []string{"/api/actions", "/api/actions/" + a.ID, "/api/action-runs?node_id=" + n.ID} {
		if w := request(s, "GET", path, "", false); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if exec.calls.Load() != 0 {
		t.Fatal("read or save executed commands")
	}
	path := "/api/nodes/" + n.ID + "/actions/" + a.ID + "/runs"
	for _, tt := range []struct {
		method, body string
		csrf         bool
		want         int
	}{
		{"GET", "", false, 404},
		{"POST", `{"confirmed":true}`, false, 403},
		{"POST", `{"confirmed":false}`, true, 409},
	} {
		w := request(s, tt.method, path, tt.body, tt.csrf)
		if w.Code != tt.want {
			t.Fatalf("%s: %d %s", tt.method, w.Code, w.Body.String())
		}
	}
	if exec.calls.Load() != 0 {
		t.Fatal("rejected launch executed commands")
	}
	w := request(s, "POST", path, `{"confirmed":true}`, true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var run model.ActionRun
	json.Unmarshal(w.Body.Bytes(), &run)
	run = waitAPIRun(t, s, run.ID)
	if run.Status != "success" || len(run.Steps) != 2 || run.Steps[0].Stdout != "live output from Mock node" || run.Steps[0].Stderr != "diagnostic" || exec.calls.Load() != 2 {
		t.Fatalf("unexpected execution: %+v", run)
	}
	w = request(s, "GET", "/api/action-runs?node_id="+n.ID+"&action_id="+a.ID, "", false)
	var history struct {
		Runs []model.ActionRun `json:"runs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil || w.Code != 200 || len(history.Runs) != 1 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if w = request(s, "DELETE", "/api/actions/"+a.ID, "", true); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(s, "GET", "/api/action-runs/"+run.ID, "", false); w.Code != 200 {
		t.Fatal("deletion lost history")
	}
}

func TestActionAPIAvailabilityBusyCancelAndNodeEdit(t *testing.T) {
	s, n, exec := actionSetup(t, true)
	a := apiAction(t, s, n, false)
	path := "/api/nodes/" + n.ID + "/actions/" + a.ID + "/runs"
	for _, m := range []actionMetrics{{status: "offline"}, {status: "connecting"}, {status: "error"}, {status: "online", stale: true}} {
		s.metrics = m
		if w := request(s, "POST", path, `{}`, true); w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	s.metrics = actionMetrics{status: "online"}
	w := request(s, "POST", path, `{}`, true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var run model.ActionRun
	json.Unmarshal(w.Body.Bytes(), &run)
	select {
	case <-exec.started:
	case <-time.After(time.Second):
		t.Fatal("mock not started")
	}
	if w = request(s, "POST", path, `{}`, true); w.Code != 409 {
		t.Fatal("duplicate accepted", w.Code, w.Body.String())
	}
	w = request(s, "GET", "/api/action-runs/"+run.ID, "", false)
	if !strings.Contains(w.Body.String(), "live output") {
		t.Fatal("missing live output", w.Body.String())
	}
	w = request(s, "POST", "/api/action-runs/"+run.ID+"/cancel", `{}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if done := waitAPIRun(t, s, run.ID); done.Status != "cancelled" {
		t.Fatal(done.Status)
	}
	w = request(s, "POST", path, `{}`, true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &run)
	select {
	case <-exec.started:
	case <-time.After(time.Second):
		t.Fatal("second mock not started")
	}
	w = request(s, "PATCH", "/api/nodes/"+n.ID, `{"name":"Renamed"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if done := waitAPIRun(t, s, run.ID); done.Status != "cancelled" {
		t.Fatal("node edit did not cancel action")
	}
}

func TestActionAPISharedAssignmentAndValidation(t *testing.T) {
	s, n, exec := actionSetup(t, false)
	a := apiAction(t, s, n, false)
	n2, err := s.store.SaveNode(model.Node{Name: "Second", SSHTarget: "second.invalid"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if w := request(s, "POST", "/api/nodes/"+n2.ID+"/actions/"+a.ID+"/runs", `{}`, true); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.NodeIDs = append(a.NodeIDs, n2.ID)
	a.Name = "Updated"
	body, _ := json.Marshal(a)
	w := request(s, "PUT", "/api/actions/"+a.ID, string(body), true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if exec.calls.Load() != 0 {
		t.Fatal("edit started action")
	}
	var saved model.Action
	json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.Name != "Updated" || len(saved.NodeIDs) != 2 {
		t.Fatal(saved)
	}
	for _, body := range []string{
		`{"name":"Missing steps"}`, `{"name":"bad","steps":[{"command":"true","timeout_seconds":-1}]}`,
		`{"name":"bad","steps":[{"command":"true"}],"node_ids":["missing"]}`,
		`{"name":"bad","steps":[{"command":"true"}],"unexpected":true}`,
	} {
		w = request(s, "POST", "/api/actions", body, true)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Multi-step commands are permitted beyond the smaller ordinary config limit.
	a.Steps = []model.ActionStep{{Command: strings.Repeat("x", 16000)}, {Command: strings.Repeat("y", 16000)}, {Command: "true"}}
	body, _ = json.Marshal(a)
	w = request(s, "PUT", "/api/actions/"+a.ID, string(body), true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
