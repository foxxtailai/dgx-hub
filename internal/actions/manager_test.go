package actions

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"dgx-hub/internal/model"
	"dgx-hub/internal/storage"
)

type memoryStore struct {
	mu       sync.Mutex
	runs     map[string]model.ActionRun
	writes   int
	failFrom int
}

func (s *memoryStore) SaveActionRun(run model.ActionRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.failFrom > 0 && s.writes >= s.failFrom {
		return errors.New("disk full")
	}
	if s.runs == nil {
		s.runs = map[string]model.ActionRun{}
	}
	s.runs[run.ID] = copyRun(run)
	return nil
}

func (s *memoryStore) ActionRun(id string) (model.ActionRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return run, storage.ErrNotFound
	}
	return copyRun(run), nil
}

type executorFunc func(context.Context, model.Node, model.ActionStep, io.Writer, io.Writer) (*int, error)

func (f executorFunc) Execute(ctx context.Context, node model.Node, step model.ActionStep, stdout, stderr io.Writer) (*int, error) {
	return f(ctx, node, step, stdout, stderr)
}

func code(value int) *int { return &value }

func actionWith(policy string, commands ...string) model.Action {
	a := model.Action{ID: "a", Name: "Check node", NodeIDs: []string{"n"}}
	for _, command := range commands {
		a.Steps = append(a.Steps, model.ActionStep{Command: command, Name: command, TimeoutSeconds: 1, OnError: policy})
	}
	return a
}

func waitDone(t *testing.T, m *Manager, id string) model.ActionRun {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		_, active := m.active[id]
		m.mu.Unlock()
		if !active {
			run, err := m.Run(id)
			if err != nil {
				t.Fatal(err)
			}
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("action did not complete")
	return model.ActionRun{}
}

func TestSequenceOutputExitAndHistory(t *testing.T) {
	store := &memoryStore{}
	var calls []string
	m := New(store, executorFunc(func(_ context.Context, node model.Node, step model.ActionStep, out, stderr io.Writer) (*int, error) {
		if node.ID != "n" || node.SSHTarget != "chosen-target" {
			t.Errorf("wrong SSH node: %+v", node)
		}
		calls = append(calls, step.Command)
		_, _ = io.WriteString(out, "output "+step.Command+"\n")
		_, _ = io.WriteString(stderr, "warning\n")
		return code(0), nil
	}))
	defer m.Close()
	run, err := m.Start(model.Node{ID: "n", Name: "Node at launch", SSHTarget: "chosen-target"}, actionWith("stop", "one", "two", "three"))
	if err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, m, run.ID)
	if strings.Join(calls, ",") != "one,two,three" || got.Status != "success" || got.ActionName != "Check node" || got.NodeName != "Node at launch" || got.FinishedAt == nil {
		t.Fatalf("unexpected sequence: %+v, %v", got, calls)
	}
	for _, step := range got.Steps {
		if step.Status != "success" || step.ExitCode == nil || *step.ExitCode != 0 || step.Stdout != "output "+step.Command+"\n" || step.Stderr != "warning\n" || step.StartedAt == nil || step.FinishedAt == nil {
			t.Fatalf("incomplete step: %+v", step)
		}
	}
	persisted, err := store.ActionRun(run.ID)
	if err != nil || persisted.Status != "success" || persisted.Steps[2].Stdout != "output three\n" {
		t.Fatalf("history missing: %+v %v", persisted, err)
	}
	// External consumers cannot mutate active or completed manager snapshots.
	*got.Steps[0].ExitCode = 99
	again, _ := m.Run(run.ID)
	if *again.Steps[0].ExitCode != 0 {
		t.Fatal("run snapshot aliases manager state")
	}
}

func TestFailurePoliciesAndDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name, policy string
		exit         *int
		wantCalls    int
	}{
		{"stop", "stop", code(7), 1},
		{"continue", "continue", code(7), 2},
		{"disconnect", "continue", code(255), 1},
		{"setup failure", "continue", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := New(&memoryStore{}, executorFunc(func(context.Context, model.Node, model.ActionStep, io.Writer, io.Writer) (*int, error) {
				calls++
				if calls == 1 {
					return tc.exit, errors.New("failure")
				}
				return code(0), nil
			}))
			defer m.Close()
			run, err := m.Start(model.Node{ID: "n"}, actionWith(tc.policy, "fail", "after"))
			if err != nil {
				t.Fatal(err)
			}
			got := waitDone(t, m, run.ID)
			if got.Status != "failed" || got.Steps[0].Status != "failed" || calls != tc.wantCalls {
				t.Fatalf("wrong failure policy: %+v, calls=%d", got, calls)
			}
			if tc.wantCalls == 1 && got.Steps[1].Status != "skipped" || tc.wantCalls == 2 && got.Steps[1].Status != "success" {
				t.Fatal("wrong following-step status")
			}
		})
	}
}

func TestTimeoutStopsLocalOperationAndCanContinue(t *testing.T) {
	for _, policy := range []string{"stop", "continue"} {
		t.Run(policy, func(t *testing.T) {
			calls := 0
			m := New(&memoryStore{}, executorFunc(func(ctx context.Context, _ model.Node, _ model.ActionStep, _, _ io.Writer) (*int, error) {
				calls++
				if calls == 1 {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return code(0), nil
			}))
			defer m.Close()
			run, err := m.Start(model.Node{ID: "n"}, actionWith(policy, "wait", "after"))
			if err != nil {
				t.Fatal(err)
			}
			got := waitDone(t, m, run.ID)
			if got.Status != "failed" || !strings.Contains(got.Steps[0].Error, "timed out") || got.Steps[0].DurationMS < 900 || got.Steps[0].ExitCode != nil {
				t.Fatalf("timeout missing: %+v", got)
			}
			if policy == "stop" && calls != 1 || policy == "continue" && calls != 2 {
				t.Fatalf("wrong calls after timeout: %d", calls)
			}
		})
	}
}

func TestStreamingBusyCancellationAndNodeRelease(t *testing.T) {
	started := make(chan struct{}, 3)
	m := New(&memoryStore{}, executorFunc(func(ctx context.Context, _ model.Node, _ model.ActionStep, out, _ io.Writer) (*int, error) {
		_, _ = io.WriteString(out, "live output\n")
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	defer m.Close()
	node, action := model.Node{ID: "n"}, actionWith("stop", "wait", "after")
	run, err := m.Start(node, action)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	live, _ := m.Run(run.ID)
	if live.Steps[0].Stdout != "live output\n" || live.Steps[0].Status != "running" {
		t.Fatalf("not streaming: %+v", live)
	}
	if _, err := m.Start(node, action); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate run not rejected: %v", err)
	}
	other, err := m.Start(model.Node{ID: "other"}, action)
	if err != nil {
		t.Fatalf("different nodes blocked: %v", err)
	}
	<-started
	if err := m.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, m, run.ID)
	if got.Status != "cancelled" || got.Steps[0].Status != "cancelled" || got.Steps[1].Status != "skipped" {
		t.Fatalf("wrong cancellation: %+v", got)
	}
	next, err := m.Start(node, action)
	if err != nil {
		t.Fatalf("node lock leaked: %v", err)
	}
	<-started
	m.CancelNode(node.ID)
	_ = waitDone(t, m, next.ID)
	m.Close()
	if waitDone(t, m, other.ID).Status != "cancelled" {
		t.Fatal("shutdown did not cancel other node")
	}
	if _, err := m.Start(node, action); !errors.Is(err, ErrClosed) {
		t.Fatal("shutdown accepted a new run")
	}
}

func TestOutputLimitsStillDrainWriters(t *testing.T) {
	const streams = 20
	m := New(&memoryStore{}, executorFunc(func(_ context.Context, _ model.Node, _ model.ActionStep, out, stderr io.Writer) (*int, error) {
		p := strings.Repeat("x", MaxStreamBytes+32)
		if n, err := io.WriteString(out, p); n != len(p) || err != nil {
			t.Errorf("stdout not drained: %d %v", n, err)
		}
		_, _ = io.WriteString(stderr, p)
		return code(0), nil
	}))
	defer m.Close()
	commands := make([]string, streams)
	for i := range commands {
		commands[i] = "output"
	}
	run, err := m.Start(model.Node{ID: "n"}, actionWith("stop", commands...))
	if err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, m, run.ID)
	total := 0
	for _, step := range got.Steps {
		if len(step.Stdout) > MaxStreamBytes || len(step.Stderr) > MaxStreamBytes || !step.StdoutTruncated || !step.StderrTruncated {
			t.Fatal("stream limit or truncation marker missing")
		}
		total += len(step.Stdout) + len(step.Stderr)
	}
	if total != MaxRunBytes || got.Status != "success" {
		t.Fatalf("wrong total output limit: %d, %s", total, got.Status)
	}
}

func TestPersistenceFailuresPreventCommandsAndReleaseNode(t *testing.T) {
	for _, failFrom := range []int{1, 2, 3, 5, 6} {
		t.Run(string(rune('0'+failFrom)), func(t *testing.T) {
			store := &memoryStore{failFrom: failFrom}
			calls := 0
			m := New(store, executorFunc(func(context.Context, model.Node, model.ActionStep, io.Writer, io.Writer) (*int, error) {
				calls++
				return code(0), nil
			}))
			defer m.Close()
			run, err := m.Start(model.Node{ID: "n"}, actionWith("stop", "one", "two"))
			if failFrom == 1 {
				if err == nil || calls != 0 {
					t.Fatalf("initial history failure ran a command: %v %d", err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := waitDone(t, m, run.ID)
			if got.Status != "failed" || !strings.Contains(got.Error, "disk full") || failFrom == 2 && calls != 0 || failFrom == 3 && calls != 1 {
				t.Fatalf("history failure not surfaced/stopped: %+v calls=%d", got, calls)
			}
			m.mu.Lock()
			locked := m.nodes["n"] != ""
			m.mu.Unlock()
			if locked {
				t.Fatal("failed save leaked node lock")
			}
		})
	}
}
