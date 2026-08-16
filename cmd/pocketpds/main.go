package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alesr/pocketPDS/internal/blob"
	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/server"
	"github.com/alesr/pocketPDS/internal/tunnel"
	"golang.org/x/sync/errgroup"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"serve"}, args...)
	}
	switch args[0] {
	case "serve":
		if err := serve(args[1:]); err != nil {
			slog.Error("serve", "err", err)
			os.Exit(1)
		}
	case "accounts":
		if err := accounts(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "pocketpds accounts:", err)
			os.Exit(1)
		}
	case "tunnel":
		if err := tunnelCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "pocketpds tunnel:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("pocketpds 0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "usage: pocketpds [serve] [accounts] [tunnel] [version]\n")
		os.Exit(2)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	seed := fs.Bool("seed", false, "create a dev account (dev.example.com / password) if none exists")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	cfg := config.FromEnv()

	slog.SetDefault(makeLogger(cfg.LogLevel))

	if cfg.Secret == "" {
		slog.Warn("POCKETPDS_SECRET is unset; using an insecure development key. Set it before exposing this instance.")
	}

	ctx := context.Background()

	store, err := db.Open(ctx, cfg.DatabasePath, cfg.Secret)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = store.Close() }()

	settings, err := store.LoadSettings(ctx)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	cfg.ApplySettings(settings)

	if *seed {
		if err := store.SeedDevAccount(ctx, "dev.example.com", "password", cfg.PublicURL); err != nil {
			return fmt.Errorf("seed dev account: %w", err)
		}
	}

	tunnels := tunnel.New(cfg.DataDir)
	tunnels.Start()
	defer tunnels.Stop()

	srv, err := server.New(cfg, store, tunnels)
	if err != nil {
		return fmt.Errorf("init server: %w", err)
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	g, ctx := errgroup.WithContext(context.Background())

	g.Go(func() error {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-sigCtx.Done()
		if ctx.Err() != nil {
			return nil // group canceled by a server error, not a signal
		}
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	})

	return g.Wait()
}

func accounts(args []string) error {
	if len(args) > 0 && args[0] == "delete" {
		if len(args) < 2 {
			return fmt.Errorf("usage: pocketpds accounts delete <handle>")
		}
		return deleteAccount(args[1])
	}

	cfg := config.FromEnv()
	store, err := db.Open(context.Background(), cfg.DatabasePath, cfg.Secret)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = store.Close() }()

	rows, err := store.DB.Query("SELECT did, handle, email, deactivated_at FROM accounts ORDER BY created_at")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	fmt.Printf("%-45s %-30s %-25s %s\n", "DID", "HANDLE", "EMAIL", "ACTIVE")
	for rows.Next() {
		var did, handle string
		var email, deactivated *string
		if err := rows.Scan(&did, &handle, &email, &deactivated); err != nil {
			continue
		}
		active := "yes"
		if deactivated != nil {
			active = "no"
		}
		em := ""
		if email != nil {
			em = *email
		}
		fmt.Printf("%-45s %-30s %-25s %s\n", did, handle, em, active)
	}
	return rows.Err()
}

func deleteAccount(handle string) error {
	cfg := config.FromEnv()
	store, err := db.Open(context.Background(), cfg.DatabasePath, cfg.Secret)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = store.Close() }()

	mgr := repo.NewManager(store)
	blobs, err := blob.New(filepath.Join(cfg.DataDir, "blobs"), store)
	if err != nil {
		return err
	}
	mgr.SetBlobStore(blobs)

	var did string
	if err := store.DB.QueryRow("SELECT did FROM accounts WHERE handle = ?", handle).Scan(&did); err != nil {
		return fmt.Errorf("no account with handle %q", handle)
	}

	if err := mgr.DeleteAccount(context.Background(), did); err != nil {
		return err
	}

	res, err := store.DB.Exec("DELETE FROM accounts WHERE handle = ?", handle)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no account with handle %q", handle)
	}
	fmt.Printf("deleted account %q\n", handle)
	return nil
}

func makeLogger(lvl slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// tunnelCmd installs or removes the cloudflared binary used by the supervised
// Cloudflare Tunnel. install must run as root (or a user with write access to
// the target directory).
func tunnelCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pocketpds tunnel [install|uninstall]")
	}
	switch args[0] {
	case "install":
		target := tunnel.BinaryPath()
		fmt.Printf("installing cloudflared to %s ...\n", target)
		if err := tunnel.Install(context.Background(), target); err != nil {
			return fmt.Errorf("install cloudflared: %w", err)
		}
		fmt.Println("installed cloudflared", tunnel.BinaryPath())
		return nil
	case "uninstall":
		target := tunnel.BinaryPath()
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Printf("removed %s\n", target)
		return nil
	default:
		return fmt.Errorf("unknown tunnel subcommand %q", args[0])
	}
}
