package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func launchSession(app string) string {
	return fmt.Sprintf(`{"app":%q,"version":"0.1.2","csrf_token":%q,"launch_token":%q}`, app, strings.Repeat("a", 64), strings.Repeat("b", 64))
}

func TestListenOrOpenExistingDashboard(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/session" {
					app := "dgx-hub"
					if legacy {
						app = ""
					}
					fmt.Fprint(w, launchSession(app))
				} else {
					fmt.Fprint(w, "<title>DGX Hub — Your local workspace</title>")
				}
			}))
			defer server.Close()
			var opened []string
			listener, err := listenOrOpen(t.Context(), server.Listener.Addr().String(), func(url string) error { opened = append(opened, url); return nil })
			if err != nil || listener != nil || len(opened) != 1 || opened[0] != server.URL+"/" {
				t.Fatalf("handoff failed: listener=%v error=%v URLs=%v", listener, err, opened)
			}
		})
	}
}

func TestForeignPortNeverOpensBrowserOrFollowsRedirect(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		fmt.Fprint(w, launchSession("dgx-hub"))
	}))
	defer other.Close()
	for _, tc := range []struct {
		name, body, root string
		status           int
	}{
		{"unrelated HTML", "<html>Other application</html>", "", 200},
		{"wrong app", launchSession("other-app"), "", 200},
		{"legacy lookalike", launchSession(""), "<title>Other dashboard</title>", 200},
		{"missing tokens", `{"app":"dgx-hub","version":"0.1.2"}`, "", 200},
		{"oversized", strings.Repeat(" ", 4096) + launchSession("dgx-hub"), "", 200},
		{"redirect", "", "", 302},
		{"unavailable", "", "", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", other.URL)
				w.WriteHeader(tc.status)
				if r.URL.Path == "/api/session" {
					fmt.Fprint(w, tc.body)
				} else {
					fmt.Fprint(w, tc.root)
				}
			}))
			defer server.Close()
			opened := false
			listener, err := listenOrOpen(t.Context(), server.Listener.Addr().String(), func(string) error { opened = true; return nil })
			if err == nil || listener != nil || opened {
				t.Fatalf("foreign service treated as DGX Hub: listener=%v err=%v opened=%v", listener, err, opened)
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("probe followed a redirect")
	}
}

func TestHeadlessConflictDoesNotOpenBrowserOrProbe(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	if listener, err := listenOrOpen(t.Context(), server.Listener.Addr().String(), nil); listener != nil || err == nil || calls.Load() != 0 {
		t.Fatalf("headless launch unexpectedly attached: listener=%v err=%v calls=%d", listener, err, calls.Load())
	}
}

func TestListenOrOpenWaitsForStartingOwner(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, launchSession("dgx-hub")) })}
	defer server.Close()
	defer listener.Close()
	go func() {
		// The first request fails while the owner is still initializing. The
		// retry must reach the same listener once it starts serving HTTP.
		connection, err := listener.Accept()
		if err == nil {
			connection.Close()
			_ = server.Serve(listener)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	opened := 0
	got, err := listenOrOpen(ctx, listener.Addr().String(), func(string) error { opened++; return nil })
	if err != nil || got != nil || opened != 1 {
		t.Fatalf("starting owner was not reused: listener=%v err=%v opened=%d", got, err, opened)
	}
}

func TestStartupProbeHonorsDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if runningHub(ctx, server.Listener.Addr().String()) || ctx.Err() == nil {
		t.Fatal("stalled server escaped the launch deadline")
	}
}

func TestExistingDashboardBrowserFailureIsActionable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, launchSession("dgx-hub")) }))
	defer server.Close()
	problem := errors.New("no browser handler")
	listener, err := listenOrOpen(t.Context(), server.Listener.Addr().String(), func(string) error { return problem })
	if listener != nil || !errors.Is(err, problem) || !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("missing browser recovery information: %v", err)
	}
}

func TestLaunchProcessHelper(t *testing.T) {
	if os.Getenv("DGX_HUB_LAUNCH_TEST") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("DGX_HUB_LAUNCH_ARGS")), &args); err != nil {
		os.Exit(2)
	}
	err := runWithDesktop(args, desktopIntegration{
		openBrowser: func(url string) error {
			file, err := os.OpenFile(os.Getenv("DGX_HUB_BROWSER_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = fmt.Fprintln(file, url)
			return err
		},
		runTray: func(ctx context.Context, _ string, _ func()) error { <-ctx.Done(); return nil },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestRepeatedProcessOpensExistingOwnerBeforeTouchingData(t *testing.T) {
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	port := fmt.Sprint(reserved.Addr().(*net.TCPAddr).Port)
	reserved.Close()
	directory := t.TempDir()
	browserLog := filepath.Join(directory, "browser.log")
	child := func(ctx context.Context, dataDir string) *exec.Cmd {
		args, _ := json.Marshal([]string{"--port", port, "--data-dir", dataDir})
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLaunchProcessHelper$")
		cmd.Env = append(os.Environ(), "DGX_HUB_LAUNCH_TEST=1", "DGX_HUB_LAUNCH_ARGS="+string(args), "DGX_HUB_BROWSER_LOG="+browserLog)
		return cmd
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := child(ctx, filepath.Join(directory, "owner"))
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Process.Kill(); _ = owner.Wait() }()
	client := &http.Client{Timeout: 300 * time.Millisecond}
	getSession := func() map[string]string {
		response, err := client.Get("http://" + address + "/api/session")
		if err != nil {
			return nil
		}
		defer response.Body.Close()
		var session map[string]string
		if json.NewDecoder(response.Body).Decode(&session) != nil {
			return nil
		}
		return session
	}
	var before map[string]string
	for before == nil {
		if ctx.Err() != nil {
			t.Fatal("owner did not start")
		}
		before = getSession()
		if before == nil {
			time.Sleep(20 * time.Millisecond)
		}
	}
	unusedData := filepath.Join(directory, "secondary-must-not-be-created")
	if output, err := child(ctx, unusedData).CombinedOutput(); err != nil {
		t.Fatalf("repeated launch did not exit successfully: %v\n%s", err, output)
	}
	after := getSession()
	if before["app"] != "dgx-hub" || after == nil || after["csrf_token"] != before["csrf_token"] {
		t.Fatal("repeated launch replaced or stopped the owner")
	}
	if _, err := os.Stat(unusedData); !os.IsNotExist(err) {
		t.Fatal("repeated launch touched its data directory")
	}
	opened, err := os.ReadFile(browserLog)
	if err != nil || string(opened) != strings.Repeat("http://"+address+"/\n", 2) {
		t.Fatalf("first and repeated launch must each open the same dashboard: %v %q", err, opened)
	}
}
