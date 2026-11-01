package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"dgx-hub/internal/actions"
	"dgx-hub/internal/model"
	"dgx-hub/internal/platform"
	"dgx-hub/internal/sshutil"
	"dgx-hub/internal/storage"
	"dgx-hub/internal/telemetry"
	"dgx-hub/internal/validation"
)

type Collector interface {
	Poll(context.Context, model.Node) model.Metrics
	Get(string) model.Metrics
	Remove(string)
}
type TunnelManager interface {
	Ensure(context.Context, model.Node, model.Service) (string, error)
	CloseNode(string)
	CloseService(string)
}

type Server struct {
	store                               *storage.Store
	metrics                             Collector
	tunnels                             TunnelManager
	actions                             *actions.Manager
	host, origin, csrf, launch, version string
	assets                              fs.FS
	mux                                 *http.ServeMux
	configMu                            sync.RWMutex
	launchMu                            sync.Mutex
	launches                            map[*pendingLaunch]struct{}
	queue                               chan string
	wg                                  sync.WaitGroup
	check                               func(context.Context, string) model.SSHCheck
	terminal                            func(string, string) error
}

type pendingLaunch struct {
	nodeID, serviceID string
	cancel            context.CancelFunc
}

const contentPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

func New(store *storage.Store, metrics Collector, tunnels TunnelManager, host string, assets fs.FS, version string) *Server {
	s := &Server{store: store, metrics: metrics, tunnels: tunnels, host: host, origin: "http://" + host, csrf: token(), launch: token(), version: version, assets: assets, mux: http.NewServeMux(), queue: make(chan string, 128), launches: make(map[*pendingLaunch]struct{}), check: sshutil.Check, terminal: platform.LaunchTerminal}
	s.actions = actions.New(store, nil)
	s.routes()
	return s
}

func token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", contentPolicy)
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != s.host {
		fail(w, 403, "unexpected Host header")
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "cross-site requests are not allowed")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
		fail(w, 403, "unexpected Origin header")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if r.Header.Get("Origin") != s.origin || !equal(r.Header.Get("X-CSRF-Token"), s.csrf) {
			fail(w, 403, "request verification failed; refresh DGX Hub and retry")
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]string{"csrf_token": s.csrf, "launch_token": s.launch, "version": s.version})
	})
	s.mux.HandleFunc("GET /api/nodes", s.listNodes)
	s.mux.HandleFunc("POST /api/nodes", s.createNode)
	s.mux.HandleFunc("PATCH /api/nodes/{id}", s.updateNode)
	s.mux.HandleFunc("DELETE /api/nodes/{id}", s.deleteNode)
	s.mux.HandleFunc("GET /api/nodes/{id}/metrics", s.nodeMetrics)
	s.mux.HandleFunc("POST /api/nodes/{id}/check", s.checkNode)
	s.mux.HandleFunc("POST /api/nodes/{id}/terminal", s.nodeTerminal)
	s.mux.HandleFunc("GET /api/services", s.listServices)
	s.mux.HandleFunc("POST /api/services", s.createService)
	s.mux.HandleFunc("PATCH /api/services/{id}", s.updateService)
	s.mux.HandleFunc("DELETE /api/services/{id}", s.deleteService)
	s.mux.HandleFunc("GET /services/{id}/open", s.openService)
	s.actionRoutes()
	s.mux.HandleFunc("/", s.frontend)
}

func (s *Server) Start(ctx context.Context) {
	for i := 0; i < 2; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-s.queue:
					n, err := s.store.Node(id)
					if err == nil {
						pollCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
						s.metrics.Poll(pollCtx, n)
						cancel()
					}
				}
			}
		}()
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			s.scheduleAll()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *Server) Wait()  { s.wg.Wait() }
func (s *Server) Close() { s.actions.Close() }
func (s *Server) schedule(id string) {
	select {
	case s.queue <- id:
	default:
	}
}
func (s *Server) scheduleAll() {
	nodes, err := s.store.Nodes()
	if err != nil {
		log.Printf("load nodes for polling: %v", err)
		return
	}
	for _, n := range nodes {
		s.schedule(n.ID)
	}
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.Nodes()
	if handleError(w, err) {
		return
	}
	jsonResponse(w, 200, map[string]any{"nodes": nodes})
}
func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var n model.Node
	if !decode(w, r, &n) {
		return
	}
	if err := validation.Node(&n); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.configMu.Lock()
	n, err := s.store.SaveNode(n, true)
	s.configMu.Unlock()
	if handleError(w, err) {
		return
	}
	s.schedule(n.ID)
	jsonResponse(w, 201, n)
}
func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	id := r.PathValue("id")
	n, err := s.store.Node(id)
	if handleError(w, err) {
		return
	}
	previous := n
	if !decode(w, r, &n) {
		return
	}
	n.ID = id
	if err = validation.Node(&n); err != nil {
		fail(w, 400, err.Error())
		return
	}
	n, err = s.store.SaveNode(n, false)
	if handleError(w, err) {
		return
	}
	// Changing only the display color must not interrupt running work.
	if n.Name != previous.Name || n.SSHTarget != previous.SSHTarget || n.DataPath != previous.DataPath {
		s.cancelLaunches(id, "")
		s.actions.CancelNode(id)
		s.tunnels.CloseNode(id)
		s.metrics.Remove(id)
		s.schedule(id)
	}
	jsonResponse(w, 200, n)
}
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	id := r.PathValue("id")
	if handleError(w, s.store.DeleteNode(id)) {
		return
	}
	s.cancelLaunches(id, "")
	s.actions.CancelNode(id)
	s.tunnels.CloseNode(id)
	s.metrics.Remove(id)
	w.WriteHeader(204)
}
func (s *Server) nodeMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Node(id); handleError(w, err) {
		return
	}
	m := s.metrics.Get(id)
	m.NodeID = id
	if m.Status == "" {
		m.Status = "connecting"
	}
	if m.Errors == nil {
		m.Errors = map[string]string{}
	}
	if m.Disks == nil {
		m.Disks = []model.Disk{}
	}
	nodes, err := s.store.Nodes()
	if handleError(w, err) {
		return
	}
	readings := make(map[string]model.Metrics, len(nodes))
	for _, node := range nodes {
		if node.ID != id {
			readings[node.ID] = s.metrics.Get(node.ID)
		}
	}
	m.ConnectX = telemetry.ResolveConnectX(m, readings, time.Now())
	jsonResponse(w, 200, m)
}
func (s *Server) checkNode(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.Node(r.PathValue("id"))
	if handleError(w, err) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result := s.check(ctx, n.SSHTarget)
	if result.OK {
		s.schedule(n.ID)
	}
	jsonResponse(w, 200, result)
}
func (s *Server) nodeTerminal(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.Node(r.PathValue("id"))
	if handleError(w, err) {
		return
	}
	if err = sshutil.ValidateTarget(n.SSHTarget); err != nil {
		fail(w, 400, err.Error())
		return
	}
	err = s.terminal(n.SSHTarget, n.Color)
	result := map[string]any{"ok": err == nil, "command": "ssh " + n.SSHTarget}
	if err != nil {
		log.Printf("terminal launch for node %q failed: %v", n.ID, err)
		result["error"] = err.Error()
	}
	jsonResponse(w, 200, result)
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	services, err := s.store.Services()
	if handleError(w, err) {
		return
	}
	jsonResponse(w, 200, map[string]any{"services": services})
}
func (s *Server) createService(w http.ResponseWriter, r *http.Request) {
	var v model.Service
	if !decode(w, r, &v) {
		return
	}
	if err := validation.Service(&v); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if _, err := s.store.Node(v.NodeID); err != nil {
		fail(w, 400, "the selected node does not exist")
		return
	}
	v, err := s.store.SaveService(v, true)
	if handleError(w, err) {
		return
	}
	jsonResponse(w, 201, v)
}
func (s *Server) updateService(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	id := r.PathValue("id")
	v, err := s.store.Service(id)
	if handleError(w, err) {
		return
	}
	if !decode(w, r, &v) {
		return
	}
	v.ID = id
	if err = validation.Service(&v); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if _, err = s.store.Node(v.NodeID); err != nil {
		fail(w, 400, "the selected node does not exist")
		return
	}
	v, err = s.store.SaveService(v, false)
	if handleError(w, err) {
		return
	}
	s.cancelLaunches("", id)
	s.tunnels.CloseService(id)
	jsonResponse(w, 200, v)
}
func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	id := r.PathValue("id")
	if handleError(w, s.store.DeleteService(id)) {
		return
	}
	s.cancelLaunches("", id)
	s.tunnels.CloseService(id)
	w.WriteHeader(204)
}

func (s *Server) cancelLaunches(nodeID, serviceID string) {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	for pending := range s.launches {
		if (nodeID != "" && pending.nodeID == nodeID) || (serviceID != "" && pending.serviceID == serviceID) {
			pending.cancel()
		}
	}
}

var launchError = template.Must(template.New("launch-error").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>DGX Hub · Connection failed</title><style nonce="{{.Nonce}}">body{background:#101515;color:#e6eeee;font:16px system-ui;max-width:680px;margin:12vh auto;padding:24px}h1{font-size:26px}p{line-height:1.7;color:#b1c2c0}pre{white-space:pre-wrap;background:#192221;padding:20px;border-radius:12px}a{color:#8ee4b0}</style><h1>Could not open {{.Name}}</h1><p>DGX Hub could not establish a working SSH tunnel. Check that the node and service are running. If SSH needs setup, use SSH setup on the node card.</p><pre>{{.Error}}</pre><p><a href="/">Return to DGX Hub</a></p></html>`))

func (s *Server) openService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "use GET to open a saved service")
		return
	}
	if !equal(r.URL.Query().Get("token"), s.launch) {
		fail(w, 403, "open services from the DGX Hub dashboard")
		return
	}
	if fetch := r.Header.Get("Sec-Fetch-Site"); fetch != "" && fetch != "same-origin" && fetch != "none" {
		fail(w, 403, "open services from the DGX Hub dashboard")
		return
	}
	if r.Header.Get("Purpose") == "prefetch" || r.Header.Get("Sec-Purpose") == "prefetch" {
		w.WriteHeader(204)
		return
	}
	s.configMu.RLock()
	v, err := s.store.Service(r.PathValue("id"))
	if handleError(w, err) {
		s.configMu.RUnlock()
		return
	}
	n, err := s.store.Node(v.NodeID)
	if handleError(w, err) {
		s.configMu.RUnlock()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	pending := &pendingLaunch{nodeID: n.ID, serviceID: v.ID, cancel: cancel}
	s.launchMu.Lock()
	s.launches[pending] = struct{}{}
	s.launchMu.Unlock()
	s.configMu.RUnlock()
	defer func() { s.launchMu.Lock(); delete(s.launches, pending); s.launchMu.Unlock() }()
	location, err := s.tunnels.Ensure(ctx, n, v)
	if err != nil {
		nonce := token()
		w.Header().Set("Content-Security-Policy", strings.Replace(contentPolicy, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(502)
		_ = launchError.Execute(w, map[string]string{"Name": v.Name, "Error": err.Error(), "Nonce": nonce})
		return
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		fail(w, 405, "method not allowed")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/services/") {
		fail(w, 404, "route not found")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if !fs.ValidPath(path) {
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(s.assets, path); err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.FileServerFS(s.assets).ServeHTTP(w, r)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, 32<<10)
}
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		fail(w, 415, "send application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		fail(w, 400, "send a single JSON object")
		return false
	}
	return true
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
func handleError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, storage.ErrNotFound) {
		fail(w, 404, "record not found")
	} else {
		log.Printf("storage error: %v", err)
		fail(w, 500, fmt.Sprint("could not access the local database"))
	}
	return true
}
