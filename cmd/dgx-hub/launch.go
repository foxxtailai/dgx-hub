package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type desktopIntegration struct {
	openBrowser func(string) error
	runTray     func(context.Context, string, func()) error
}

// The listening socket is the instance lock for this dashboard address. Only
// its owner starts storage, telemetry and the tray; later launches open its UI.
// A nil opener preserves headless --no-tray behavior, including bind errors.
func listenOrOpen(ctx context.Context, address string, open func(string) error) (net.Listener, error) {
	listener, err := net.Listen("tcp4", address)
	if err == nil {
		return listener, nil
	}
	if open != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		running := runningHub(probeCtx, address)
		cancel()
		if running {
			url := "http://" + address + "/"
			if err := open(url); err != nil {
				return nil, fmt.Errorf("DGX Hub is already running, but its dashboard could not be opened; open %s manually: %w", url, err)
			}
			return nil, nil
		}
	}
	return nil, fmt.Errorf("cannot listen on %s; no running DGX Hub was identified on this port: %w", address, err)
}

func runningHub(ctx context.Context, address string) bool {
	// Fixed loopback destination, no proxy and no redirects: an unrelated port
	// occupant cannot send the browser to a URL from its response.
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   300 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	url := "http://" + address
	for {
		body, err := launchResponse(ctx, client, url+"/api/session", 4096)
		if err == nil {
			var session struct {
				App         string `json:"app"`
				Version     string `json:"version"`
				CSRFToken   string `json:"csrf_token"`
				LaunchToken string `json:"launch_token"`
			}
			if json.Unmarshal(body, &session) != nil || session.Version == "" || !sessionToken(session.CSRFToken) || !sessionToken(session.LaunchToken) {
				return false
			}
			if session.App != "" {
				return session.App == "dgx-hub"
			}
			// Older installed builds predate the app marker. Match their actual
			// dashboard as well as their session format for upgrade compatibility.
			html, err := launchResponse(ctx, client, url+"/", 64<<10)
			return err == nil && strings.Contains(string(html), "<title>DGX Hub — Your local workspace</title>")
		}
		// The winning process may have bound the socket but not finished its
		// first storage initialization. Wait only for transport/startup failures.
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func launchResponse(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Non-200 responses are conclusive foreign/unavailable server replies,
		// rather than a reason to wait through the startup retry budget.
		return []byte{}, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return []byte{}, nil
	}
	return body, nil
}

func sessionToken(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
