// Package sshutil runs the current user's system OpenSSH client. It never accepts
// a shell command supplied by an HTTP caller.
package sshutil

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"dgx-hub/internal/model"
	"dgx-hub/internal/platform"
)

var targetPart = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:%\[\]-]*$`)
var userPart = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
var hostPart = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func ValidateTarget(target string) error {
	if target == "" || len(target) > 255 || strings.TrimSpace(target) != target {
		return errors.New("SSH target must be an alias, hostname, IP address, or user@host")
	}
	parts := strings.Split(target, "@")
	if len(parts) > 2 || (len(parts) == 2 && !userPart.MatchString(parts[0])) {
		return errors.New("invalid SSH username or target")
	}
	host := parts[len(parts)-1]
	if strings.HasPrefix(host, "[") != strings.HasSuffix(host, "]") {
		return errors.New("unbalanced IP address brackets")
	}
	plain := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	ip := plain
	if at := strings.LastIndex(ip, "%"); at >= 0 {
		if !hostPart.MatchString(ip[at+1:]) {
			return errors.New("invalid IP zone")
		}
		ip = ip[:at]
	}
	if net.ParseIP(ip) != nil {
		return nil
	}
	if !targetPart.MatchString(host) {
		return errors.New("SSH target contains unsupported characters")
	}
	return nil
}

func ValidateRemoteHost(host string) error {
	if host == "" || len(host) > 253 || strings.TrimSpace(host) != host {
		return errors.New("remote host must be a hostname or IP address")
	}
	plain := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.HasPrefix(host, "[") != strings.HasSuffix(host, "]") {
		return errors.New("unbalanced IP address brackets")
	}
	ip := plain
	if at := strings.LastIndex(ip, "%"); at >= 0 {
		if !hostPart.MatchString(ip[at+1:]) {
			return errors.New("invalid IP zone")
		}
		ip = ip[:at]
	}
	if net.ParseIP(ip) != nil {
		return nil
	}
	if !hostPart.MatchString(host) || strings.Contains(host, "..") {
		return errors.New("remote host contains unsupported characters")
	}
	return nil
}

// BaseArgs also prevents a background process from waiting for a password or
// accepting a previously unknown host key. First-use trust is handled in a terminal.
func BaseArgs() []string {
	return []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=6", "-o", "ConnectionAttempts=1", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no", "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none", "-o", "ForwardX11=no", "-o", "ForwardAgent=no"}
}

func Command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = 2 * time.Second
	platform.Background(cmd)
	return cmd
}

// Buffer safely captures a bounded amount of concurrent subprocess output.
type Buffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const limit = 64 * 1024
	if len(b.data) < limit {
		n := min(len(p), limit-len(b.data))
		b.data = append(b.data, p[:n]...)
	}
	return len(p), nil
}
func (b *Buffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }

func Run(ctx context.Context, args []string, stdin io.Reader) (string, string, error) {
	cmd := Command(ctx, args...)
	var out, stderr Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &stderr
	err := cmd.Run()
	return out.String(), stderr.String(), err
}

// Error carries an actionable failure category across configuration checks and
// real SSH operations without losing the distinction between setup and network.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

// ValidateBackgroundConfig inspects effective system SSH configuration without
// connecting. DGX Hub owns its foreground ssh subprocesses and its loopback-only
// forwards, so inherited forwarding or automatic detachment is not accepted.
// Inspecting -G avoids passing newer options to older Windows OpenSSH clients.
func ValidateBackgroundConfig(ctx context.Context, target string) error {
	if err := ValidateTarget(target); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := append(BaseArgs(), "-G", target, "true")
	out, stderr, err := Run(ctx, args, nil)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		code, message := ClassifyError(err, stderr)
		return &Error{Code: code, Message: message}
	}
	if len(out) >= 64*1024 {
		return &Error{Code: "ssh_config_error", Message: "Effective SSH configuration is too large to validate safely. Use a smaller dedicated SSH alias for DGX Hub."}
	}
	return parseBackgroundConfig(out)
}

func parseBackgroundConfig(output string) error {
	foundHost := false
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key, value := strings.ToLower(fields[0]), strings.ToLower(fields[1])
		switch key {
		case "hostname":
			foundHost = true
		case "localforward", "remoteforward", "dynamicforward":
			return &Error{Code: "ssh_config_error", Message: "SSH setup required: this target inherits " + fields[0] + ". Create a clean SSH alias for DGX Hub without LocalForward, RemoteForward, or DynamicForward entries; DGX Hub manages its own loopback-only tunnels."}
		case "forkafterauthentication":
			if value == "yes" {
				return &Error{Code: "ssh_config_error", Message: "SSH setup required: ForkAfterAuthentication must be disabled for this target. Use a clean SSH alias so DGX Hub can own and stop its background SSH processes."}
			}
		case "stdinnull":
			if value == "yes" {
				return &Error{Code: "ssh_config_error", Message: "SSH setup required: StdinNull must be disabled for this target because DGX Hub sends fixed telemetry input over SSH."}
			}
		case "sessiontype":
			if value != "default" {
				return &Error{Code: "ssh_config_error", Message: "SSH setup required: use the default SessionType for this target so DGX Hub can run its fixed monitoring commands."}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return &Error{Code: "ssh_config_error", Message: "Could not read the effective SSH configuration: " + err.Error()}
	}
	if !foundHost {
		return &Error{Code: "ssh_config_error", Message: "OpenSSH did not return a valid effective configuration. Check the selected SSH target and OpenSSH Client installation."}
	}
	return nil
}

func ClassifyError(err error, stderr string) (string, string) {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Code, classified.Message
	}
	message := strings.TrimSpace(stderr)
	lower := strings.ToLower(message)
	var missing *exec.Error
	switch {
	case errors.As(err, &missing) && errors.Is(missing.Err, exec.ErrNotFound):
		return "ssh_missing", "OpenSSH Client was not found. Install the Windows OpenSSH Client optional feature."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout", "The SSH operation timed out or was cancelled. Check the node address and network connection."
	case strings.Contains(lower, "host key verification failed"), strings.Contains(lower, "remote host identification has changed"), strings.Contains(lower, "no host key is known"):
		return "host_key_required", "SSH host-key verification needs attention. Verify the server fingerprint in a terminal before continuing."
	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "authentication failed"), strings.Contains(lower, "no supported authentication"):
		return "authentication_required", "SSH setup required: configure key authentication and load any protected key into ssh-agent."
	case strings.Contains(lower, "could not resolve hostname"), strings.Contains(lower, "connection refused"), strings.Contains(lower, "network is unreachable"), strings.Contains(lower, "no route to host"), strings.Contains(lower, "connection timed out"):
		return "network_error", "The SSH node is unreachable. Check its SSH target, power, network connection, and SSH service."
	default:
		if message == "" && err != nil {
			message = err.Error()
		}
		if message == "" {
			message = "The SSH operation failed."
		}
		if len(message) > 1200 {
			message = message[:1200]
		}
		return "ssh_error", message
	}
}

func SetupSteps(target string) []model.SetupStep {
	sshCommand := "ssh " + target
	keyCommand := "ssh-keygen -t ed25519"
	agentCommand := "ssh-add ~/.ssh/id_ed25519"
	publicCommand := "cat ~/.ssh/id_ed25519.pub"
	if runtime.GOOS == "windows" {
		agentCommand = "ssh-add \"$env:USERPROFILE\\.ssh\\id_ed25519\""
		publicCommand = "Get-Content \"$env:USERPROFILE\\.ssh\\id_ed25519.pub\""
	}
	steps := []model.SetupStep{
		{Title: "Local computer: check OpenSSH Client", Description: "Run this in your local PowerShell or terminal. On Windows, install OpenSSH Client in Optional Features if it is missing.", Command: "ssh -V"},
		{Title: "Verify the node, then return to your local computer", Description: "Run this locally. Compare the host-key fingerprint with a trusted value from the server administrator before accepting it. Enter the server password in the terminal. After a successful login, type exit to return to your LOCAL computer before continuing.", Command: sshCommand},
		{Title: "Local computer: choose or create a key", Description: "Run this on your LOCAL computer, not on the Spark. Reuse an existing suitable key when available. Otherwise create an Ed25519 key with a passphrase. Do not overwrite an existing key.", Command: keyCommand},
		{Title: "Local computer: copy your PUBLIC key", Description: "Run this locally and copy the entire .pub line. Use the actual public-key path if you chose a different key. The matching file without .pub is private and must remain on your local computer.", Command: publicCommand},
		{Title: "Remote node: authorize the public key", Description: "Connect again using " + sshCommand + ". In that REMOTE Linux shell, run the command below. Paste the public-key line on a new line in nano, preserving existing keys. Save with Ctrl+O, Enter, and Ctrl+X. Use another editor if nano is unavailable.", Command: "mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys\nnano ~/.ssh/authorized_keys"},
		{Title: "Remote node: return to your local computer", Description: "Run exit in the remote shell. The remaining agent and test commands must run on your LOCAL computer.", Command: "exit"},
	}
	if runtime.GOOS == "windows" {
		steps = append(steps, model.SetupStep{Title: "Local Windows: enable the SSH agent", Description: "If the agent is disabled, run this once in a LOCAL Administrator PowerShell. Load your key in your normal user PowerShell afterward.", Command: "Set-Service -Name ssh-agent -StartupType Automatic\nStart-Service ssh-agent"})
	}
	steps = append(steps,
		model.SetupStep{Title: "Local computer: load your protected key", Description: "Run this in your LOCAL normal-user terminal. Use the private-key path you selected and enter its passphrase here. On Linux, first ensure your desktop's ssh-agent is running.", Command: agentCommand},
		model.SetupStep{Title: "Local computer: check background access", Description: "Run this locally. It must finish without requesting a password, passphrase, or host-key confirmation. Then press Check connection again in DGX Hub.", Command: "ssh -o BatchMode=yes -o StrictHostKeyChecking=yes " + target + " true"},
	)
	return steps
}

func Check(ctx context.Context, target string) model.SSHCheck {
	result := model.SSHCheck{}
	if err := ValidateTarget(target); err != nil {
		result.Code, result.Message = "invalid_target", err.Error()
		return result
	}
	result.Command, result.Steps = "ssh "+target, SetupSteps(target)
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	if err := ValidateBackgroundConfig(ctx, target); err != nil {
		result.Code, result.Message = ClassifyError(err, "")
		if result.Code == "ssh_config_error" {
			result.Steps = append([]model.SetupStep{{Title: "Local computer: use a clean SSH alias", Description: "Review your LOCAL ~/.ssh/config (on Windows: %USERPROFILE%\\.ssh\\config). Keep the node's HostName, User, Port, IdentityFile, and any needed ProxyJump. Use an alias that does not inherit LocalForward, RemoteForward, DynamicForward, ForkAfterAuthentication yes, StdinNull yes, or a non-default SessionType. DGX Hub owns its own tunnels. Preserve other applications' aliases."}}, result.Steps...)
		}
		return result
	}
	args := append(BaseArgs(), "-o", "ClearAllForwardings=yes", target, "true")
	_, stderr, err := Run(ctx, args, nil)
	if err == nil {
		result.OK, result.Code, result.Message = true, "ready", "SSH key authentication is ready for background connections."
		return result
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	result.Code, result.Message = ClassifyError(err, stderr)
	return result
}

// ShellQuote is used only for a fixed application-owned remote script. User data
// is sent on stdin and is never interpolated into this command.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

const tcpProbeScript = `IFS= read -r remote_host
IFS= read -r remote_port
exec timeout 4 bash -c 'exec 3<>"/dev/tcp/$1/$2"' dgx-hub "$remote_host" "$remote_port"
`

// ProbeTCP checks a destination from the SSH node's perspective without making
// HTTP requests or handling the service's authentication and WebSocket traffic.
func ProbeTCP(ctx context.Context, target, host string, port int) error {
	if err := ValidateTarget(target); err != nil {
		return err
	}
	if err := ValidateRemoteHost(host); err != nil {
		return err
	}
	if port < 1 || port > 65535 {
		return errors.New("remote port must be between 1 and 65535")
	}
	if err := ValidateBackgroundConfig(ctx, target); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	args := append(BaseArgs(), "-o", "ClearAllForwardings=yes", target, "sh -c "+ShellQuote(tcpProbeScript))
	_, stderr, err := Run(ctx, args, strings.NewReader(fmt.Sprintf("%s\n%d\n", host, port)))
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		_, message := ClassifyError(err, stderr)
		return fmt.Errorf("remote endpoint %s is unavailable: %s", net.JoinHostPort(host, fmt.Sprint(port)), message)
	}
	return nil
}
