// Command skra is a self-hosted contacts application served as a single static
// binary backed by one SQLite file.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/johannesheinz/skra/internal/config"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/web"
)

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "skra:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: skra <command>\n\ncommands:\n  serve    run the HTTP server")
	}

	switch args[1] {
	case "serve":
		return serve()
	default:
		return fmt.Errorf("unknown command %q (try: serve)", args[1])
	}
}

func serve() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return web.New(cfg.Listen, logger).Run(ctx)
}
