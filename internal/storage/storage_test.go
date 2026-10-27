package storage

import (
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
	n, err := s.SaveNode(model.Node{Name: "Elly", SSHTarget: "elly"}, true)
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
