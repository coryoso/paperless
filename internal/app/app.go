package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"paperless/internal/bonsai"
	"paperless/internal/config"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
	"paperless/internal/embeddings"
	"paperless/internal/fm"
	"paperless/internal/ocr"
)

type Check struct {
	Name   string
	OK     bool
	Detail string
}

func Init(cfg config.Config) error {
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	store, err := db.Open(context.Background(), cfg.DBPath())
	if err != nil {
		return err
	}
	defer store.Close()
	for _, folder := range cfg.Policy.KnownFolders {
		now := db.Now()
		if err := store.Queries.UpsertFolder(context.Background(), sqlc.UpsertFolderParams{
			Path:      folder,
			Source:    "config",
			FirstSeen: now,
			LastSeen:  now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func Doctor(cfg config.Config) []Check {
	checks := []Check{}
	for _, name := range []string{"tesseract", "pdftoppm", "pdftotext", "pdfinfo", "pdfimages", "qpdf"} {
		path, err := exec.LookPath(name)
		checks = append(checks, Check{Name: name, OK: err == nil, Detail: detail(path, err)})
	}
	if available, err := ocr.AvailableLanguages(context.Background()); err == nil {
		for _, language := range cfg.OCR.Languages {
			checks = append(checks, Check{
				Name:   "ocr.language." + language,
				OK:     available[language],
				Detail: languageDetail(language, available[language]),
			})
		}
	} else {
		checks = append(checks, Check{Name: "ocr.languages", OK: false, Detail: err.Error()})
	}
	if cfg.LLM.Enabled && cfg.LLM.Provider == "ollama" {
		ollamaOK := ollamaReachable(context.Background(), cfg)
		checks = append(checks, Check{Name: "ollama", OK: ollamaOK, Detail: cfg.LLM.Endpoint})
		models, err := ollamaModelList(context.Background(), cfg)
		if err != nil {
			model := strings.TrimSpace(cfg.LLM.Model)
			if model == "" || isQwen35Selector(model) {
				model = defaultOllamaInstallModel
			}
			checks = append(checks, Check{Name: "ollama.model." + cfg.LLM.Model, OK: false, Detail: "unavailable; start Ollama and run `ollama pull " + model + "`"})
		} else {
			ok, detail := ollamaModelDetail(cfg.LLM.Model, models)
			checks = append(checks, Check{Name: "ollama.model." + cfg.LLM.Model, OK: ok, Detail: detail})
		}
	} else if cfg.LLM.Enabled && cfg.LLM.Provider == "fm" {
		err := fm.Available(context.Background())
		modelDetail := "Apple Foundation Models: on-device system model available"
		if err != nil {
			modelDetail = err.Error()
		}
		checks = append(checks, Check{Name: "fm.model.system", OK: err == nil, Detail: modelDetail})
	} else if cfg.LLM.Enabled && cfg.LLM.Provider == "bonsai" {
		err := bonsai.Available(context.Background(), cfg.Bonsai)
		modelDetail := cfg.Bonsai.Model + " available at " + cfg.Bonsai.Endpoint
		if err != nil {
			modelDetail = err.Error()
		}
		checks = append(checks, Check{Name: "bonsai.model." + cfg.Bonsai.Model, OK: err == nil, Detail: modelDetail})
	} else {
		checks = append(checks, Check{Name: "llm", OK: !cfg.LLM.Enabled, Detail: "disabled or unsupported provider"})
	}
	if cfg.Embeddings.Enabled {
		_, err := (embeddings.Client{Config: cfg.Embeddings}).Identity(context.Background())
		modelDetail := cfg.Embeddings.Model + " at " + cfg.Embeddings.Endpoint
		if err != nil {
			modelDetail = err.Error()
		}
		checks = append(checks, Check{Name: "embeddings.model." + cfg.Embeddings.Model, OK: err == nil, Detail: modelDetail})
	}
	for _, dir := range cfg.RuntimeDirs() {
		checks = append(checks, Check{Name: "path", OK: exists(dir), Detail: dir})
	}
	checks = append(checks, Check{Name: "archive_root", OK: exists(cfg.Paths.ArchiveRoot), Detail: cfg.Paths.ArchiveRoot})
	return checks
}

func languageDetail(language string, ok bool) string {
	if ok {
		return "installed"
	}
	return "missing; run `brew install tesseract-lang` or remove " + language + " from ocr.languages"
}

func detail(path string, err error) string {
	if err != nil {
		return "not found on PATH"
	}
	return path
}

func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func Serve(ctx context.Context, cfg config.Config, configPath string) error {
	return reloadOnSetupChanges(ctx, cfg, configPath, serveDashboard)
}

func Run(ctx context.Context, cfg config.Config, configPath string) error {
	return reloadOnSetupChanges(ctx, cfg, configPath, runService)
}

var errRestartRequested = errors.New("reload saved configuration")

// Setup also works when launched from a terminal, without a service manager
// restarting the process after every saved choice.
func reloadOnSetupChanges(ctx context.Context, cfg config.Config, path string, run func(context.Context, config.Config, string) error) error {
	for {
		err := run(ctx, cfg, path)
		if !errors.Is(err, errRestartRequested) {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		cfg, err = config.Load(path)
		if err != nil {
			return err
		}
	}
}
