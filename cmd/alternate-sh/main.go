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
	adduser.Flags().String("password", "", "password")
	adduser.Flags().String("name", "", "display name")
	adduser.Flags().Bool("admin", false, "grant admin privileges")
	adduser.MarkFlagRequired("username")
	adduser.MarkFlagRequired("password")
	root.AddCommand(adduser)

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

	pool, err := db.Connect(ctx, cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	hub := presence.NewHub()
	sshSrv := server.NewSSH(cfg, pool, hub)
	wsSrv := server.NewWebSocket(cfg, pool, hub)

	errCh := make(chan error, 2)
	go func() { errCh <- sshSrv.ListenAndServe() }()
	go func() { errCh <- wsSrv.ListenAndServe() }()

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
	pool, err := db.Connect(ctx, cfg.Database.DSN)
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
	admin, _ := cmd.Flags().GetBool("admin")

	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	u, err := db.CreateUser(ctx, pool, username, string(hash), displayName, admin)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}

	fmt.Printf("created user %s (id: %s)\n", u.Username, u.ID)
	if admin {
		fmt.Println("  [admin]")
	}
	return nil
}
