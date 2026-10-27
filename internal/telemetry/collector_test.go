package telemetry

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dgx-hub/internal/model"
)

const sample = `DGX_HUB_METRICS_V1
CPU1	100	0	50	850	0	0	0	0
MEM	1000	400
DISK	root	1000	600	/
DISK	data	1000	600	/
GPU	N/A
CPU2	110	0	60	880	0	0	0	0
`

func testCollector() *Collector {
	c := New()
	c.checkConfig = func(context.Context, string) error { return nil }
	return c
}

func TestParseDeltaCPUUnifiedMemoryAndDeduplicateDisks(t *testing.T) {
	m, err := Parse(sample, "node", "/srv/ai")
	if err != nil {
		t.Fatal(err)
	}
	if m.CPU == nil || *m.CPU != 40 {
		t.Fatalf("CPU must use the interval delta: %+v", m.CPU)
	}
	if m.Memory == nil || m.Memory.Total != 1000 || m.Memory.Used != 600 || m.Memory.Percent != 60 {
		t.Fatalf("memory: %+v", m.Memory)
	}
	if m.GPU != nil || m.Errors["gpu"] == "" {
		t.Fatalf("unsupported GPU must be unavailable, not 0: %+v", m)
	}
	if len(m.Disks) != 1 || m.Disks[0].Mount != "/" {
		t.Fatalf("same filesystem duplicated: %+v", m.Disks)
	}
}
func TestParseKeepsSeparateDataFilesystem(t *testing.T) {
	input := strings.Replace(sample, "DISK\tdata\t1000\t600\t/", "DISK\tdata\t2000\t1000\t/data volume", 1)
	m, err := Parse(input, "node", "/srv/ai")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Disks) != 2 || m.Disks[1].Path != "/srv/ai" || m.Disks[1].Mount != "/data volume" {
		t.Fatalf("data filesystem lost: %+v", m.Disks)
	}
}

func TestParseDeduplicatesBindMountsOnSameFilesystem(t *testing.T) {
	input := strings.Replace(sample, "DISK\troot\t1000\t600\t/", "DISK\troot\t1000\t600\t/\t42", 1)
	input = strings.Replace(input, "DISK\tdata\t1000\t600\t/", "DISK\tdata\t1000\t600\t/srv/ai\t42", 1)
	m, err := Parse(input, "node", "/srv/ai")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Disks) != 1 {
		t.Fatalf("bind mount duplicated system filesystem: %+v", m.Disks)
	}
}
func TestParseRealGPUZeroIsAvailable(t *testing.T) {
	m, err := Parse(strings.Replace(sample, "GPU\tN/A", "GPU\t0", 1), "node", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.GPU == nil || *m.GPU != 0 || m.Errors["gpu"] != "" {
		t.Fatalf("real GPU zero was discarded: %+v", m)
	}
}
func TestParseInvalidCountersNeverInventMetrics(t *testing.T) {
	input := strings.Replace(sample, "CPU2\t110", "CPU2\t99", 1)
	input = strings.Replace(input, "MEM\t1000\t400", "MEM\t1000\t1001", 1)
	m, err := Parse(input, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.CPU != nil || m.Memory != nil || m.Errors["cpu"] == "" || m.Errors["memory"] == "" {
		t.Fatalf("invalid readings must be unavailable: %+v", m)
	}
	if _, err := Parse("not telemetry", "node", ""); err == nil {
		t.Fatal("missing protocol marker accepted")
	}
}
func TestPollPreservesLastReadingAsStaleAfterSSHFailure(t *testing.T) {
	c := testCollector()
	node := model.Node{ID: "node", SSHTarget: "elly", DataPath: "/srv/ai"}
	c.run = func(context.Context, []string, io.Reader) (string, string, error) { return sample, "", nil }
	first := c.Poll(context.Background(), node)
	if first.LastSuccess == nil || first.Status != "online" {
		t.Fatalf("first poll: %+v", first)
	}
	c.run = func(context.Context, []string, io.Reader) (string, string, error) {
		return "", "ssh: connect to host elly: Connection refused", errors.New("exit status 255")
	}
	failed := c.Poll(context.Background(), node)
	if failed.Status != "offline" || !failed.Stale || failed.LastSuccess == nil || !failed.LastSuccess.Equal(*first.LastSuccess) || failed.CPU == nil || *failed.CPU != 40 {
		t.Fatalf("last valid data must stay stale: %+v", failed)
	}
	failed.Errors["gpu"] = "mutated"
	if c.Get("node").Errors["gpu"] == "mutated" {
		t.Fatal("cache was exposed to callers")
	}
}
func TestGetAndOtherNodesDoNotBlockOnOfflinePoll(t *testing.T) {
	c := testCollector()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c.run = func(context.Context, []string, io.Reader) (string, string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return sample, "", nil
	}
	go func() { defer close(done); c.Poll(context.Background(), model.Node{ID: "slow", SSHTarget: "elly"}) }()
	<-started
	readDone := make(chan struct{})
	go func() { c.Get("slow"); close(readDone) }()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Get blocked on SSH operation")
	}
	other := c.Poll(context.Background(), model.Node{ID: "fast", SSHTarget: "evie"})
	if other.Status != "online" {
		t.Errorf("other node blocked: %+v", other)
	}
	// A second poll of the same node returns cached state instead of starting SSH.
	c.Poll(context.Background(), model.Node{ID: "slow", SSHTarget: "elly"})
	if calls.Load() != 2 {
		t.Errorf("overlapping polls started %d commands", calls.Load())
	}
	close(release)
	<-done
}
func TestDataPathIsStdinDataNotRemoteCommand(t *testing.T) {
	c := testCollector()
	path := "/srv/it's $(not-a-command); safe"
	c.run = func(_ context.Context, args []string, stdin io.Reader) (string, string, error) {
		for _, arg := range args {
			if strings.Contains(arg, path) {
				t.Fatal("data path interpolated into command")
			}
		}
		data, _ := io.ReadAll(stdin)
		if string(data) != path+"\n" {
			t.Fatalf("incorrect stdin data %q", data)
		}
		return sample, "", nil
	}
	m := c.Poll(context.Background(), model.Node{ID: "node", SSHTarget: "elly", DataPath: path})
	if m.Status != "online" {
		t.Fatal(m.Errors)
	}
}
