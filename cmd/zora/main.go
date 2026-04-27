package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zora/internal/blobs"
	"zora/internal/config"
	"zora/internal/db"
	"zora/internal/extract"
	"zora/internal/ingest"
	"zora/internal/jobs"
	"zora/internal/server"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "version":
		return runVersion(stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runServe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config/example.yaml", "path to YAML config file")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}

	if err := config.EnsureLocalPaths(cfg); err != nil {
		fmt.Fprintf(stderr, "prepare local paths: %v\n", err)
		return 1
	}

	database, err := db.Open(cfg.SQLite.Path)
	if err != nil {
		fmt.Fprintf(stderr, "open sqlite: %v\n", err)
		return 1
	}
	defer database.Close()

	if err := db.Migrate(context.Background(), database); err != nil {
		fmt.Fprintf(stderr, "migrate sqlite: %v\n", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var ingestService *ingest.Service
	if cfg.Ingest.Enabled {
		extractor, err := buildExtractor(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "configure extractor: %v\n", err)
			return 1
		}
		jobStore := jobs.Store{DB: database}
		ingestService = &ingest.Service{
			Scanner: ingest.Scanner{
				DB:             database,
				Jobs:           jobStore,
				Inbox:          cfg.Paths.Inbox,
				SettleDuration: cfg.Ingest.SettleDuration,
				MaxAttempts:    cfg.Ingest.MaxAttempts,
			},
			Jobs: jobStore,
			Handler: ingest.FileHandler{
				DB:        database,
				Blobs:     blobs.Store{ArchiveRoot: cfg.Paths.Archive},
				Extractor: extractor,
				Owner:     cfg.User.ID,
			},
			ScanInterval: cfg.Ingest.ScanInterval,
			Workers:      cfg.Ingest.Workers,
			Logger:       logger,
		}
		ingestService.Start(ctx)
	}

	handler, err := server.New(cfg, database, logger, ingestService)
	if err != nil {
		fmt.Fprintf(stderr, "create server: %v\n", err)
		return 1
	}

	httpServer := &http.Server{
		Addr:              cfg.Server.Bind,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("zora serving", "bind", cfg.Server.Bind, "version", version, "commit", commit, "date", date)
		fmt.Fprintf(stdout, "Zora listening on http://%s\n", cfg.Server.Bind)
		errs <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(stderr, "serve: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			fmt.Fprintf(stderr, "shutdown: %v\n", err)
			return 1
		}
	}

	return 0
}

func runVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "zora %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
	return 0
}

func buildExtractor(cfg config.Config) (extract.Extractor, error) {
	local := extract.LocalExtractor{Timeout: cfg.Extract.Timeout}
	switch cfg.Extract.Provider {
	case "local":
		return local, nil
	case "docling":
		return extract.Router{
			Text: local,
			Binary: extract.DoclingExtractor{
				BaseURL:         cfg.Extract.Docling.BaseURL,
				APIKey:          cfg.Extract.Docling.APIKey,
				OutputFormats:   cfg.Extract.Docling.OutputFormats,
				DoOCR:           cfg.Extract.Docling.DoOCR,
				ForceOCR:        cfg.Extract.Docling.ForceOCR,
				TableMode:       cfg.Extract.Docling.TableMode,
				ImageExportMode: cfg.Extract.Docling.ImageExportMode,
				Timeout:         cfg.Extract.Timeout,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown extract provider %q", cfg.Extract.Provider)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  zora serve --config config/example.yaml")
	fmt.Fprintln(w, "  zora version")
}
