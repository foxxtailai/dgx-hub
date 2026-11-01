package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"dgx-hub/internal/model"
)

// Completed history is capped globally; active runs are retained until finished.
const ActionHistoryLimit = 100

func (s *Store) initActions() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS actions (id TEXT PRIMARY KEY, name TEXT NOT NULL,
	 description TEXT NOT NULL, steps TEXT NOT NULL, confirm_before_run INTEGER NOT NULL);
	CREATE TABLE IF NOT EXISTS action_nodes (action_id TEXT NOT NULL REFERENCES actions(id) ON DELETE CASCADE,
	 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, PRIMARY KEY(action_id,node_id));
	CREATE INDEX IF NOT EXISTS action_nodes_node ON action_nodes(node_id);
	CREATE TABLE IF NOT EXISTS action_runs (id TEXT PRIMARY KEY, action_id TEXT NOT NULL, node_id TEXT NOT NULL,
	 started_at TEXT NOT NULL, status TEXT NOT NULL, result TEXT NOT NULL);
	CREATE INDEX IF NOT EXISTS action_runs_started ON action_runs(started_at DESC);
	CREATE INDEX IF NOT EXISTS action_runs_node ON action_runs(node_id,started_at DESC);
	CREATE INDEX IF NOT EXISTS action_runs_action ON action_runs(action_id,started_at DESC);`)
	if err != nil {
		return err
	}
	// A process that exited before final persistence cannot still own a running
	// SSH subprocess. Record the interruption instead of showing an endless run.
	rows, err := s.db.Query("SELECT result FROM action_runs WHERE status IN ('running','pending')")
	if err != nil {
		return err
	}
	var interrupted []model.ActionRun
	for rows.Next() {
		var raw string
		var run model.ActionRun
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &run); err != nil {
			break
		}
		interrupted = append(interrupted, run)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, run := range interrupted {
		run.Status, run.Error, run.FinishedAt = "cancelled", "DGX Hub stopped before this run finished.", &now
		run.DurationMS = max(int64(0), now.Sub(run.StartedAt).Milliseconds())
		for i := range run.Steps {
			step := &run.Steps[i]
			if step.Status != "running" && step.Status != "pending" {
				continue
			}
			step.Status, step.Error, step.FinishedAt = "cancelled", run.Error, &now
			if step.StartedAt != nil {
				step.DurationMS = max(int64(0), now.Sub(*step.StartedAt).Milliseconds())
			}
		}
		if err = s.SaveActionRun(run); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Actions() ([]model.Action, error) {
	rows, err := s.db.Query("SELECT id,name,description,steps,confirm_before_run FROM actions ORDER BY name COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	actions := []model.Action{}
	for rows.Next() {
		a, scanErr := scanAction(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		actions = append(actions, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Close the result set before asking for assignments: this Store intentionally
	// uses one SQLite connection for the existing foreign-key/lifecycle rules.
	for i := range actions {
		if actions[i].NodeIDs, err = s.actionNodeIDs(actions[i].ID); err != nil {
			return nil, err
		}
	}
	return actions, nil
}

type actionScanner interface{ Scan(...any) error }

func scanAction(row actionScanner) (model.Action, error) {
	var a model.Action
	var raw string
	err := row.Scan(&a.ID, &a.Name, &a.Description, &raw, &a.ConfirmBeforeRun)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	err = json.Unmarshal([]byte(raw), &a.Steps)
	return a, err
}

func (s *Store) Action(id string) (model.Action, error) {
	a, err := scanAction(s.db.QueryRow("SELECT id,name,description,steps,confirm_before_run FROM actions WHERE id=?", id))
	if err == nil {
		a.NodeIDs, err = s.actionNodeIDs(id)
	}
	return a, err
}

func (s *Store) actionNodeIDs(id string) ([]string, error) {
	rows, err := s.db.Query("SELECT node_id FROM action_nodes WHERE action_id=? ORDER BY node_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) SaveAction(a model.Action, create bool) (model.Action, error) {
	raw, err := json.Marshal(a.Steps)
	if err != nil {
		return a, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	if create {
		a.ID = newID()
		_, err = tx.Exec("INSERT INTO actions(id,name,description,steps,confirm_before_run) VALUES(?,?,?,?,?)", a.ID, a.Name, a.Description, string(raw), a.ConfirmBeforeRun)
	} else {
		var result sql.Result
		result, err = tx.Exec("UPDATE actions SET name=?,description=?,steps=?,confirm_before_run=? WHERE id=?", a.Name, a.Description, string(raw), a.ConfirmBeforeRun, a.ID)
		err = changed(result, err)
	}
	if err != nil {
		return a, err
	}
	if _, err = tx.Exec("DELETE FROM action_nodes WHERE action_id=?", a.ID); err != nil {
		return a, err
	}
	seen := map[string]bool{}
	a.NodeIDs = append([]string{}, a.NodeIDs...)
	for _, id := range a.NodeIDs {
		if seen[id] {
			return a, fmt.Errorf("duplicate assigned node %q", id)
		}
		seen[id] = true
		var exists int
		if err = tx.QueryRow("SELECT 1 FROM nodes WHERE id=?", id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return a, fmt.Errorf("assigned node %q: %w", id, ErrNotFound)
		} else if err != nil {
			return a, err
		}
		if _, err = tx.Exec("INSERT INTO action_nodes(action_id,node_id) VALUES(?,?)", a.ID, id); err != nil {
			return a, err
		}
	}
	return a, tx.Commit()
}

func (s *Store) DeleteAction(id string) error {
	result, err := s.db.Exec("DELETE FROM actions WHERE id=?", id)
	return changed(result, err)
}

func (s *Store) SaveActionRun(run model.ActionRun) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO action_runs(id,action_id,node_id,started_at,status,result) VALUES(?,?,?,?,?,?)
	ON CONFLICT(id) DO UPDATE SET action_id=excluded.action_id,node_id=excluded.node_id,started_at=excluded.started_at,status=excluded.status,result=excluded.result`,
		run.ID, run.ActionID, run.NodeID, run.StartedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), run.Status, string(raw))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM action_runs WHERE status NOT IN ('running','pending') AND id NOT IN
	 (SELECT id FROM action_runs WHERE status NOT IN ('running','pending') ORDER BY started_at DESC,id DESC LIMIT ?)`, ActionHistoryLimit)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ActionRun(id string) (model.ActionRun, error) {
	var raw string
	var run model.ActionRun
	err := s.db.QueryRow("SELECT result FROM action_runs WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return run, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &run)
	}
	return run, err
}

func (s *Store) ActionRuns(nodeID, actionID string) ([]model.ActionRun, error) {
	return s.actionRuns(nodeID, actionID, false)
}

// ActionRunSummaries omits command/output snapshots for the frequently refreshed
// history list. Full details remain available through ActionRun.
func (s *Store) ActionRunSummaries(nodeID, actionID string) ([]model.ActionRun, error) {
	return s.actionRuns(nodeID, actionID, true)
}

func actionRunsQuery(nodeID, actionID string, summary bool) (string, []any) {
	projection := "result"
	if summary {
		projection = "json_remove(result,'$.steps')"
	}
	query := "SELECT " + projection + " FROM action_runs WHERE 1=1"
	args := []any{}
	if nodeID != "" {
		query += " AND node_id=?"
		args = append(args, nodeID)
	}
	if actionID != "" {
		query += " AND action_id=?"
		args = append(args, actionID)
	}
	query += " ORDER BY started_at DESC,id DESC LIMIT ?"
	args = append(args, ActionHistoryLimit)
	return query, args
}

func (s *Store) actionRuns(nodeID, actionID string, summary bool) ([]model.ActionRun, error) {
	query, args := actionRunsQuery(nodeID, actionID, summary)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []model.ActionRun{}
	for rows.Next() {
		var raw string
		var run model.ActionRun
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &run); err != nil {
			return nil, err
		}
		if summary {
			run.Steps = []model.ActionStepResult{}
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
