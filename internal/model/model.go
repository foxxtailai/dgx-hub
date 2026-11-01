package model

import "time"

type Node struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	SSHTarget string `json:"ssh_target"`
	DataPath  string `json:"data_path"`
}

type Service struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	NodeID     string `json:"node_id"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
	Scheme     string `json:"scheme"`
	Path       string `json:"path"`
}

type Memory struct {
	Used    uint64  `json:"used"`
	Total   uint64  `json:"total"`
	Percent float64 `json:"percent"`
}

type Disk struct {
	Path    string  `json:"path"`
	Mount   string  `json:"mount"`
	Used    uint64  `json:"used"`
	Total   uint64  `json:"total"`
	Percent float64 `json:"percent"`
}

type Metrics struct {
	NodeID      string            `json:"node_id"`
	Status      string            `json:"status"`
	LastSuccess *time.Time        `json:"last_success,omitempty"`
	Stale       bool              `json:"stale"`
	CPU         *float64          `json:"cpu"`
	GPU         *float64          `json:"gpu"`
	Memory      *Memory           `json:"memory"`
	Disks       []Disk            `json:"disks"`
	Errors      map[string]string `json:"errors"`
}

type SSHCheck struct {
	OK      bool        `json:"ok"`
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Command string      `json:"command"`
	Steps   []SetupStep `json:"steps"`
}

type SetupStep struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
}
