package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dgx-hub/internal/model"
)

func TestLegacyImportIncludesLiveWALAndPreservesOriginal(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "previous", "legacy.db")
	destination := filepath.Join(root, "DGXHub", "dgx-hub.db")
	old, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err = old.db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	n, err := old.SaveNode(model.Node{Name: "Elly", SSHTarget: "test-user@example.invalid"}, true)
	if err != nil {
		t.Fatal(err)
	}
	v, err := old.SaveService(model.Service{Name: "Web", NodeID: n.ID, RemoteHost: "127.0.0.1", RemotePort: 8080, Scheme: "http", Path: "/"}, true)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.Stat(source + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("test requires uncheckpointed WAL: %v", err)
	}
	imported, err := ImportLegacyDatabase(context.Background(), source, destination)
	if err != nil || !imported {
		t.Fatalf("import: %v %v", imported, err)
	}
	current, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	got, err := current.Service(v.ID)
	if err != nil || got != v {
		t.Fatalf("service was lost: %+v %v", got, err)
	}
	if original, err := old.Node(n.ID); err != nil || original != n {
		t.Fatalf("original changed: %+v %v", original, err)
	}
	if err = current.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	imported, err = ImportLegacyDatabase(context.Background(), source, destination)
	if err != nil || imported {
		t.Fatalf("existing destination replaced: %v %v", imported, err)
	}
	nodes, err := current.Nodes()
	if err != nil || len(nodes) != 0 {
		t.Fatalf("old data imported twice: %+v %v", nodes, err)
	}
}

func TestMissingOrInvalidLegacyDatabaseDoesNotCreateDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "legacy.db")
	destination := filepath.Join(root, "new", "dgx-hub.db")
	if imported, err := ImportLegacyDatabase(context.Background(), source, destination); err != nil || imported {
		t.Fatalf("missing database: %v %v", imported, err)
	}
	if err := os.WriteFile(source, []byte("not SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	if imported, err := ImportLegacyDatabase(context.Background(), source, destination); err == nil || imported {
		t.Fatalf("invalid database imported: %v %v", imported, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("partial destination left behind: %v", err)
	}
}
