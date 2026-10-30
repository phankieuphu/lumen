// Command lumen starts the desktop SQL client.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/phankieuphu/lumen/internal/app"
	"github.com/phankieuphu/lumen/ui/shell"
)

// version is set at build time: -ldflags "-X main.version=v0.0.1".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lumen:", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := flag.String("data-dir", "", "directory for the local database (default: user config dir)")
	debug := flag.Bool("debug", false, "verbose logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.Open(ctx, app.Config{DataDir: *dataDir, Version: version, Logger: log})
	if err != nil {
		return err
	}
	runErr := shell.Run(ctx, a, log)

	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Close(closeCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
	return runErr
}
