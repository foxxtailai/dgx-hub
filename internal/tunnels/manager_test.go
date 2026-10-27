package tunnels

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dgx-hub/internal/model"
)

// This subprocess is a loopback-only fake ssh listener. It exercises actual
// process startup, bind failures, process death, and cleanup on Windows/Linux.
func TestTunnelHelperProcess(t *testing.T) {
	if os.Getenv("DGX_HUB_TUNNEL_HELPER") != "1" {
		return
	}
	address := os.Getenv("DGX_HUB_TUNNEL_BIND")
	if os.Getenv("DGX_HUB_TUNNEL_DELAY") == "1" {
		time.Sleep(400 * time.Millisecond)
	}
	l, err := net.Listen("tcp4", address)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bind: address already in use")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "debug1: Entering interactive session.")
	defer l.Close()
	for {
		conn, err := l.Accept()
		if err != nil {
			os.Exit(0)
		}
		_ = conn.Close()
	}
}
func fakeManager(t *testing.T) (*Manager, *atomic.Int32) {
	t.Helper()
	m := New()
	count := &atomic.Int32{}
	m.probe = func(context.Context, string, string, int) error { return nil }
	m.command = func(ctx context.Context, args ...string) *exec.Cmd {
		count.Add(1)
		var bind string
		for i, arg := range args {
			if arg == "-L" && i+1 < len(args) {
				parts := strings.Split(args[i+1], ":")
				bind = parts[0] + ":" + parts[1]
			}
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTunnelHelperProcess$")
		cmd.Env = append(os.Environ(), "DGX_HUB_TUNNEL_HELPER=1", "DGX_HUB_TUNNEL_BIND="+bind)
		return cmd
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, count
}
func fixture() (model.Node, model.Service) {
	return model.Node{ID: "n1", Name: "Elly", SSHTarget: "elly"}, model.Service{ID: "s1", NodeID: "n1", Name: "UI", RemoteHost: "127.0.0.1", RemotePort: 8840, Scheme: "http", Path: "/ui?x=1#view"}
}

func TestLocalLoopbackDialAllowed(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.DialTimeout("tcp4", l.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}
func urlPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestConcurrentOpenOfSameServiceReusesOneProcess(t *testing.T) {
	m, count := fakeManager(t)
	node, service := fixture()
	var wg sync.WaitGroup
	results := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := m.Ensure(context.Background(), node, service)
			results <- u
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for u := range results {
		if first == "" {
			first = u
		}
		if first != u {
			t.Fatalf("duplicate tunnels %q vs %q", first, u)
		}
	}
	if count.Load() != 1 {
		t.Fatalf("expected one child, got %d", count.Load())
	}
	u, _ := url.Parse(first)
	if u.Hostname() != "127.0.0.1" || u.Path != "/ui" || u.RawQuery != "x=1" || u.Fragment != "view" {
		t.Fatalf("bad browser URL %q", first)
	}
}
func TestEqualRemotePortsOnDifferentNodesUseDifferentLocalPorts(t *testing.T) {
	m, _ := fakeManager(t)
	node, s := fixture()
	node2 := model.Node{ID: "n2", SSHTarget: "evie"}
	s2 := s
	s2.ID = "s2"
	s2.NodeID = node2.ID
	var wg sync.WaitGroup
	var a, b string
	var e1, e2 error
	wg.Add(2)
	go func() { defer wg.Done(); a, e1 = m.Ensure(context.Background(), node, s) }()
	go func() { defer wg.Done(); b, e2 = m.Ensure(context.Background(), node2, s2) }()
	wg.Wait()
	if e1 != nil || e2 != nil {
		t.Fatalf("open errors: %v %v", e1, e2)
	}
	if urlPort(t, a) == urlPort(t, b) {
		t.Fatal("local ports collide")
	}
}
func TestDeadTunnelIsRecreatedAndCloseCleansProcesses(t *testing.T) {
	m, count := fakeManager(t)
	node, s := fixture()
	first, err := m.Ensure(context.Background(), node, s)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	child := m.items[s.ID]
	m.mu.Unlock()
	_ = child.cmd.Process.Kill()
	<-child.done
	second, err := m.Ensure(context.Background(), node, s)
	if err != nil {
		t.Fatal(err)
	}
	if count.Load() != 2 {
		t.Fatal("dead tunnel was not recreated")
	}
	m.CloseNode(node.ID)
	if listening(urlPort(t, second)) {
		t.Fatal("listener remains after node cleanup")
	}
	_ = first
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(context.Background(), node, s); err == nil {
		t.Fatal("open after shutdown succeeded")
	}
}
func TestBindRaceRetriesAnotherPort(t *testing.T) {
	m, count := fakeManager(t)
	node, s := fixture()
	blocker, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	occupied := blocker.Addr().(*net.TCPAddr).Port
	var calls int
	m.port = func() (int, error) {
		calls++
		if calls == 1 {
			return occupied, nil
		}
		return freePort()
	}
	u, err := m.Ensure(context.Background(), node, s)
	if err != nil {
		t.Fatal(err)
	}
	if urlPort(t, u) == occupied || count.Load() != 2 {
		t.Fatalf("port race was not retried: %s, starts %d", u, count.Load())
	}
}

func TestDelayedBindFailureCannotAcceptUnrelatedListener(t *testing.T) {
	m, count := fakeManager(t)
	node, s := fixture()
	blocker, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	occupied := blocker.Addr().(*net.TCPAddr).Port
	var calls int
	m.port = func() (int, error) {
		calls++
		if calls == 1 {
			return occupied, nil
		}
		return freePort()
	}
	original := m.command
	m.command = func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := original(ctx, args...)
		if count.Load() == 1 {
			cmd.Env = append(cmd.Env, "DGX_HUB_TUNNEL_DELAY=1")
		}
		return cmd
	}
	u, err := m.Ensure(context.Background(), node, s)
	if err != nil {
		t.Fatal(err)
	}
	if urlPort(t, u) == occupied || count.Load() != 2 {
		t.Fatalf("accepted unrelated listener before SSH bind: %s, starts %d", u, count.Load())
	}
}
func TestDeletionWhileStartingDoesNotLeaveTunnel(t *testing.T) {
	m, count := fakeManager(t)
	node, s := fixture()
	started, release := make(chan struct{}), make(chan struct{})
	m.probe = func(context.Context, string, string, int) error { close(started); <-release; return nil }
	done := make(chan error, 1)
	go func() { _, err := m.Ensure(context.Background(), node, s); done <- err }()
	<-started
	m.CloseNode(node.ID)
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("deleted node still opened")
		}
	case <-time.After(time.Second):
		t.Fatal("operation did not finish")
	}
	if count.Load() != 0 {
		t.Fatal("child started after node deletion")
	}
}
func TestUnsafeEndpointRejectedBeforeProcessStart(t *testing.T) {
	m, count := fakeManager(t)
	node, s := fixture()
	s.RemoteHost = "127.0.0.1;touch nope"
	if _, err := m.Ensure(context.Background(), node, s); err == nil {
		t.Fatal("unsafe remote host accepted")
	}
	if count.Load() != 0 {
		t.Fatal("invalid endpoint started a child")
	}
}
