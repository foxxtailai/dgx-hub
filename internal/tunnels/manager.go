package tunnels

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"dgx-hub/internal/model"
	"dgx-hub/internal/sshutil"
)

type tunnel struct {
	node    model.Node
	service model.Service
	port    int
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
	stderr  sshutil.Buffer
}

func (t *tunnel) alive() bool {
	select {
	case <-t.done:
		return false
	default:
		return true
	}
}
func (t *tunnel) stop() {
	t.cancel()
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		select {
		case <-t.done:
		case <-time.After(3 * time.Second):
			log.Printf("SSH tunnel for service %q did not finish waiting after cancellation and kill", t.service.ID)
		}
	}
}

type Manager struct {
	mu           sync.Mutex
	items        map[string]*tunnel
	locks        map[string]*sync.Mutex
	nodeEpoch    map[string]uint64
	serviceEpoch map[string]uint64
	closed       bool
	command      func(context.Context, ...string) *exec.Cmd
	probe        func(context.Context, string, string, int) error
	port         func() (int, error)
}

func New() *Manager {
	return &Manager{items: map[string]*tunnel{}, locks: map[string]*sync.Mutex{}, nodeEpoch: map[string]uint64{}, serviceEpoch: map[string]uint64{}, command: sshutil.Command, probe: sshutil.ProbeTCP, port: freePort}
}
func freePort() (int, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	err = l.Close()
	return port, err
}
func (m *Manager) lock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	return l
}
func (m *Manager) valid(nodeID, serviceID string, ne, se uint64) bool {
	return !m.closed && m.nodeEpoch[nodeID] == ne && m.serviceEpoch[serviceID] == se
}

func (m *Manager) Ensure(ctx context.Context, node model.Node, service model.Service) (string, error) {
	if err := sshutil.ValidateTarget(node.SSHTarget); err != nil {
		return "", err
	}
	if err := sshutil.ValidateRemoteHost(service.RemoteHost); err != nil {
		return "", err
	}
	if service.ID == "" || node.ID == "" || service.NodeID != node.ID {
		return "", errors.New("service does not belong to the selected node")
	}
	if service.RemotePort < 1 || service.RemotePort > 65535 {
		return "", errors.New("remote port must be between 1 and 65535")
	}
	if service.Scheme != "http" && service.Scheme != "https" {
		return "", errors.New("only HTTP and HTTPS services are supported")
	}
	if !strings.HasPrefix(service.Path, "/") || strings.ContainsAny(service.Path, "\x00\r\n") || strings.HasPrefix(service.Path, "//") {
		return "", errors.New("service path must start with one slash and contain no control characters")
	}
	l := m.lock(service.ID)
	l.Lock()
	defer l.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", errors.New("DGX Hub is shutting down")
	}
	ne, se := m.nodeEpoch[node.ID], m.serviceEpoch[service.ID]
	existing := m.items[service.ID]
	m.mu.Unlock()
	if existing != nil && (existing.node != node || existing.service != service || !existing.alive() || !listening(existing.port)) {
		existing.stop()
		m.mu.Lock()
		if m.items[service.ID] == existing {
			delete(m.items, service.ID)
		}
		m.mu.Unlock()
		existing = nil
	}
	if err := m.probe(ctx, node.SSHTarget, service.RemoteHost, service.RemotePort); err != nil {
		return "", err
	}
	m.mu.Lock()
	valid := m.valid(node.ID, service.ID, ne, se)
	m.mu.Unlock()
	if !valid {
		return "", errors.New("node or service changed while opening the tunnel; try again")
	}
	if existing != nil && existing.alive() && listening(existing.port) {
		return serviceURL(existing.port, service), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("tunnel startup cancelled: %w", err)
		}
		port, err := m.port()
		if err != nil {
			return "", fmt.Errorf("choose local port: %w", err)
		}
		host := strings.TrimSuffix(strings.TrimPrefix(service.RemoteHost, "["), "]")
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		args := append(sshutil.BaseArgs(), "-N", "-o", "LogLevel=DEBUG1", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", "-L", fmt.Sprintf("127.0.0.1:%d:%s:%d", port, host, service.RemotePort), node.SSHTarget)
		processCtx, stop := context.WithCancel(context.Background())
		t := &tunnel{node: node, service: service, port: port, cancel: stop, done: make(chan struct{})}
		t.cmd = m.command(processCtx, args...)
		t.cmd.Stderr = &t.stderr
		if err := t.cmd.Start(); err != nil {
			stop()
			_, message := sshutil.ClassifyError(err, "")
			return "", errors.New(message)
		}
		go func() { t.err = t.cmd.Wait(); close(t.done) }()
		m.mu.Lock()
		valid = m.valid(node.ID, service.ID, ne, se)
		if valid {
			m.items[service.ID] = t
		}
		m.mu.Unlock()
		if !valid {
			t.stop()
			return "", errors.New("node or service changed while opening the tunnel; try again")
		}
		err = waitReady(ctx, t)
		if err == nil {
			return serviceURL(port, service), nil
		}
		t.stop()
		m.mu.Lock()
		if m.items[service.ID] == t {
			delete(m.items, service.ID)
		}
		m.mu.Unlock()
		last = err
		detail := strings.ToLower(t.stderr.String())
		if !strings.Contains(detail, "address already in use") && !strings.Contains(detail, "cannot listen") && !strings.Contains(detail, "could not request local forwarding") && !strings.Contains(detail, "bind:") {
			break
		}
	}
	return "", fmt.Errorf("could not start SSH tunnel: %w", last)
}
func listening(port int) bool {
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
func waitReady(ctx context.Context, t *tunnel) error {
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for local SSH listener: %w (%s)", ctx.Err(), strings.TrimSpace(t.stderr.String()))
		case <-t.done:
			_, message := sshutil.ClassifyError(t.err, t.stderr.String())
			return errors.New(message)
		case <-ticker.C:
			// OpenSSH enters client_loop only after local forwarding setup and
			// ExitOnForwardFailure processing. A TCP accept alone could belong
			// to a different process that won the port-allocation race.
			if strings.Contains(t.stderr.String(), "Entering interactive session.") && t.alive() && listening(t.port) {
				return nil
			}
		}
	}
}
func serviceURL(port int, s model.Service) string {
	u := url.URL{Scheme: s.Scheme, Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	ref, err := url.Parse(s.Path)
	if err == nil {
		u.Path = ref.Path
		u.RawPath = ref.RawPath
		u.RawQuery = ref.RawQuery
		u.Fragment = ref.Fragment
	} else {
		u.Path = s.Path
	}
	return u.String()
}
func (m *Manager) CloseService(id string) {
	m.mu.Lock()
	m.serviceEpoch[id]++
	t := m.items[id]
	delete(m.items, id)
	m.mu.Unlock()
	if t != nil {
		t.stop()
	}
}
func (m *Manager) CloseNode(id string) {
	m.mu.Lock()
	m.nodeEpoch[id]++
	var stopped []*tunnel
	for key, t := range m.items {
		if t.node.ID == id {
			stopped = append(stopped, t)
			delete(m.items, key)
		}
	}
	m.mu.Unlock()
	for _, t := range stopped {
		t.stop()
	}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	var stopped []*tunnel
	for _, t := range m.items {
		stopped = append(stopped, t)
	}
	m.items = map[string]*tunnel{}
	m.mu.Unlock()
	for _, t := range stopped {
		t.stop()
	}
	return nil
}
