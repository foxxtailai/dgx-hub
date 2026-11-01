package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"dgx-hub/internal/model"
)

func TestPersistenceAndCascade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dgx-hub.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.SaveNode(model.Node{Name: "Elly", SSHTarget: "elly", Color: "blue"}, true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.SaveNode(model.Node{Name: "Evie", SSHTarget: "evie"}, true)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SaveService(model.Service{Name: "Web", NodeID: n.ID, RemoteHost: "127.0.0.1", RemotePort: 8840, Scheme: "http", Path: "/"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Node(n.ID); err != nil || got != n {
		t.Fatalf("node color not persisted: %+v %v", got, err)
	}
	if other.Color != "green" {
		t.Fatalf("missing default node color: %+v", other)
	}
	loaded, err := s.Service(v.ID)
	if err != nil || loaded.NodeID != n.ID {
		t.Fatalf("not persisted: %+v %v", loaded, err)
	}
	if err = s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Service(v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("service not cascaded: %v", err)
	}
	if _, err = s.Node(other.ID); err != nil {
		t.Fatalf("unrelated node removed: %v", err)
	}
	if _, err = s.SaveService(model.Service{NodeID: n.ID, RemotePort: 80}, true); err == nil {
		t.Fatal("foreign key was not enforced")
	}
}

func TestNodeColorMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL, ssh_target TEXT NOT NULL, data_path TEXT NOT NULL DEFAULT '');
	INSERT INTO nodes VALUES ('old-node','Existing node','existing','/srv/data');
	CREATE TABLE services (id TEXT PRIMARY KEY, name TEXT NOT NULL, node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	remote_host TEXT NOT NULL, remote_port INTEGER NOT NULL, scheme TEXT NOT NULL, path TEXT NOT NULL);
	INSERT INTO services VALUES ('old-service','Existing service','old-node','127.0.0.1',8080,'http','/');`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopening also checks that the migration is safe to run more than once.
	for _, wantColor := range []string{"green", "orange"} {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		nodes, err := s.Nodes()
		if err != nil || len(nodes) != 1 || nodes[0] != (model.Node{ID: "old-node", Name: "Existing node", SSHTarget: "existing", DataPath: "/srv/data", Color: wantColor}) {
			s.Close()
			t.Fatalf("migrated nodes: %+v %v", nodes, err)
		}
		if service, err := s.Service("old-service"); err != nil || service.NodeID != "old-node" {
			s.Close()
			t.Fatalf("migration lost service: %+v %v", service, err)
		}
		nodes[0].Color = "orange"
		if _, err = s.SaveNode(nodes[0], false); err != nil {
			s.Close()
			t.Fatal(err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
