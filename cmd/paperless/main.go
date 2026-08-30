package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"paperless/internal/app"
	"paperless/internal/config"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "paperless: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root := flag.NewFlagSet("paperless", flag.ContinueOnError)
	root.SetOutput(os.Stderr)
	configPath := root.String("config", config.DefaultPath(), "config TOML path")
	if err := root.Parse(args); err != nil {
		return err
	}
	rest := root.Args()
	if len(rest) == 0 {
		usage()
		return nil
	}

	command := rest[0]
	commandArgs := rest[1:]

	switch command {
	case "version":
		fmt.Printf("paperless %s\n", version)
		return nil
	case "configure":
		fs := flag.NewFlagSet("configure", flag.ContinueOnError)
		force := fs.Bool("force", false, "overwrite existing config")
		base := fs.String("base", "", "runtime base folder for inbox/raw/processing/archive/review/rejected/duplicates/logs")
		inbox := fs.String("inbox", "", "scanner inbox folder")
		archive := fs.String("archive", "", "Dropbox archive root")
		stateDir := fs.String("state-dir", "", "local state directory")
		if err := fs.Parse(commandArgs); err != nil {
			return err
		}
		cfg := config.Default()
		if *base != "" {
			cfg.Paths.Inbox = filepath.Join(*base, "inbox")
			cfg.Paths.Raw = filepath.Join(*base, "raw")
			cfg.Paths.Processing = filepath.Join(*base, "processing")
			cfg.Paths.Archive = filepath.Join(*base, "archive")
			cfg.Paths.Review = filepath.Join(*base, "review")
			cfg.Paths.Rejected = filepath.Join(*base, "rejected")
			cfg.Paths.Duplicates = filepath.Join(*base, "duplicates")
			cfg.Paths.Logs = filepath.Join(*base, "logs")
		}
		if *inbox != "" {
			cfg.Paths.Inbox = *inbox
		}
		if *archive != "" {
			cfg.Paths.ArchiveRoot = *archive
		}
		if *stateDir != "" {
			cfg.Paths.StateDir = *stateDir
		}
		path, err := config.WriteDefault(*configPath, cfg, *force)
		if err != nil {
			return err
		}
		fmt.Printf("Wrote config: %s\n", path)
		return nil
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		skipInstall := fs.Bool("skip-install", false, "skip Homebrew tool/model installation")
		if err := fs.Parse(commandArgs); err != nil {
			return err
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		if !*skipInstall {
			if err := app.InstallRuntimeDependencies(context.Background(), cfg, os.Stdout, os.Stderr); err != nil {
				return err
			}
		}
		if err := app.Init(cfg); err != nil {
			return err
		}
		fmt.Printf("Initialized Paperless at %s\n", cfg.Paths.StateDir)
		app.PrintInboxSetup(os.Stdout, cfg)
		return nil
	case "doctor":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		failed := false
		for _, check := range app.Doctor(cfg) {
			status := "ok"
			if !check.OK {
				status = "missing"
				failed = true
			}
			fmt.Printf("%-7s %-24s %s\n", status, check.Name, check.Detail)
		}
		if failed {
			return errors.New("one or more checks failed")
		}
		return nil
	case "process-once":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		count, err := app.ProcessOnce(context.Background(), cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Processed %d file(s)\n", count)
		return nil
	case "dry-run":
		if len(commandArgs) != 1 {
			return errors.New("usage: paperless dry-run <pdf-or-image>")
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		result, err := app.DryRunFile(context.Background(), cfg, commandArgs[0])
		if err != nil {
			return err
		}
		fmt.Printf("Suggested path: %s\n", result.SuggestedPath)
		fmt.Printf("Auto-file:      %v\n", result.WouldAutoFile)
		fmt.Printf("Confidence:     %.2f\n", result.Classification.Confidence)
		fmt.Printf("Paper action:   %s\n", result.Classification.PhysicalOriginalAction)
		fmt.Printf("OCR text:       %s\n", result.TextPath)
		return nil
	case "serve":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		return runWithSignals(func(ctx context.Context) error {
			return app.Serve(ctx, cfg)
		})
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		inbox := fs.String("inbox", "", "override watched inbox folder")
		if err := fs.Parse(commandArgs); err != nil {
			return err
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		if *inbox != "" {
			abs, err := filepath.Abs(*inbox)
			if err != nil {
				return err
			}
			cfg.Paths.Inbox = abs
		}
		return runWithSignals(func(ctx context.Context) error {
			return app.Run(ctx, cfg)
		})
	case "service":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		return app.ServiceCommand(cfg, *configPath, commandArgs)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func runWithSignals(run func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return run(ctx)
}

func usage() {
	fmt.Print(`paperless

Usage:
  paperless version
  paperless [--config path] configure [--force] [--base path] [--inbox path] [--archive path] [--state-dir path]
  paperless [--config path] init [--skip-install]
  paperless [--config path] doctor
  paperless [--config path] process-once
  paperless [--config path] dry-run <pdf-or-image>
  paperless [--config path] serve
  paperless [--config path] run [--inbox path]
  paperless [--config path] service install|uninstall|start|stop|status
`)
}
