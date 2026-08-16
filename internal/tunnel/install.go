package tunnel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Install downloads the cloudflared static binary for the current platform and
// installs it at target (mode 0755). It must run with write access to the
// target directory (usually as root).
func Install(ctx context.Context, target string) error {
	asset, err := assetName()
	if err != nil {
		return err
	}
	url := "https://github.com/cloudflare/cloudflared/releases/latest/download/" + asset

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download cloudflared: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download cloudflared: %s", res.Status)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".cloudflared-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, res.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

func assetName() (string, error) {
	var arch string
	switch runtime.GOARCH {
	case "arm64":
		arch = "arm64"
	case "amd64":
		arch = "amd64"
	case "386":
		arch = "386"
	case "arm":
		arch = "arm"
	default:
		return "", fmt.Errorf("unsupported architecture %q", runtime.GOARCH)
	}
	return fmt.Sprintf("cloudflared-%s-%s", runtime.GOOS, arch), nil
}
