package telemetry

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"dgx-hub/internal/model"
	"dgx-hub/internal/sshutil"
)

type nodeState struct {
	mu    sync.RWMutex
	poll  sync.Mutex
	value model.Metrics
}
type Collector struct {
	mu          sync.Mutex
	nodes       map[string]*nodeState
	slots       chan struct{}
	run         func(context.Context, []string, io.Reader) (string, string, error)
	checkConfig func(context.Context, string) error
}

func New() *Collector {
	return &Collector{nodes: make(map[string]*nodeState), slots: make(chan struct{}, 4), run: sshutil.Run, checkConfig: sshutil.ValidateBackgroundConfig}
}
func empty(id string) model.Metrics {
	return model.Metrics{NodeID: id, Status: "connecting", Disks: []model.Disk{}, Errors: map[string]string{}}
}
func (c *Collector) state(id string) *nodeState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state := c.nodes[id]; state != nil {
		return state
	}
	state := &nodeState{value: empty(id)}
	c.nodes[id] = state
	return state
}
func clone(m model.Metrics) model.Metrics {
	if m.CPU != nil {
		v := *m.CPU
		m.CPU = &v
	}
	if m.GPU != nil {
		v := *m.GPU
		m.GPU = &v
	}
	if m.TemperatureC != nil {
		v := *m.TemperatureC
		m.TemperatureC = &v
	}
	if m.PowerW != nil {
		v := *m.PowerW
		m.PowerW = &v
	}
	if m.Memory != nil {
		v := *m.Memory
		m.Memory = &v
	}
	if m.LastSuccess != nil {
		v := *m.LastSuccess
		m.LastSuccess = &v
	}
	m.Disks = append([]model.Disk{}, m.Disks...)
	copyErrors := make(map[string]string, len(m.Errors))
	for k, v := range m.Errors {
		copyErrors[k] = v
	}
	m.Errors = copyErrors
	return m
}
func (c *Collector) Get(id string) model.Metrics {
	s := c.state(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.value)
}
func (c *Collector) Remove(id string) { c.mu.Lock(); delete(c.nodes, id); c.mu.Unlock() }

func (c *Collector) Poll(ctx context.Context, node model.Node) model.Metrics {
	s := c.state(node.ID)
	if !s.poll.TryLock() {
		return c.Get(node.ID)
	}
	defer s.poll.Unlock()
	s.mu.Lock()
	if s.value.LastSuccess == nil {
		s.value.Status = "connecting"
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return c.failed(s, node.ID, ctx.Err(), "")
	}
	if err := sshutil.ValidateTarget(node.SSHTarget); err != nil {
		return c.failed(s, node.ID, err, err.Error())
	}
	if err := c.checkConfig(ctx, node.SSHTarget); err != nil {
		return c.failed(s, node.ID, err, "")
	}
	if strings.ContainsAny(node.DataPath, "\x00\r\n") || (node.DataPath != "" && !strings.HasPrefix(node.DataPath, "/")) {
		return c.failed(s, node.ID, errors.New("data path must be an absolute Linux path without control characters"), "")
	}
	args := append(sshutil.BaseArgs(), "-o", "ClearAllForwardings=yes", node.SSHTarget, "sh -c "+sshutil.ShellQuote(metricsScript))
	out, stderr, err := c.run(ctx, args, strings.NewReader(node.DataPath+"\n"))
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return c.failed(s, node.ID, err, stderr)
	}
	m, err := Parse(out, node.ID, node.DataPath)
	if err != nil {
		return c.failed(s, node.ID, err, err.Error())
	}
	now := time.Now().UTC()
	m.LastSuccess = &now
	m.Status = "online"
	s.mu.Lock()
	logChanges(node.ID, s.value.Errors, m.Errors)
	s.value = clone(m)
	s.mu.Unlock()
	return m
}
func (c *Collector) failed(s *nodeState, id string, err error, stderr string) model.Metrics {
	code, message := sshutil.ClassifyError(err, stderr)
	s.mu.Lock()
	defer s.mu.Unlock()
	m := clone(s.value)
	m.NodeID = id
	m.Status = "error"
	if code == "network_error" || code == "timeout" {
		m.Status = "offline"
	}
	m.Stale = m.LastSuccess != nil
	m.Errors["ssh"] = message
	m.Errors["ssh_code"] = code
	logChanges(id, s.value.Errors, m.Errors)
	s.value = clone(m)
	return m
}

func logChanges(nodeID string, previous, current map[string]string) {
	for field, message := range current {
		if field == "ssh_code" || previous[field] == message {
			continue
		}
		if len(message) > 1200 {
			message = message[:1200]
		}
		log.Printf("telemetry node=%q field=%q unavailable: %s", nodeID, field, message)
	}
}

// The command is constant; data_path arrives as stdin data and remains a quoted
// shell variable. All commands are unprivileged reads on the Linux SSH node.
const metricsScript = `IFS= read -r data_path
LC_ALL=C
export LC_ALL
printf 'DGX_HUB_METRICS_V1\n'
awk '/^cpu / {printf "CPU1"; for(i=2;i<=9;i++) printf "\t%s",$i; printf "\n"; exit}' /proc/stat 2>/dev/null || printf 'ERROR\tcpu\tCannot read /proc/stat\n'
awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2; found=1} END {if(t>0 && found) printf "MEM\t%.0f\t%.0f\n",t*1024,a*1024; else exit 1}' /proc/meminfo 2>/dev/null || printf 'ERROR\tmemory\tCannot read MemTotal and MemAvailable\n'
disk() {
  disk_output=$(df -B1 --output=size,used,pcent,target -- "$2" 2>&1)
  if [ "$?" -ne 0 ]; then
    printf 'ERROR\tdisk_%s\t' "$1"; printf '%s' "$disk_output" | tr '\n\t' '  '; printf '\n'; return
  fi
  device=$(stat -Lc %d -- "$2" 2>/dev/null)
  printf '%s\n' "$disk_output" | awk -v label="$1" -v device="$device" 'NR>1 {total=$1; used=$2; match($0,/^[ \t]*[0-9]+[ \t]+[0-9]+[ \t]+[0-9]+%[ \t]+/); mount=substr($0,RLENGTH+1); printf "DISK\t%s\t%s\t%s\t%s\t%s\n",label,total,used,mount,device}'
}
disk root /
if [ -n "$data_path" ]; then disk data "$data_path"; fi
if command -v nvidia-smi >/dev/null 2>&1; then
  gpu_output=$(timeout 3 nvidia-smi --query-gpu=utilization.gpu,temperature.gpu,power.draw --format=csv,noheader,nounits 2>&1)
  if [ "$?" -eq 0 ]; then printf '%s\n' "$gpu_output" | awk -F, '{printf "GPU\t%s\t%s\t%s\n",$1,$2,$3}'
  else
    gpu_error=$(printf '%s' "$gpu_output" | tr '\n\t' '  ')
    for field in gpu temperature power; do printf 'ERROR\t%s\t%s\n' "$field" "$gpu_error"; done
  fi
else for field in gpu temperature power; do printf 'ERROR\t%s\tnvidia-smi is not installed\n' "$field"; done; fi
sleep 0.3
awk '/^cpu / {printf "CPU2"; for(i=2;i<=9;i++) printf "\t%s",$i; printf "\n"; exit}' /proc/stat 2>/dev/null || printf 'ERROR\tcpu\tCannot read second /proc/stat sample\n'
`

func Parse(output, nodeID, dataPath string) (model.Metrics, error) {
	m := empty(nodeID)
	m.Status = "online"
	var first, second []uint64
	var seen bool
	gpuCount := 0
	mounts := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "DGX_HUB_METRICS_V1" {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) == 0 {
			continue
		}
		switch p[0] {
		case "CPU1", "CPU2":
			values, err := parseCPU(p[1:])
			if err != nil {
				m.Errors["cpu"] = err.Error()
				continue
			}
			if p[0] == "CPU1" {
				first = values
			} else {
				second = values
			}
		case "MEM":
			if len(p) != 3 {
				m.Errors["memory"] = "Invalid memory reading"
				continue
			}
			total, e1 := strconv.ParseUint(p[1], 10, 64)
			available, e2 := strconv.ParseUint(p[2], 10, 64)
			if e1 != nil || e2 != nil || total == 0 || available > total {
				m.Errors["memory"] = "Invalid MemTotal or MemAvailable"
				continue
			}
			m.Memory = &model.Memory{Total: total, Used: total - available, Percent: float64(total-available) / float64(total) * 100}
		case "GPU":
			gpuCount++
			// Accept the previous utilization-only record as well as the new
			// three-field record. Unsupported sensors are independent of utilization.
			if len(p) != 2 && len(p) != 4 {
				m.Errors["gpu"] = "Invalid GPU reading"
				m.Errors["temperature"] = "Invalid GPU reading"
				m.Errors["power"] = "Invalid GPU reading"
				continue
			}
			m.GPU = sensor(p[1], "gpu", "GPU utilization", 100, m.Errors)
			if len(p) == 4 {
				m.TemperatureC = sensor(p[2], "temperature", "Device temperature", math.Inf(1), m.Errors)
				m.PowerW = sensor(p[3], "power", "Device power", math.Inf(1), m.Errors)
			}
		case "DISK":
			if len(p) != 5 && len(p) != 6 {
				m.Errors["disk"] = "Invalid disk reading"
				continue
			}
			total, e1 := strconv.ParseUint(p[2], 10, 64)
			used, e2 := strconv.ParseUint(p[3], 10, 64)
			if e1 != nil || e2 != nil || total == 0 || used > total || p[4] == "" {
				m.Errors["disk_"+p[1]] = "Invalid disk usage"
				continue
			}
			filesystem := "mount:" + p[4]
			if len(p) == 6 && p[5] != "" {
				filesystem = "device:" + p[5]
			}
			if mounts[filesystem] {
				continue
			}
			mounts[filesystem] = true
			path := "/"
			if p[1] == "data" {
				path = dataPath
			}
			m.Disks = append(m.Disks, model.Disk{Path: path, Mount: p[4], Total: total, Used: used, Percent: float64(used) / float64(total) * 100})
		case "ERROR":
			if len(p) >= 3 {
				m.Errors[p[1]] = strings.Join(p[2:], " ")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return m, fmt.Errorf("invalid telemetry output: %w", err)
	}
	if !seen {
		return m, errors.New("The node did not return the expected Linux telemetry format")
	}
	if len(first) == 8 && len(second) == 8 {
		var deltaTotal, deltaIdle uint64
		valid := true
		for i := range first {
			if second[i] < first[i] {
				valid = false
				break
			}
			d := second[i] - first[i]
			deltaTotal += d
			if i == 3 || i == 4 {
				deltaIdle += d
			}
		}
		if valid && deltaTotal > 0 && deltaIdle <= deltaTotal {
			v := 100 * float64(deltaTotal-deltaIdle) / float64(deltaTotal)
			m.CPU = &v
		} else {
			m.Errors["cpu"] = "CPU counters did not advance or were reset"
		}
	} else if m.Errors["cpu"] == "" {
		m.Errors["cpu"] = "CPU samples are unavailable"
	}
	if m.Memory == nil && m.Errors["memory"] == "" {
		m.Errors["memory"] = "Memory reading is unavailable"
	}
	if gpuCount > 1 {
		m.GPU = nil
		m.TemperatureC = nil
		m.PowerW = nil
		for _, field := range []string{"gpu", "temperature", "power"} {
			m.Errors[field] = "Multiple GPU readings are not supported by this Spark dashboard"
		}
	}
	if m.GPU == nil && m.Errors["gpu"] == "" {
		m.Errors["gpu"] = "GPU utilization is unavailable"
	}
	if m.TemperatureC == nil && m.Errors["temperature"] == "" {
		m.Errors["temperature"] = "Device temperature is unavailable"
	}
	if m.PowerW == nil && m.Errors["power"] == "" {
		m.Errors["power"] = "Device power is unavailable"
	}
	if len(m.Disks) == 0 && m.Errors["disk"] == "" && m.Errors["disk_root"] == "" {
		m.Errors["disk"] = "Disk readings are unavailable"
	}
	return m, nil
}
func sensor(raw, field, label string, max float64, errs map[string]string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > max {
		errs[field] = label + " is unavailable: " + strings.TrimSpace(raw)
		return nil
	}
	return &v
}
func parseCPU(fields []string) ([]uint64, error) {
	if len(fields) != 8 {
		return nil, errors.New("Invalid CPU counters")
	}
	values := make([]uint64, 8)
	for i, v := range fields {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, errors.New("Invalid CPU counter")
		}
		values[i] = n
	}
	return values, nil
}
