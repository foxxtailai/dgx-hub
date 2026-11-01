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

type ConnectXInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	State     string   `json:"state"`
	SpeedMbps int      `json:"speed_mbps"`
	Addresses []string `json:"addresses"`
}

type ConnectXReachability struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	MAC       string `json:"mac"`
}

type ConnectXLink struct {
	Interface     string `json:"interface"`
	PeerInterface string `json:"peer_interface"`
	SpeedMbps     int    `json:"speed_mbps"`
}

type ConnectXPeer struct {
	NodeID string         `json:"node_id"`
	Links  []ConnectXLink `json:"links"`
}

type ConnectX struct {
	Status     string                 `json:"status"`
	Interfaces []ConnectXInterface    `json:"interfaces"`
	Reachable  []ConnectXReachability `json:"reachable"`
	Peers      []ConnectXPeer         `json:"peers"`
}

type Metrics struct {
	NodeID       string            `json:"node_id"`
	Status       string            `json:"status"`
	LastSuccess  *time.Time        `json:"last_success,omitempty"`
	Stale        bool              `json:"stale"`
	CPU          *float64          `json:"cpu"`
	GPU          *float64          `json:"gpu"`
	TemperatureC *float64          `json:"temperature_c"`
	PowerW       *float64          `json:"power_w"`
	Memory       *Memory           `json:"memory"`
	Disks        []Disk            `json:"disks"`
	ConnectX     *ConnectX         `json:"connectx"`
	Errors       map[string]string `json:"errors"`
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
