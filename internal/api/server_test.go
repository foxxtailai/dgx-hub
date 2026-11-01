package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"dgx-hub/internal/model"
	"dgx-hub/internal/storage"
)

type fakeMetrics struct{}

func TestTerminalFailureReturnsCopyableCommand(t *testing.T) {
	s, _ := setup(t)
	n, err := s.store.SaveNode(model.Node{Name: "Test node", SSHTarget: "test-user@example.invalid", Color: "blue"}, true)
	if err != nil {
		t.Fatal(err)
	}
	s.terminal = func(target, color string) error {
		if target != n.SSHTarget || color != n.Color {
			t.Fatalf("wrong terminal destination/color: %s %s", target, color)
		}
		return errors.New("terminal activation failed")
	}
	w := request(s, "POST", "/api/nodes/"+n.ID+"/terminal", "", true)
	var result struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Error   string `json:"error"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.OK || result.Command != "ssh test-user@example.invalid" || result.Error != "terminal activation failed" {
		t.Fatalf("missing actionable error: %d %s", w.Code, w.Body.String())
	}
}

func (fakeMetrics) Poll(context.Context, model.Node) model.Metrics { return model.Metrics{} }
func (fakeMetrics) Get(string) model.Metrics                       { return model.Metrics{Status: "connecting"} }
func (fakeMetrics) Remove(string)                                  {}

type sensorMetrics struct {
	fakeMetrics
	value model.Metrics
}

func (m sensorMetrics) Get(string) model.Metrics { return m.value }

func TestMetricsSensorJSONIncludesNumbersAndNulls(t *testing.T) {
	s, _ := setup(t)
	node, err := s.store.SaveNode(model.Node{Name: "Sensors", SSHTarget: "test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	zero, temperature := 0.0, 61.5
	for _, value := range []model.Metrics{{}, {TemperatureC: &temperature, PowerW: &zero}} {
		s.metrics = sensorMetrics{value: value}
		response := request(s, "GET", "/api/nodes/"+node.ID+"/metrics", "", false)
		var payload map[string]any
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			t.Fatal(response.Body.String())
		}
		for _, field := range []struct {
			key   string
			value *float64
		}{{"temperature_c", value.TemperatureC}, {"power_w", value.PowerW}} {
			got, present := payload[field.key]
			if !present || (field.value == nil && got != nil) || (field.value != nil && got != *field.value) {
				t.Fatalf("%s: %#v", field.key, payload)
			}
		}
	}
}

type fakeTunnels struct {
	ensures                   int
	closedNode, closedService string
}

type blockingTunnel struct{ started chan struct{} }

func (b *blockingTunnel) Ensure(ctx context.Context, _ model.Node, _ model.Service) (string, error) {
	close(b.started)
	<-ctx.Done()
	return "", ctx.Err()
}
func (*blockingTunnel) CloseNode(string)    {}
func (*blockingTunnel) CloseService(string) {}

func TestSlowOpenDoesNotBlockCRUDAndDeletionCancelsIt(t *testing.T) {
	s, _ := setup(t)
	n, err := s.store.SaveNode(model.Node{Name: "Slow node", SSHTarget: "slow"}, true)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.store.SaveService(model.Service{Name: "Slow web", NodeID: n.ID, RemoteHost: "127.0.0.1", RemotePort: 80, Scheme: "http", Path: "/"}, true)
	if err != nil {
		t.Fatal(err)
	}
	b := &blockingTunnel{started: make(chan struct{})}
	s.tunnels = b
	opened := make(chan *httptest.ResponseRecorder, 1)
	go func() { opened <- request(s, "GET", "/services/"+v.ID+"/open?token="+s.launch, "", false) }()
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("open did not start")
	}
	created := make(chan *httptest.ResponseRecorder, 1)
	go func() { created <- request(s, "POST", "/api/nodes", `{"name":"Other","ssh_target":"other"}`, true) }()
	select {
	case w := <-created:
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("slow open blocked unrelated CRUD")
	}
	deleted := request(s, "DELETE", "/api/nodes/"+n.ID, "", true)
	if deleted.Code != 204 {
		t.Fatal(deleted.Code, deleted.Body.String())
	}
	select {
	case w := <-opened:
		if w.Code != 502 {
			t.Fatal(w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "nonce-") || strings.Contains(w.Body.String(), "ZgotmplZ") {
			t.Fatal("invalid launch-error style nonce")
		}
	case <-time.After(time.Second):
		t.Fatal("node deletion did not cancel pending open")
	}
}

func (f *fakeTunnels) Ensure(context.Context, model.Node, model.Service) (string, error) {
	f.ensures++
	return "http://127.0.0.1:45678/", nil
}
func (f *fakeTunnels) CloseNode(id string)    { f.closedNode = id }
func (f *fakeTunnels) CloseService(id string) { f.closedService = id }

func setup(t *testing.T) (*Server, *fakeTunnels) {
	t.Helper()
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tm := &fakeTunnels{}
	s := New(st, fakeMetrics{}, tm, "127.0.0.1:8787", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("DGX Hub")}}, "test")
	return s, tm
}
func request(s *Server, method, path, body string, valid bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	if valid {
		r.Header.Set("Origin", s.origin)
		r.Header.Set("X-CSRF-Token", s.csrf)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestLocalAPISecurity(t *testing.T) {
	s, _ := setup(t)
	for _, tt := range []struct {
		name, method, path, host, origin, csrf, fetch string
		want                                          int
	}{
		{"dns rebinding", "GET", "/api/session", "evil.test:8787", "", "", "", 403},
		{"cross origin", "GET", "/api/session", s.host, "https://evil.test", "", "", 403},
		{"cross site", "GET", "/api/session", s.host, "", "", "cross-site", 403},
		{"missing csrf", "POST", "/api/nodes", s.host, s.origin, "", "same-origin", 403},
		{"missing origin", "POST", "/api/nodes", s.host, "", s.csrf, "same-origin", 403},
		{"normal read", "GET", "/api/session", s.host, "", "", "same-origin", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "http://"+s.host+tt.path, strings.NewReader(`{}`))
			r.Host = tt.host
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("X-CSRF-Token", tt.csrf)
			r.Header.Set("Sec-Fetch-Site", tt.fetch)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCRUDAndProtectedServiceLaunch(t *testing.T) {
	s, tm := setup(t)
	w := request(s, "POST", "/api/nodes", `{"name":"Elly","ssh_target":"elly","data_path":"/srv/ai","color":"blue"}`, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n model.Node
	json.Unmarshal(w.Body.Bytes(), &n)
	if n.Color != "blue" {
		t.Fatalf("node color not returned: %+v", n)
	}
	w = request(s, "PATCH", "/api/nodes/"+n.ID, `{"color":"yellow"}`, true)
	if w.Code != 200 || tm.closedNode != "" {
		t.Fatal("color change interrupted tunnels", w.Code, w.Body.String())
	}
	w = request(s, "POST", "/api/services", `{"name":"Web","node_id":"`+n.ID+`","remote_host":"127.0.0.1","remote_port":8840,"scheme":"http","path":"/"}`, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v model.Service
	json.Unmarshal(w.Body.Bytes(), &v)
	path := "/services/" + v.ID + "/open"
	if w = request(s, "GET", path, "", false); w.Code != 403 || tm.ensures != 0 {
		t.Fatal("launch without token was permitted")
	}
	if w = request(s, "HEAD", path+"?token="+s.launch, "", false); w.Code != 405 || tm.ensures != 0 {
		t.Fatal("HEAD created a tunnel")
	}
	r := httptest.NewRequest("GET", "http://"+s.host+path+"?token="+s.launch, nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, r)
	if rec.Code != 403 || tm.ensures != 0 {
		t.Fatal("cross-site launch permitted")
	}
	if w = request(s, "GET", path+"?token="+s.launch, "", false); w.Code != http.StatusSeeOther || tm.ensures != 1 || w.Header().Get("Location") != "http://127.0.0.1:45678/" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(s, "PATCH", "/api/nodes/"+n.ID, `{"name":"Elly renamed"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	updated, err := s.store.Node(n.ID)
	if err != nil || updated.SSHTarget != "elly" || updated.Color != "yellow" {
		t.Fatal("PATCH lost existing fields")
	}
	w = request(s, "DELETE", "/api/nodes/"+n.ID, "", true)
	if w.Code != 204 || tm.closedNode != n.ID {
		t.Fatal(w.Code, w.Body.String())
	}
	services, _ := s.store.Services()
	if len(services) != 0 {
		t.Fatal("node deletion did not cascade")
	}
}

func TestRejectUnsafeInputAndGuideAuthFailure(t *testing.T) {
	s, _ := setup(t)
	for _, body := range []string{`{"name":"bad","ssh_target":"-oProxyCommand=bad"}`, `{"name":"bad","ssh_target":"elly","command":"rm"}`, `{"name":"bad","ssh_target":"elly","color":"purple"}`, `{"name":"bad","ssh_target":"elly"} {}`} {
		if w := request(s, "POST", "/api/nodes", body, true); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	n, err := s.store.SaveNode(model.Node{Name: "Elly", SSHTarget: "elly"}, true)
	if err != nil {
		t.Fatal(err)
	}
	s.check = func(context.Context, string) model.SSHCheck {
		return model.SSHCheck{OK: false, Code: "authentication_required", Steps: []model.SetupStep{{Title: "Set up SSH keys"}}}
	}
	w := request(s, "POST", "/api/nodes/"+n.ID+"/check", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "authentication_required") {
		t.Fatal(w.Code, w.Body.String())
	}
}
