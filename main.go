// Command skra is a self-hosted contacts application served as a single static binary backed by one SQLite file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/config"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/web"
)

// Version is the release version of Skrá.
const Version = "1.1.0"

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "skra:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return usageError()
	}

	switch args[1] {
	case "serve":
		return serve()
	case "create-admin":
		return createAdmin()
	case "backup":
		return backup(args[2:])
	case "version", "--version", "-v":
		fmt.Println("skra " + Version)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[1], usage())
	}
}

func usage() string {
	return "usage: skra <command>\n\ncommands:\n" +
		"  serve          run the HTTP server\n" +
		"  create-admin   create the initial admin account (first-run bootstrap)\n" +
		"  backup         write a consistent snapshot: skra backup --out <path>\n" +
		"  version        print the version"
}

func usageError() error {
	return fmt.Errorf("%s", usage())
}

func serve() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("starting skra", "version", Version)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()
	logger.Info("database ready", "path", cfg.DBPath)

	// One-time (idempotent) backfills of the denormalized columns for contacts that predate them; each is a no-op once every row has been visited.
	if n, err := models.BackfillBirthdays(context.Background(), database); err != nil {
		return err
	} else if n > 0 {
		logger.Info("backfilled birthdays", "contacts", n)
	}
	if n, err := models.BackfillSortKeys(context.Background(), database); err != nil {
		return err
	} else if n > 0 {
		logger.Info("backfilled sort keys", "contacts", n)
	}

	server, err := web.New(cfg, database, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return server.Run(ctx)
}

// backup writes a consistent VACUUM INTO snapshot of the database.
// It needs only SKRA_DB_PATH and a --out destination.
func backup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "destination path for the snapshot")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" {
		return fmt.Errorf("backup requires --out <path>")
	}
	dbPath := strings.TrimSpace(os.Getenv("SKRA_DB_PATH"))
	if dbPath == "" {
		return fmt.Errorf("backup requires SKRA_DB_PATH")
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := database.Snapshot(context.Background(), *out); err != nil {
		return err
	}
	fmt.Printf("wrote backup to %s\n", *out)
	return nil
}

// createAdmin bootstraps the first admin account.
// It reads credentials from the environment, refuses to run on a database that already has users, and uses only SKRA_DB_PATH (not the full serve configuration).
func createAdmin() error {
	dbPath := strings.TrimSpace(os.Getenv("SKRA_DB_PATH"))
	username := strings.TrimSpace(os.Getenv("SKRA_ADMIN_USERNAME"))
	email := strings.TrimSpace(os.Getenv("SKRA_ADMIN_EMAIL"))
	password := os.Getenv("SKRA_ADMIN_PASSWORD")

	var missing []string
	if dbPath == "" {
		missing = append(missing, "SKRA_DB_PATH")
	}
	if username == "" {
		missing = append(missing, "SKRA_ADMIN_USERNAME")
	}
	if email == "" {
		missing = append(missing, "SKRA_ADMIN_EMAIL")
	}
	if password == "" {
		missing = append(missing, "SKRA_ADMIN_PASSWORD")
	}
	if len(missing) > 0 {
		return fmt.Errorf("create-admin requires: %s", strings.Join(missing, ", "))
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx := context.Background()
	count, err := models.CountUsers(ctx, database)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("refusing to bootstrap: database already has %d user(s)", count)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	user, err := models.CreateUser(ctx, database, username, email, hash, models.RoleAdmin)
	if err != nil {
		return err
	}

	fmt.Printf("created admin %q (public_id %s)\n", user.Username, user.PublicID)
	return nil
}
