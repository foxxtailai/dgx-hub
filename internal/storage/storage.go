package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"dgx-hub/internal/model"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("record not found")

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	_, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
	CREATE TABLE IF NOT EXISTS nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL, ssh_target TEXT NOT NULL, data_path TEXT NOT NULL DEFAULT '', color TEXT NOT NULL DEFAULT 'green');
	CREATE TABLE IF NOT EXISTS services (id TEXT PRIMARY KEY, name TEXT NOT NULL, node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	remote_host TEXT NOT NULL, remote_port INTEGER NOT NULL CHECK(remote_port BETWEEN 1 AND 65535), scheme TEXT NOT NULL, path TEXT NOT NULL);
	CREATE INDEX IF NOT EXISTS services_node ON services(node_id);`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	if err = s.initNodeColors(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize node colors: %w", err)
	}
	if err = s.initActions(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize actions: %w", err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) initNodeColors() error {
	var columns int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name='color'").Scan(&columns); err != nil {
		return err
	}
	if columns == 0 {
		_, err := s.db.Exec("ALTER TABLE nodes ADD COLUMN color TEXT NOT NULL DEFAULT 'green'")
		return err
	}
	return nil
}

func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

func (s *Store) Nodes() ([]model.Node, error) {
	rows, err := s.db.Query("SELECT id,name,ssh_target,data_path,color FROM nodes ORDER BY name COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []model.Node{}
	for rows.Next() {
		var n model.Node
		if err := rows.Scan(&n.ID, &n.Name, &n.SSHTarget, &n.DataPath, &n.Color); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *Store) Node(id string) (model.Node, error) {
	var n model.Node
	err := s.db.QueryRow("SELECT id,name,ssh_target,data_path,color FROM nodes WHERE id=?", id).Scan(&n.ID, &n.Name, &n.SSHTarget, &n.DataPath, &n.Color)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return n, err
}

func (s *Store) SaveNode(n model.Node, create bool) (model.Node, error) {
	if n.Color == "" {
		n.Color = "green"
	}
	if create {
		n.ID = newID()
		_, err := s.db.Exec("INSERT INTO nodes(id,name,ssh_target,data_path,color) VALUES(?,?,?,?,?)", n.ID, n.Name, n.SSHTarget, n.DataPath, n.Color)
		return n, err
	}
	result, err := s.db.Exec("UPDATE nodes SET name=?,ssh_target=?,data_path=?,color=? WHERE id=?", n.Name, n.SSHTarget, n.DataPath, n.Color, n.ID)
	return n, changed(result, err)
}

func (s *Store) DeleteNode(id string) error {
	result, err := s.db.Exec("DELETE FROM nodes WHERE id=?", id)
	return changed(result, err)
}

func (s *Store) Services() ([]model.Service, error) {
	rows, err := s.db.Query("SELECT id,name,node_id,remote_host,remote_port,scheme,path FROM services ORDER BY name COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	services := []model.Service{}
	for rows.Next() {
		var v model.Service
		if err = rows.Scan(&v.ID, &v.Name, &v.NodeID, &v.RemoteHost, &v.RemotePort, &v.Scheme, &v.Path); err != nil {
			return nil, err
		}
		services = append(services, v)
	}
	return services, rows.Err()
}

func (s *Store) Service(id string) (model.Service, error) {
	var v model.Service
	err := s.db.QueryRow("SELECT id,name,node_id,remote_host,remote_port,scheme,path FROM services WHERE id=?", id).Scan(&v.ID, &v.Name, &v.NodeID, &v.RemoteHost, &v.RemotePort, &v.Scheme, &v.Path)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

func (s *Store) SaveService(v model.Service, create bool) (model.Service, error) {
	if create {
		v.ID = newID()
		_, err := s.db.Exec("INSERT INTO services(id,name,node_id,remote_host,remote_port,scheme,path) VALUES(?,?,?,?,?,?,?)", v.ID, v.Name, v.NodeID, v.RemoteHost, v.RemotePort, v.Scheme, v.Path)
		return v, err
	}
	result, err := s.db.Exec("UPDATE services SET name=?,node_id=?,remote_host=?,remote_port=?,scheme=?,path=? WHERE id=?", v.Name, v.NodeID, v.RemoteHost, v.RemotePort, v.Scheme, v.Path, v.ID)
	return v, changed(result, err)
}

func (s *Store) DeleteService(id string) error {
	result, err := s.db.Exec("DELETE FROM services WHERE id=?", id)
	return changed(result, err)
}

func changed(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
