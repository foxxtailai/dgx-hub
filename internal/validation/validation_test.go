package validation

import (
	"dgx-hub/internal/model"
	"testing"
)

func TestUnsafeServiceConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.Service)
	}{
		{"port", func(v *model.Service) { v.RemotePort = 65536 }},
		{"scheme", func(v *model.Service) { v.Scheme = "javascript" }},
		{"redirect", func(v *model.Service) { v.Path = "//evil.example/" }},
		{"absolute", func(v *model.Service) { v.Path = "https://evil.example/" }},
		{"backslash", func(v *model.Service) { v.Path = `/\evil` }},
		{"newline", func(v *model.Service) { v.Path = "/\r\nLocation: https://evil.example" }},
		{"host option", func(v *model.Service) { v.RemoteHost = "-oProxyCommand=bad" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := model.Service{Name: "Web", NodeID: "node", RemoteHost: "127.0.0.1", RemotePort: 80, Scheme: "http", Path: "/"}
			tt.mutate(&v)
			if err := Service(&v); err == nil {
				t.Fatalf("accepted %+v", v)
			}
		})
	}
}
func TestDefaultsAndLinuxDataPath(t *testing.T) {
	v := model.Service{Name: " Web ", NodeID: "n", RemotePort: 80}
	if err := Service(&v); err != nil {
		t.Fatal(err)
	}
	if v.Path != "/" || v.RemoteHost != "127.0.0.1" || v.Scheme != "http" {
		t.Fatalf("missing defaults %+v", v)
	}
	n := model.Node{Name: "Elly", SSHTarget: "elly", DataPath: "relative"}
	if Node(&n) == nil {
		t.Fatal("accepted relative remote path")
	}
	n.DataPath = "/srv/AI data"
	if err := Node(&n); err != nil {
		t.Fatal(err)
	}
}
