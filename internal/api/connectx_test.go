package api

import (
	"encoding/json"
	"testing"
	"time"

	"dgx-hub/internal/model"
	"dgx-hub/internal/telemetry"
)

type clusterMetrics struct {
	fakeMetrics
	readings map[string]model.Metrics
}

func (c clusterMetrics) Get(id string) model.Metrics { return c.readings[id] }

func TestMetricsConnectXResolvesOnlyRegisteredFreshPeers(t *testing.T) {
	s, _ := setup(t)
	a, err := s.store.SaveNode(model.Node{Name: "Spark A", SSHTarget: "a"}, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.SaveNode(model.Node{Name: "Spark B", SSHTarget: "b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	makeReading := func(node model.Node, mac, addr string) model.Metrics {
		m, err := telemetry.Parse("DGX_HUB_METRICS_V1\nCX_BEGIN\nCX_IF\tcx0\t"+mac+"\tup\t200000\nCX_ADDR\tcx0\t"+addr+"/24\n", node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		m.LastSuccess = &now
		return m
	}
	local := makeReading(a, "02:00:00:00:00:01", "192.168.100.1")
	peer := makeReading(b, "02:00:00:00:00:02", "192.168.100.2")
	local.ConnectX.Reachable = []model.ConnectXReachability{{Interface: "cx0", Address: "192.168.100.2", MAC: "02:00:00:00:00:02"}}
	readings := map[string]model.Metrics{a.ID: local, b.ID: peer}
	s.metrics = clusterMetrics{readings: readings}
	get := func() model.Metrics {
		response := request(s, "GET", "/api/nodes/"+a.ID+"/metrics", "", false)
		var m model.Metrics
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &m) != nil || m.ConnectX == nil {
			t.Fatalf("metrics response: %d %s", response.Code, response.Body.String())
		}
		return m
	}
	if m := get(); m.ConnectX.Status != "connected" || len(m.ConnectX.Peers) != 1 || m.ConnectX.Peers[0].NodeID != b.ID {
		t.Fatalf("missing peer: %+v", m.ConnectX)
	}
	peer.Stale = true
	readings[b.ID] = peer
	if m := get(); m.ConnectX.Status != "link_up" || len(m.ConnectX.Peers) != 0 {
		t.Fatalf("stale peer still connected: %+v", m.ConnectX)
	}
	peer.Stale = false
	readings[b.ID] = peer
	if err := s.store.DeleteNode(b.ID); err != nil {
		t.Fatal(err)
	}
	if m := get(); m.ConnectX.Status != "link_up" || len(m.ConnectX.Peers) != 0 {
		t.Fatalf("deleted peer still connected: %+v", m.ConnectX)
	}
}
