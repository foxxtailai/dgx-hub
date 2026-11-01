// Package actions runs explicitly requested SSH command sequences. It shares
// system SSH credentials with telemetry and tunnels, while owning only its own
// cancellable SSH processes.
package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"dgx-hub/internal/model"
	"dgx-hub/internal/sshutil"
)

var (
	ErrBusy   = errors.New("an action is already running on this node")
	ErrClosed = errors.New("DGX Hub is shutting down")
)

const (
	MaxStreamBytes = 64 * 1024
	MaxRunBytes    = 1024 * 1024
)

type Store interface {
	SaveActionRun(model.ActionRun) error
	ActionRun(string) (model.ActionRun, error)
}

// Executor must honor context cancellation and return only after its output
// writers are no longer in use. A nil exit code means no remote exit is known.
type Executor interface {
	Execute(context.Context, model.Node, model.ActionStep, io.Writer, io.Writer) (*int, error)
}

type SSHExecutor struct{}

func (SSHExecutor) Execute(ctx context.Context, node model.Node, step model.ActionStep, stdout, stderr io.Writer) (*int, error) {
	return sshutil.RunAction(ctx, node.SSHTarget, step.Command, stdout, stderr)
}

type execution struct {
	run    model.ActionRun
	cancel context.CancelFunc
	bytes  int
}

type Manager struct {
	mu        sync.Mutex
	store     Store
	executor  Executor
	active    map[string]*execution
	nodes     map[string]string
	completed map[string]model.ActionRun
	recent    []string
	closed    bool
	wg        sync.WaitGroup
}

func New(store Store, executor Executor) *Manager {
	if executor == nil {
		executor = SSHExecutor{}
	}
	return &Manager{store: store, executor: executor, active: map[string]*execution{}, nodes: map[string]string{}, completed: map[string]model.ActionRun{}}
}

func (m *Manager) Start(node model.Node, action model.Action) (model.ActionRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return model.ActionRun{}, ErrClosed
	}
	if m.nodes[node.ID] != "" {
		return model.ActionRun{}, ErrBusy
	}
	if node.ID == "" || action.ID == "" || len(action.Steps) == 0 {
		return model.ActionRun{}, errors.New("a saved action, node, and at least one step are required")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return model.ActionRun{}, err
	}
	run := model.ActionRun{ID: hex.EncodeToString(id[:]), ActionID: action.ID, ActionName: action.Name, NodeID: node.ID, NodeName: node.Name, Status: "running", StartedAt: time.Now().UTC(), Steps: make([]model.ActionStepResult, len(action.Steps))}
	for i, step := range action.Steps {
		if step.TimeoutSeconds < 1 || strings.TrimSpace(step.Command) == "" || (step.OnError != "stop" && step.OnError != "continue") {
			return model.ActionRun{}, errors.New("each step needs a command, positive timeout, and stop or continue error policy")
		}
		run.Steps[i] = model.ActionStepResult{Command: step.Command, Name: step.Name, TimeoutSeconds: step.TimeoutSeconds, OnError: step.OnError, Status: "pending"}
	}
	// Persist before scheduling any SSH work. If history cannot be written, no
	// administrative command is started and the node remains available.
	if err := m.store.SaveActionRun(run); err != nil {
		return model.ActionRun{}, fmt.Errorf("save action run before execution: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	item := &execution{run: run, cancel: cancel}
	m.active[run.ID], m.nodes[node.ID] = item, run.ID
	m.wg.Add(1)
	go m.execute(ctx, node, item)
	return copyRun(run), nil
}

func (m *Manager) Run(id string) (model.ActionRun, error) {
	m.mu.Lock()
	if item := m.active[id]; item != nil {
		run := copyRun(item.run)
		m.mu.Unlock()
		return run, nil
	}
	if run, ok := m.completed[id]; ok {
		m.mu.Unlock()
		return copyRun(run), nil
	}
	m.mu.Unlock()
	return m.store.ActionRun(id)
}

func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	item := m.active[id]
	if item != nil {
		item.cancel()
	}
	_, completed := m.completed[id]
	m.mu.Unlock()
	if item != nil || completed {
		return nil
	}
	_, err := m.store.ActionRun(id)
	return err
}

func (m *Manager) CancelNode(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.active[m.nodes[id]]; item != nil {
		item.cancel()
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, item := range m.active {
		item.cancel()
	}
	m.mu.Unlock()
	// The production executor uses exec.CommandContext plus a two-second
	// WaitDelay, so SSH processes finish promptly after cancellation.
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		log.Print("Action executor did not stop within six seconds of cancellation")
	}
}

func copyRun(run model.ActionRun) model.ActionRun {
	if run.FinishedAt != nil {
		finished := *run.FinishedAt
		run.FinishedAt = &finished
	}
	run.Steps = append([]model.ActionStepResult{}, run.Steps...)
	for i := range run.Steps {
		step := &run.Steps[i]
		if step.StartedAt != nil {
			started := *step.StartedAt
			step.StartedAt = &started
		}
		if step.FinishedAt != nil {
			finished := *step.FinishedAt
			step.FinishedAt = &finished
		}
		if step.ExitCode != nil {
			code := *step.ExitCode
			step.ExitCode = &code
		}
	}
	return run
}

func (m *Manager) save(item *execution) error {
	m.mu.Lock()
	run := copyRun(item.run)
	m.mu.Unlock()
	if err := m.store.SaveActionRun(run); err != nil {
		m.mu.Lock()
		item.run.Error = "Could not save action history: " + err.Error()
		m.mu.Unlock()
		log.Printf("Could not save action run %s: %v", run.ID, err)
		return err
	}
	return nil
}

func (m *Manager) execute(ctx context.Context, node model.Node, item *execution) {
	defer m.wg.Done()
	defer item.cancel()
	failed := false
	for i := range item.run.Steps {
		if ctx.Err() != nil {
			break
		}
		now := time.Now().UTC()
		m.mu.Lock()
		result := &item.run.Steps[i]
		result.Status, result.StartedAt = "running", &now
		step := model.ActionStep{Command: result.Command, Name: result.Name, TimeoutSeconds: result.TimeoutSeconds, OnError: result.OnError}
		m.mu.Unlock()
		if err := m.save(item); err != nil {
			failed = true
			m.mu.Lock()
			result.Status, result.Error = "failed", item.run.Error
			end := time.Now().UTC()
			result.FinishedAt, result.DurationMS = &end, end.Sub(now).Milliseconds()
			m.mu.Unlock()
			break
		}
		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
		code, err := m.executor.Execute(stepCtx, node, step, &outputWriter{m: m, item: item, index: i}, &outputWriter{m: m, item: item, index: i, stderr: true})
		stepErr := stepCtx.Err()
		cancel()
		end := time.Now().UTC()
		m.mu.Lock()
		result.FinishedAt, result.DurationMS = &end, end.Sub(now).Milliseconds()
		if code != nil {
			value := *code
			result.ExitCode = &value
		}
		switch {
		case ctx.Err() != nil:
			result.Status, result.Error = "cancelled", "Execution cancelled. Detached remote processes may still be running."
		case errors.Is(stepErr, context.DeadlineExceeded):
			result.Status, result.Error = "failed", fmt.Sprintf("Step timed out after %d seconds. Detached remote processes may still be running.", step.TimeoutSeconds)
		case err != nil || code == nil || *code != 0:
			result.Status = "failed"
			if err != nil {
				result.Error = err.Error()
			} else if code == nil {
				result.Error = "SSH ended without a remote exit code."
			} else {
				result.Error = fmt.Sprintf("Command exited with code %d.", *code)
			}
		default:
			result.Status = "success"
		}
		stepFailed := result.Status == "failed"
		failed = failed || stepFailed
		// A transport/setup failure cannot safely be continued. OpenSSH uses
		// 255 for connection failures (also conservatively treats remote 255).
		transportFailure := code == nil && stepErr == nil || code != nil && *code == 255
		stop := result.Status == "cancelled" || stepFailed && (step.OnError != "continue" || transportFailure)
		m.mu.Unlock()
		if err := m.save(item); err != nil {
			failed = true
			break
		}
		if stop {
			break
		}
	}
	end := time.Now().UTC()
	m.mu.Lock()
	item.run.Status = "success"
	if failed {
		item.run.Status = "failed"
	}
	if ctx.Err() != nil {
		item.run.Status = "cancelled"
	}
	for i := range item.run.Steps {
		if item.run.Steps[i].Status == "pending" {
			item.run.Steps[i].Status = "skipped"
		}
	}
	item.run.FinishedAt, item.run.DurationMS = &end, end.Sub(item.run.StartedAt).Milliseconds()
	m.mu.Unlock()
	_ = m.save(item)
	m.mu.Lock()
	if item.run.Error != "" && item.run.Status == "success" {
		item.run.Status = "failed"
	}
	m.completed[item.run.ID] = copyRun(item.run)
	m.recent = append(m.recent, item.run.ID)
	if len(m.recent) > 100 {
		delete(m.completed, m.recent[0])
		m.recent = m.recent[1:]
	}
	delete(m.active, item.run.ID)
	delete(m.nodes, item.run.NodeID)
	m.mu.Unlock()
}

type outputWriter struct {
	m      *Manager
	item   *execution
	index  int
	stderr bool
}

func (w *outputWriter) Write(p []byte) (int, error) {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	result := &w.item.run.Steps[w.index]
	destination, truncated := &result.Stdout, &result.StdoutTruncated
	if w.stderr {
		destination, truncated = &result.Stderr, &result.StderrTruncated
	}
	n := min(len(p), max(0, MaxStreamBytes-len(*destination)), max(0, MaxRunBytes-w.item.bytes))
	*destination += string(p[:n])
	w.item.bytes += n
	if n < len(p) {
		*truncated = true
	}
	return len(p), nil
}
