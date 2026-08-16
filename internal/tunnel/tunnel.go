// Package tunnel supervises a cloudflared process that fronts the PDS over a
// Cloudflare Tunnel. It writes the tunnel credentials and config into the PDS
// data directory and runs `cloudflared tunnel run` as a child process, so a
// single systemd unit manages both the PDS and its tunnel.
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/alesr/pocketPDS/internal/cloudflare"
)

const defaultBinaryPath = "/usr/local/bin/cloudflared"

// BinaryPath returns the cloudflared executable path. Overridable via
// POCKETPDS_CLOUDFLARED_BIN for portability (e.g. a bundled binary).
func BinaryPath() string {
	if v := os.Getenv("POCKETPDS_CLOUDFLARED_BIN"); v != "" {
		return v
	}
	return defaultBinaryPath
}

// Manager supervises a cloudflared child process.
type Manager struct {
	dataDir string
	binPath string

	mu         sync.Mutex
	cmd        *exec.Cmd
	running    bool
	generation int
	cancel     context.CancelFunc
	done       chan struct{}
}

// New returns a Manager that stores tunnel config under dataDir/cloudflared.
func New(dataDir string) *Manager {
	return &Manager{dataDir: dataDir, binPath: BinaryPath()}
}

func (m *Manager) configDir() string  { return filepath.Join(m.dataDir, "cloudflared") }
func (m *Manager) configPath() string { return filepath.Join(m.configDir(), "config.yml") }
func (m *Manager) credsPath() string  { return filepath.Join(m.configDir(), "credentials.json") }

// Installed reports whether the cloudflared binary is present.
func (m *Manager) Installed() bool {
	_, err := os.Stat(m.binPath)
	return err == nil
}

// Ready reports whether the tunnel can run: config present and the cloudflared
// binary installed.
func (m *Manager) Ready() bool {
	if _, err := os.Stat(m.configPath()); err != nil {
		return false
	}
	if _, err := os.Stat(m.binPath); err != nil {
		return false
	}
	return true
}

// Running reports whether the cloudflared child is currently up.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// LoadCredentials reads previously persisted tunnel credentials, if any.
func (m *Manager) LoadCredentials() (cloudflare.TunnelCredentials, bool) {
	b, err := os.ReadFile(m.credsPath())
	if err != nil {
		return cloudflare.TunnelCredentials{}, false
	}
	var c cloudflare.TunnelCredentials
	if err := json.Unmarshal(b, &c); err != nil {
		return cloudflare.TunnelCredentials{}, false
	}
	return c, true
}

// Provision writes the cloudflared credentials and config for the given
// hostname, forwarding traffic to the local PDS listener port.
func (m *Manager) Provision(creds cloudflare.TunnelCredentials, hostname string, port int) error {
	dir := m.configDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	credsJSON, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.credsPath(), credsJSON, 0o600); err != nil {
		return err
	}

	cfg := fmt.Sprintf(`tunnel: %s
credentials-file: %s
protocol: http2
ingress:
  - hostname: %s
    service: http://127.0.0.1:%d
  - service: http_status:404
`, creds.TunnelID, m.credsPath(), hostname, port)

	return os.WriteFile(m.configPath(), []byte(cfg), 0o644)
}

// Restart bounces the running child so it picks up new config. It is a no-op
// when no tunnel is configured or installed.
func (m *Manager) Restart() {
	m.mu.Lock()
	m.generation++
	m.mu.Unlock()
	m.signal(syscall.SIGTERM)
}

// Start launches the supervisor goroutine. Safe to call once.
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		cancel()
		return
	}
	m.cancel = cancel
	m.done = make(chan struct{})
	m.mu.Unlock()
	go m.run(ctx)
}

// Stop terminates the supervisor and its child. It waits for the child to exit
// gracefully, then force-kills it if it does not stop in time.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	done := m.done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.signal(syscall.SIGTERM)
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		m.signal(syscall.SIGKILL)
		<-done
	}
}

func (m *Manager) signal(sig syscall.Signal) {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(sig)
	}
}

func (m *Manager) run(ctx context.Context) {
	defer close(m.done)
	for ctx.Err() == nil {
		m.mu.Lock()
		gen := m.generation
		m.mu.Unlock()

		if !m.Ready() {
			if !m.wait(ctx, 2*time.Second) {
				return
			}
			continue
		}

		log := &lineWriter{}
		cmd := exec.Command(m.binPath, "--no-autoupdate", "tunnel", "--config", m.configPath(), "run")
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Start(); err != nil {
			slog.Error("tunnel: start failed", "err", err)
			if !m.wait(ctx, 2*time.Second) {
				return
			}
			continue
		}

		m.mu.Lock()
		m.cmd = cmd
		m.running = true
		m.mu.Unlock()

		_ = cmd.Wait()

		m.mu.Lock()
		m.cmd = nil
		m.running = false
		changed := m.generation != gen
		m.mu.Unlock()

		if ctx.Err() != nil {
			return
		}
		if changed {
			continue
		}
		slog.Warn("tunnel: cloudflared exited; restarting")
		if !m.wait(ctx, 2*time.Second) {
			return
		}
	}
}

func (m *Manager) wait(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// lineWriter forwards cloudflared's log lines to slog.
type lineWriter struct {
	mu   sync.Mutex
	rest []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rest = append(w.rest, p...)
	for {
		i := bytes.IndexByte(w.rest, '\n')
		if i < 0 {
			break
		}
		if i > 0 {
			slog.Info("cloudflared", "msg", string(w.rest[:i]))
		}
		w.rest = w.rest[i+1:]
	}
	return len(p), nil
}
