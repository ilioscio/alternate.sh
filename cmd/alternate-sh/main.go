package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
	"github.com/ilioscio/alternate.sh/internal/server"
)

func main() {
	root := &cobra.Command{
		Use:   "alternate-sh",
		Short: "alternate.sh — a retro Unix timeshare social network",
	}

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the server",
		RunE:  runServe,
	}
	serve.Flags().String("config", "config.toml", "path to config file")
	root.AddCommand(serve)

	adduser := &cobra.Command{
		Use:   "adduser",
		Short: "Create a new user account",
		RunE:  runAdduser,
	}
	adduser.Flags().String("config", "config.toml", "path to config file")
	adduser.Flags().String("username", "", "username")
	adduser.Flags().String("password", "", "password (min 8 chars)")
	adduser.Flags().String("name", "", "display name")
	adduser.Flags().StringArray("pubkey", nil, "SSH public key(s) to authorize (can repeat)")
	adduser.Flags().Bool("admin", false, "grant admin privileges")
	adduser.MarkFlagRequired("username")
	root.AddCommand(adduser)

	setpw := &cobra.Command{
		Use:   "setpassword",
		Short: "Set a user's password (admin operation, no old password required)",
		RunE:  runSetpassword,
	}
	setpw.Flags().String("config", "config.toml", "path to config file")
	setpw.Flags().String("username", "", "username")
	setpw.Flags().String("password", "", "new password (min 8 chars)")
	setpw.MarkFlagRequired("username")
	setpw.MarkFlagRequired("password")
	root.AddCommand(setpw)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.Database.DSN, cfg.Database.MaxConns)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	db.StartJanitor(ctx, pool)

	hub := presence.NewHub()
	sshSrv := server.NewSSH(cfg, pool, hub)
	wsSrv := server.NewWebSocket(cfg, pool, hub)

	errCh := make(chan error, 3)
	go func() { errCh <- sshSrv.ListenAndServe() }()
	go func() { errCh <- wsSrv.ListenAndServe() }()

	if cfg.Federation.Enabled {
		fedSrv, err := server.NewFederation(ctx, cfg, pool, hub)
		if err != nil {
			return fmt.Errorf("federation: %w", err)
		}
		go func() { errCh <- fedSrv.ListenAndServe() }()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		fmt.Println("\nshutting down...")
		return nil
	}
}

func runAdduser(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.Database.DSN, cfg.Database.MaxConns)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	username, _ := cmd.Flags().GetString("username")
	password, _ := cmd.Flags().GetString("password")
	displayName, _ := cmd.Flags().GetString("name")
	pubkeys, _ := cmd.Flags().GetStringArray("pubkey")
	admin, _ := cmd.Flags().GetBool("admin")

	var hash string
	if password != "" {
		if len(password) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hashing password: %w", err)
		}
		hash = string(h)
	}

	u, err := db.CreateUser(ctx, pool, username, hash, displayName, admin)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}

	fmt.Printf("created user %s (id: %s)\n", u.Username, u.ID)
	if admin {
		fmt.Println("  [admin]")
	}

	for _, key := range pubkeys {
		if err := db.AddSSHKey(ctx, pool, u.ID, key); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not add pubkey: %v\n", err)
		} else {
			fmt.Printf("  pubkey added: %s...\n", key[:min(40, len(key))])
		}
	}
	return nil
}

func runSetpassword(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.Database.DSN, cfg.Database.MaxConns)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	username, _ := cmd.Flags().GetString("username")
	password, _ := cmd.Flags().GetString("password")

	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	u, err := db.GetUserByUsername(ctx, pool, username)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	if err := db.UpdatePassword(ctx, pool, u.ID, string(hash)); err != nil {
		return fmt.Errorf("updating password: %w", err)
	}
	fmt.Printf("password updated for %s\n", username)
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
