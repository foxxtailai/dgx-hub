package validation

import (
	"errors"
	"net/url"
	"strings"
	"unicode"

	"dgx-hub/internal/model"
	"dgx-hub/internal/sshutil"
)

func cleanText(s string, max int) bool {
	if len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func Node(n *model.Node) error {
	n.Name = strings.TrimSpace(n.Name)
	n.SSHTarget = strings.TrimSpace(n.SSHTarget)
	if n.Name == "" || !cleanText(n.Name, 120) {
		return errors.New("node name must contain 1–120 characters without control characters")
	}
	if err := sshutil.ValidateTarget(n.SSHTarget); err != nil {
		return err
	}
	if !cleanText(n.DataPath, 2048) || (n.DataPath != "" && !strings.HasPrefix(n.DataPath, "/")) {
		return errors.New("data path must be an absolute Linux path without control characters")
	}
	return nil
}

func Service(v *model.Service) error {
	v.Name = strings.TrimSpace(v.Name)
	v.RemoteHost = strings.TrimSpace(v.RemoteHost)
	if v.Name == "" || !cleanText(v.Name, 120) {
		return errors.New("service name must contain 1–120 characters without control characters")
	}
	if v.NodeID == "" {
		return errors.New("select a node")
	}
	if v.RemoteHost == "" {
		v.RemoteHost = "127.0.0.1"
	}
	if err := sshutil.ValidateRemoteHost(v.RemoteHost); err != nil {
		return err
	}
	if v.RemotePort < 1 || v.RemotePort > 65535 {
		return errors.New("service port must be between 1 and 65535")
	}
	if v.Scheme == "" {
		v.Scheme = "http"
	}
	if v.Scheme != "http" && v.Scheme != "https" {
		return errors.New("only HTTP and HTTPS services are supported")
	}
	if v.Path == "" {
		v.Path = "/"
	}
	u, err := url.ParseRequestURI(v.Path)
	if err != nil || !strings.HasPrefix(v.Path, "/") || strings.HasPrefix(v.Path, "//") || strings.Contains(v.Path, `\`) || !cleanText(v.Path, 2048) || u.IsAbs() || u.Host != "" {
		return errors.New("service path must be a local URL path starting with a single slash")
	}
	return nil
}
