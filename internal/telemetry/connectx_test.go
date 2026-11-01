package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dgx-hub/internal/model"
)

const cxInventory = "CX_BEGIN\nCX_IF\tenp1s0f0np0\t02:00:00:00:00:01\tup\t200000\nCX_ADDR\tenp1s0f0np0\t192.168.100.1/24\nCX_ADDR\tenp1s0f0np0\tfe80::1/64\n"

func cxReading(t *testing.T, id, inventory string, now time.Time) model.Metrics {
	t.Helper()
	m, err := Parse(sample+inventory, id, "")
	if err != nil {
		t.Fatal(err)
	}
	m.LastSuccess = &now
	return m
}

func cxPair(t *testing.T, now time.Time) (model.Metrics, model.Metrics) {
	local := cxReading(t, "a", cxInventory+"CX_REACH\tenp1s0f0np0\t192.168.100.2\t02:00:00:00:00:02\n", now)
	peer := cxReading(t, "b", strings.ReplaceAll(strings.ReplaceAll(cxInventory, "00:01", "00:02"), ".1/24", ".2/24"), now)
	return local, peer
}

func TestConnectXRequiresLiveIPAndMACMatch(t *testing.T) {
	now := time.Now()
	for _, name := range []string{"connected", "link only", "wrong MAC", "wrong IP", "link down", "peer link down", "peer offline", "peer stale", "peer expired", "local stale", "local expired", "peer removed", "self"} {
		t.Run(name, func(t *testing.T) {
			local, peer := cxPair(t, now)
			readings := map[string]model.Metrics{"b": peer}
			want := "link_up"
			switch name {
			case "connected":
				want = "connected"
			case "link only":
				local.ConnectX.Reachable = nil
			case "wrong MAC":
				local.ConnectX.Reachable[0].MAC = "02:00:00:00:00:09"
			case "wrong IP":
				local.ConnectX.Reachable[0].Address = "192.168.100.9"
			case "link down":
				local.ConnectX.Interfaces[0].State = "down"
				want = "disconnected"
			case "peer link down":
				peer.ConnectX.Interfaces[0].State = "down"
			case "peer offline":
				peer.Status = "offline"
				readings["b"] = peer
			case "peer stale":
				peer.Stale = true
				readings["b"] = peer
			case "peer expired":
				old := now.Add(-31 * time.Second)
				peer.LastSuccess = &old
				readings["b"] = peer
			case "local stale":
				local.Stale = true
				want = "unknown"
			case "local expired":
				old := now.Add(-31 * time.Second)
				local.LastSuccess = &old
				want = "unknown"
			case "peer removed":
				delete(readings, "b")
			case "self":
				local.NodeID = "b"
			}
			cx := ResolveConnectX(local, readings, now)
			if cx.Status != want || (len(cx.Peers) > 0) != (want == "connected") {
				t.Fatalf("got %+v; want %s", cx, want)
			}
			if want == "connected" && (cx.Peers[0].NodeID != "b" || len(cx.Peers[0].Links) != 1 || cx.Peers[0].Links[0].SpeedMbps != 200000) {
				t.Fatalf("wrong peer: %+v", cx.Peers)
			}
		})
	}
}

func TestConnectXMultipleRailsDeduplicatePeersAndIPv6(t *testing.T) {
	now := time.Now()
	local, peer := cxPair(t, now)
	peer.ConnectX.Interfaces[0].SpeedMbps = 100000
	// A second address on one rail does not count as a second link.
	peer.ConnectX.Interfaces[0].Addresses = append(peer.ConnectX.Interfaces[0].Addresses, "2001:db8::2/64")
	local.ConnectX.Reachable = append(local.ConnectX.Reachable, model.ConnectXReachability{Interface: "enp1s0f0np0", Address: "2001:db8::2", MAC: "02:00:00:00:00:02"})
	local.ConnectX.Interfaces = append(local.ConnectX.Interfaces, model.ConnectXInterface{Name: "enP2p1s0f0np0", MAC: "02:00:00:00:01:01", State: "up", SpeedMbps: 200000})
	peer.ConnectX.Interfaces = append(peer.ConnectX.Interfaces, model.ConnectXInterface{Name: "enP2p1s0f0np0", MAC: "02:00:00:00:01:02", State: "up", SpeedMbps: 200000, Addresses: []string{"fe80::abcd/64"}})
	local.ConnectX.Reachable = append(local.ConnectX.Reachable, model.ConnectXReachability{Interface: "enP2p1s0f0np0", Address: "fe80::abcd", MAC: "02:00:00:00:01:02"})
	cx := ResolveConnectX(local, map[string]model.Metrics{"b": peer}, now)
	if len(cx.Peers) != 1 || len(cx.Peers[0].Links) != 2 || cx.Peers[0].Links[0].SpeedMbps != 100000 {
		t.Fatalf("rails must remain separate without duplicating peers: %+v", cx)
	}
}

func TestConnectXParsingUnavailableAndMalformed(t *testing.T) {
	for _, tc := range []struct {
		records, status string
		invalid         bool
	}{
		{"", "unknown", false},
		{"CX_BEGIN\n", "not_detected", false},
		{cxInventory, "link_up", false},
		{strings.Replace(cxInventory, "\tup\t", "\tdown\t", 1), "disconnected", false},
		{strings.Replace(cxInventory, "\tup\t", "\tunknown\t", 1), "unknown", false},
		{"CX_BEGIN\nCX_IF\tbroken\n", "not_detected", true},
		{cxInventory + "CX_ADDR\tenp1s0f0np0\tnot-an-ip\n", "link_up", true},
		{cxInventory + "CX_REACH\tenp1s0f0np0\t127.0.0.1\t02:00:00:00:00:02\n", "link_up", true},
		{cxInventory + "CX_REACH\tenp1s0f0np0\t192.168.100.2\tff:ff:ff:ff:ff:ff\n", "link_up", true},
	} {
		m := cxReading(t, "a", tc.records, time.Now())
		if m.ConnectX.Status != tc.status || (m.Errors["connectx"] != "") != tc.invalid || m.CPU == nil {
			t.Fatalf("%q: %+v", tc.records, m)
		}
	}
}

func TestConnectXTargetsAreBoundedAndUseOnlyConnectXSubnets(t *testing.T) {
	now := time.Now()
	local, peer := cxPair(t, now)
	readings := map[string]model.Metrics{"a": local, "b": peer}
	if got := connectXTargets("a", readings, now); got != "enp1s0f0np0 192.168.100.2\n" {
		t.Fatalf("unexpected targets: %q", got)
	}
	peer.ConnectX.Interfaces[0].Addresses = []string{"192.168.100.2/24", "fe80::2/64"}
	if got := connectXTargets("a", readings, now); got != "enp1s0f0np0 192.168.100.2\nenp1s0f0np0 fe80::2\n" {
		t.Fatalf("both address families should be probed: %q", got)
	}
	peer.ConnectX.Interfaces[0].Addresses = []string{"10.1.1.2/24", "$(echo bad)/24", "127.0.0.1/8"}
	if got := connectXTargets("a", readings, now); got != "" {
		t.Fatalf("non-ConnectX subnet or invalid input accepted: %q", got)
	}
	peer.ConnectX.Interfaces[0].Addresses = []string{"fe80::2/64"}
	if got := connectXTargets("a", readings, now); got != "enp1s0f0np0 fe80::2\n" {
		t.Fatalf("link-local IPv6 must be scoped to the interface: %q", got)
	}
	peer.Stale = true
	readings["b"] = peer
	if got := connectXTargets("a", readings, now); got != "" {
		t.Fatal("probed stale peer")
	}
	for i := 2; i < 40; i++ {
		copy := clone(peer)
		copy.Stale = false
		copy.ConnectX.Interfaces[0].Addresses = []string{fmt.Sprintf("192.168.100.%d/24", i)}
		readings[fmt.Sprint(i)] = copy
	}
	if targets := connectXTargets("a", readings, now); strings.Count(targets, "\n") != 16 {
		t.Fatalf("probe fan-out is not bounded: %q", targets)
	}
}

func TestConnectXCacheIsolationStaleAndRecovery(t *testing.T) {
	c := testCollector()
	c.run = func(context.Context, []string, io.Reader) (string, string, error) {
		return sample + cxInventory, "", nil
	}
	node := model.Node{ID: "a", SSHTarget: "test"}
	first := c.Poll(context.Background(), node)
	first.ConnectX.Interfaces[0].Addresses[0] = "mutated"
	if c.Get("a").ConnectX.Interfaces[0].Addresses[0] != "192.168.100.1/24" {
		t.Fatal("Poll exposed cache")
	}
	copy := c.Get("a")
	copy.ConnectX.Interfaces[0].Name = "mutated"
	c.run = func(context.Context, []string, io.Reader) (string, string, error) {
		return "", "Connection refused", errors.New("SSH failed")
	}
	stale := c.Poll(context.Background(), node)
	if !stale.Stale || stale.ConnectX.Interfaces[0].Name != "enp1s0f0np0" || ResolveConnectX(stale, nil, time.Now()).Status != "unknown" {
		t.Fatalf("bad stale handling: %+v", stale)
	}
	c.run = func(context.Context, []string, io.Reader) (string, string, error) {
		return sample + "CX_BEGIN\n", "", nil
	}
	recovered := c.Poll(context.Background(), node)
	if recovered.Stale || len(recovered.ConnectX.Interfaces) != 0 || recovered.ConnectX.Status != "not_detected" {
		t.Fatalf("old links survived recovery: %+v", recovered)
	}
}

func TestConnectXScriptDiscoversHardwareAndRequiresSuccessfulProbe(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if _, statErr := os.Stat(`C:\Program Files\Git\bin\bash.exe`); statErr == nil {
		bash = `C:\Program Files\Git\bin\bash.exe`
		err = nil
	}
	if err != nil {
		t.Skip("bash is unavailable")
	}
	root := filepath.ToSlash(t.TempDir())
	// Bash understands /c/... on Windows; avoid a colon in its PATH.
	if len(root) > 2 && root[1] == ':' {
		root = "/" + strings.ToLower(root[:1]) + root[2:]
	}
	script := `set -eu
mkdir -p "$root/net/cx0/device" "$root/net/lan0/device" "$root/bin"
printf '0x15b3' > "$root/net/cx0/device/vendor"
printf '02:00:00:00:00:01' > "$root/net/cx0/address"
printf 1 > "$root/net/cx0/carrier"
printf 200000 > "$root/net/cx0/speed"
printf '0x10ec' > "$root/net/lan0/device/vendor"
cat > "$root/bin/readlink" <<'MOCK'
#!/bin/sh
case "$1" in */cx0/device/driver) printf '/drivers/mlx5_core\n' ;; *) exit 1 ;; esac
MOCK
cat > "$root/bin/ip" <<'MOCK'
#!/bin/sh
case "$*" in
  '-o address show dev cx0') printf '1: cx0 inet 192.168.100.1/24 scope global cx0\n' ;;
  'neigh show to 192.168.100.2 dev cx0') printf '192.168.100.2 lladdr 02:00:00:00:00:02 REACHABLE\n' ;;
  'neigh show to 192.168.100.3 dev cx0') printf '192.168.100.3 lladdr 02:00:00:00:00:03 STALE\n' ;;
esac
MOCK
cat > "$root/bin/ping" <<'MOCK'
#!/bin/sh
[ "$*" = '-n -c 1 -W 1 -I cx0 192.168.100.2' ]
MOCK
chmod +x "$root/bin/ip" "$root/bin/ping" "$root/bin/readlink"
export PATH="$root/bin:$PATH"
set +e
` + strings.ReplaceAll(connectXScript, "/sys/class/net", root+"/net")
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", "root='"+root+"'\n"+script)
	cmd.Stdin = strings.NewReader("cx0 192.168.100.2\ncx0 192.168.100.3\nlan0 192.168.100.2\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	m := cxReading(t, "a", string(out), time.Now())
	if len(m.ConnectX.Interfaces) != 1 || m.ConnectX.Interfaces[0].Name != "cx0" || len(m.ConnectX.Reachable) != 1 || m.ConnectX.Reachable[0].Address != "192.168.100.2" {
		t.Fatalf("must ignore management NIC and stale neighbor after failed ping: %s\n%+v", out, m.ConnectX)
	}
}
