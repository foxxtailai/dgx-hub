package telemetry

import (
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"dgx-hub/internal/model"
)

const connectXMaxAge = 30 * time.Second

var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

func emptyConnectX() *model.ConnectX {
	return &model.ConnectX{Status: "unknown", Interfaces: []model.ConnectXInterface{}, Reachable: []model.ConnectXReachability{}, Peers: []model.ConnectXPeer{}}
}

func cloneConnectX(value *model.ConnectX) *model.ConnectX {
	if value == nil {
		return emptyConnectX()
	}
	v := *value
	v.Interfaces = append([]model.ConnectXInterface{}, value.Interfaces...)
	for i := range v.Interfaces {
		v.Interfaces[i].Addresses = append([]string{}, v.Interfaces[i].Addresses...)
	}
	v.Reachable = append([]model.ConnectXReachability{}, value.Reachable...)
	v.Peers = append([]model.ConnectXPeer{}, value.Peers...)
	for i := range v.Peers {
		v.Peers[i].Links = append([]model.ConnectXLink{}, v.Peers[i].Links...)
	}
	return &v
}

func freshConnectX(m model.Metrics, now time.Time) bool {
	return m.Status == "online" && !m.Stale && m.LastSuccess != nil && now.Sub(*m.LastSuccess) <= connectXMaxAge && m.ConnectX != nil
}

// Probe only addresses advertised by other monitored ConnectX interfaces on
// directly attached subnets. No SSH aliases, hostnames or shell code enter stdin.
func (c *Collector) connectXTargets(id string, now time.Time) string {
	c.mu.Lock()
	states := make(map[string]*nodeState, len(c.nodes))
	for key, value := range c.nodes {
		states[key] = value
	}
	c.mu.Unlock()
	readings := make(map[string]model.Metrics, len(states))
	for key, state := range states {
		state.mu.RLock()
		readings[key] = clone(state.value)
		state.mu.RUnlock()
	}
	return connectXTargets(id, readings, now)
}

func connectXTargets(id string, readings map[string]model.Metrics, now time.Time) string {
	local := readings[id]
	// The previous local inventory remains useful for recovery; the remote script
	// rechecks hardware and carrier before every probe.
	if local.ConnectX == nil {
		return ""
	}
	var targets []string
	for peerID, peer := range readings {
		if peerID == id || !freshConnectX(peer, now) {
			continue
		}
		for _, source := range local.ConnectX.Interfaces {
			if !interfaceName.MatchString(source.Name) {
				continue
			}
			for _, dest := range peer.ConnectX.Interfaces {
				if dest.State != "up" || dest.MAC == source.MAC {
					continue
				}
				// Try both families so a blocked IPv4 path does not hide IPv6.
				addresses := slices.Clone(dest.Addresses)
				slices.Sort(addresses)
				matched := map[bool]bool{}
				for _, address := range addresses {
					remote, err := netip.ParsePrefix(address)
					if err != nil || !validConnectXIP(remote.Addr()) || matched[remote.Addr().Is4()] {
						continue
					}
					for _, ownAddress := range source.Addresses {
						own, err := netip.ParsePrefix(ownAddress)
						if err == nil && own.Addr() != remote.Addr() && own.Contains(remote.Addr()) && remote.Contains(own.Addr()) {
							targets = append(targets, source.Name+" "+remote.Addr().String()+"\n")
							matched[remote.Addr().Is4()] = true
							break
						}
					}
				}
			}
		}
	}
	slices.Sort(targets)
	targets = slices.Compact(targets)
	if len(targets) > 16 {
		targets = targets[:16]
	}
	return strings.Join(targets, "")
}

func validConnectXIP(ip netip.Addr) bool {
	return ip.IsValid() && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast() && ip.Zone() == ""
}

func canonicalMAC(raw string) string {
	mac, err := net.ParseMAC(raw)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || slices.Equal(mac, []byte{0, 0, 0, 0, 0, 0}) {
		return ""
	}
	return mac.String()
}

func parseConnectX(p []string, m *model.Metrics) {
	cx := m.ConnectX
	invalid := func() { m.Errors["connectx"] = "Invalid ConnectX reading" }
	switch p[0] {
	case "CX_BEGIN":
		cx.Status = "not_detected"
	case "CX_IF":
		if len(p) != 5 || !interfaceName.MatchString(p[1]) || canonicalMAC(p[2]) == "" || (p[3] != "up" && p[3] != "down" && p[3] != "unknown") {
			invalid()
			return
		}
		for _, iface := range cx.Interfaces {
			if iface.Name == p[1] {
				invalid()
				return
			}
		}
		speed, _ := strconv.Atoi(p[4])
		if speed < 0 || speed > 1000000 {
			speed = 0
		}
		cx.Interfaces = append(cx.Interfaces, model.ConnectXInterface{Name: p[1], MAC: canonicalMAC(p[2]), State: p[3], SpeedMbps: speed, Addresses: []string{}})
	case "CX_ADDR":
		if len(p) != 3 {
			invalid()
			return
		}
		prefix, err := netip.ParsePrefix(p[2])
		if err != nil || !validConnectXIP(prefix.Addr()) {
			invalid()
			return
		}
		for i := range cx.Interfaces {
			if cx.Interfaces[i].Name == p[1] {
				cx.Interfaces[i].Addresses = append(cx.Interfaces[i].Addresses, prefix.String())
				return
			}
		}
		invalid()
	case "CX_REACH":
		if len(p) != 4 {
			invalid()
			return
		}
		ip, err := netip.ParseAddr(p[2])
		if !interfaceName.MatchString(p[1]) || err != nil || !validConnectXIP(ip) || canonicalMAC(p[3]) == "" {
			invalid()
			return
		}
		cx.Reachable = append(cx.Reachable, model.ConnectXReachability{Interface: p[1], Address: ip.String(), MAC: canonicalMAC(p[3])})
	}
}

// Resolve only registered nodes with fresh inventories. A live, interface-bound
// ICMP reply AND the neighbor's ConnectX MAC/IP identify the peer; carrier alone
// and cached ARP/NDISC entries never establish a connection.
func ResolveConnectX(local model.Metrics, readings map[string]model.Metrics, now time.Time) *model.ConnectX {
	cx := cloneConnectX(local.ConnectX)
	cx.Peers = []model.ConnectXPeer{}
	if !freshConnectX(local, now) {
		cx.Status = "unknown"
		return cx
	}
	if len(cx.Interfaces) == 0 {
		return cx
	}
	cx.Status = "disconnected"
	for _, source := range cx.Interfaces {
		if source.State == "unknown" && cx.Status == "disconnected" {
			cx.Status = "unknown"
		}
		if source.State != "up" {
			continue
		}
		cx.Status = "link_up"
	}
	peerIDs := make([]string, 0, len(readings))
	for id := range readings {
		peerIDs = append(peerIDs, id)
	}
	slices.Sort(peerIDs)
	for _, id := range peerIDs {
		peer := readings[id]
		if id == local.NodeID || !freshConnectX(peer, now) {
			continue
		}
		links := []model.ConnectXLink{}
		seen := map[string]bool{}
		for _, source := range cx.Interfaces {
			if source.State != "up" {
				continue
			}
			for _, dest := range peer.ConnectX.Interfaces {
				if dest.State != "up" || dest.MAC == source.MAC {
					continue
				}
				for _, reach := range cx.Reachable {
					if reach.Interface != source.Name || reach.MAC != dest.MAC {
						continue
					}
					for _, address := range dest.Addresses {
						prefix, err := netip.ParsePrefix(address)
						key := source.Name + ":" + dest.Name
						if err == nil && prefix.Addr().String() == reach.Address && !seen[key] {
							speed := source.SpeedMbps
							if speed == 0 || (dest.SpeedMbps > 0 && dest.SpeedMbps < speed) {
								speed = dest.SpeedMbps
							}
							links = append(links, model.ConnectXLink{Interface: source.Name, PeerInterface: dest.Name, SpeedMbps: speed})
							seen[key] = true
						}
					}
				}
			}
		}
		if len(links) > 0 {
			cx.Peers = append(cx.Peers, model.ConnectXPeer{NodeID: id, Links: links})
		}
	}
	if len(cx.Peers) > 0 {
		cx.Status = "connected"
	}
	return cx
}

// Uses sysfs, iproute2 and bounded parallel ICMP reads, without sudo, remote SSH
// or network reconfiguration. Spark's logical interfaces are discovered rather
// than hardcoded. Targets arrive only through stdin after data_path.
const connectXScript = `
if command -v ip >/dev/null 2>&1; then
  printf 'CX_BEGIN\n'
  for nic in /sys/class/net/*; do
    [ "$(cat "$nic/device/vendor" 2>/dev/null)" = '0x15b3' ] || continue
    case "$(readlink "$nic/device/driver" 2>/dev/null)" in */mlx5_core) ;; *) continue ;; esac
    dev=${nic##*/}
    mac=$(cat "$nic/address" 2>/dev/null)
    carrier=$(cat "$nic/carrier" 2>/dev/null)
    state=unknown
    case "$carrier" in 1) state=up ;; 0) state=down ;; esac
    if [ "$state" = unknown ]; then
      case "$(cat "$nic/operstate" 2>/dev/null)" in down|notpresent|lowerlayerdown) state=down ;; esac
    fi
    speed=$(cat "$nic/speed" 2>/dev/null)
    printf 'CX_IF\t%s\t%s\t%s\t%s\n' "$dev" "$mac" "$state" "$speed"
    ip -o address show dev "$dev" 2>/dev/null | awk -v dev="$dev" '$3 ~ /^inet6?$/ && $0 !~ / tentative| dadfailed/ {printf "CX_ADDR\t%s\t%s\n",dev,$4}'
  done
  if command -v ping >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
    timeout 2 sh -c '
      while read -r dev address; do
        [ -n "$dev" ] && [ -n "$address" ] || continue
        (
          nic=/sys/class/net/$dev
          [ "$(cat "$nic/device/vendor" 2>/dev/null)" = "0x15b3" ] || exit
          case "$(readlink "$nic/device/driver" 2>/dev/null)" in */mlx5_core) ;; *) exit ;; esac
          [ "$(cat "$nic/carrier" 2>/dev/null)" = 1 ] || exit
          if ping -n -c 1 -W 1 -I "$dev" "$address" >/dev/null 2>&1; then
            ip neigh show to "$address" dev "$dev" 2>/dev/null | awk -v dev="$dev" -v address="$address" '\''{for(i=1;i<NF;i++) if($i=="lladdr") printf "CX_REACH\t%s\t%s\t%s\n",dev,address,$(i+1)}'\''
          fi
        ) &
      done
      wait
    ' || printf 'ERROR\tconnectx\tConnectX reachability probes timed out\n'
  else
    printf 'ERROR\tconnectx\tiputils ping and coreutils timeout are required to identify connected nodes\n'
  fi
else
  printf 'ERROR\tconnectx\tiproute2 is unavailable\n'
fi
`
