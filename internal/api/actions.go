package api

import (
	"errors"
	"net/http"
	"slices"

	"dgx-hub/internal/actions"
	"dgx-hub/internal/model"
	"dgx-hub/internal/validation"
)

func (s *Server) actionRoutes() {
	s.mux.HandleFunc("GET /api/actions", s.listActions)
	s.mux.HandleFunc("GET /api/actions/{id}", s.getAction)
	s.mux.HandleFunc("POST /api/actions", s.createAction)
	s.mux.HandleFunc("PUT /api/actions/{id}", s.updateAction)
	s.mux.HandleFunc("PATCH /api/actions/{id}", s.updateAction)
	s.mux.HandleFunc("DELETE /api/actions/{id}", s.deleteAction)
	s.mux.HandleFunc("POST /api/nodes/{nodeID}/actions/{actionID}/runs", s.startAction)
	s.mux.HandleFunc("GET /api/action-runs", s.listActionRuns)
	s.mux.HandleFunc("GET /api/action-runs/{id}", s.getActionRun)
	s.mux.HandleFunc("POST /api/action-runs/{id}/cancel", s.cancelActionRun)
}

func (s *Server) listActions(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Actions()
	if handleError(w, err) {
		return
	}
	jsonResponse(w, 200, map[string]any{"actions": list})
}

func (s *Server) getAction(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.Action(r.PathValue("id"))
	if handleError(w, err) {
		return
	}
	jsonResponse(w, 200, a)
}

func (s *Server) createAction(w http.ResponseWriter, r *http.Request) {
	s.saveAction(w, r, true)
}
func (s *Server) updateAction(w http.ResponseWriter, r *http.Request) {
	s.saveAction(w, r, false)
}
func (s *Server) saveAction(w http.ResponseWriter, r *http.Request, create bool) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var a model.Action
	var err error
	if !create {
		a, err = s.store.Action(r.PathValue("id"))
		if handleError(w, err) {
			return
		}
	}
	// 50 multiline commands plus JSON escaping may exceed the ordinary config limit.
	if !decodeLimit(w, r, &a, 2<<20) {
		return
	}
	if err = validation.Action(&a); err != nil {
		fail(w, 400, err.Error())
		return
	}
	for _, id := range a.NodeIDs {
		if _, err = s.store.Node(id); err != nil {
			fail(w, 400, "an assigned node does not exist")
			return
		}
	}
	a.ID = r.PathValue("id")
	a, err = s.store.SaveAction(a, create)
	if handleError(w, err) {
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	jsonResponse(w, status, a)
}

func (s *Server) deleteAction(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if handleError(w, s.store.DeleteAction(r.PathValue("id"))) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) startAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirmed bool `json:"confirmed"`
	}
	if !decode(w, r, &input) {
		return
	}
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	n, err := s.store.Node(r.PathValue("nodeID"))
	if handleError(w, err) {
		return
	}
	a, err := s.store.Action(r.PathValue("actionID"))
	if handleError(w, err) {
		return
	}
	if !slices.Contains(a.NodeIDs, n.ID) {
		fail(w, 400, "this action is not assigned to the selected node")
		return
	}
	if a.ConfirmBeforeRun && !input.Confirmed {
		fail(w, 409, "confirm this action before running it")
		return
	}
	m := s.metrics.Get(n.ID)
	if m.Status != "online" || m.Stale {
		fail(w, 409, "the node must be online with current metrics before running an action")
		return
	}
	run, err := s.actions.Start(n, a)
	if errors.Is(err, actions.ErrBusy) {
		fail(w, 409, "another action is already running on this node")
		return
	}
	if errors.Is(err, actions.ErrClosed) {
		fail(w, 503, "DGX Hub is shutting down")
		return
	}
	if handleError(w, err) {
		return
	}
	jsonResponse(w, http.StatusAccepted, run)
}

func (s *Server) listActionRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ActionRunSummaries(r.URL.Query().Get("node_id"), r.URL.Query().Get("action_id"))
	if handleError(w, err) {
		return
	}
	// Refresh active status/duration without returning detailed command output.
	for i := range runs {
		if runs[i].Status == "running" {
			if current, err := s.actions.Run(runs[i].ID); err == nil {
				current.Steps = []model.ActionStepResult{}
				runs[i] = current
			}
		}
	}
	jsonResponse(w, 200, map[string]any{"runs": runs})
}

func (s *Server) getActionRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.actions.Run(r.PathValue("id"))
	if handleError(w, err) {
		return
	}
	jsonResponse(w, 200, run)
}

func (s *Server) cancelActionRun(w http.ResponseWriter, r *http.Request) {
	if handleError(w, s.actions.Cancel(r.PathValue("id"))) {
		return
	}
	s.getActionRun(w, r)
}
