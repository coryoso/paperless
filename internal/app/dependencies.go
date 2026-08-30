package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"paperless/internal/config"
	"paperless/internal/ocr"
)

const defaultOllamaInstallModel = "qwen3.5:9b-q4_K_M"

type ollamaTagsResponse struct {
	Models []ollamaModelInfo `json:"models"`
}

type ollamaModelInfo struct {
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
}

func InstallRuntimeDependencies(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) error {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(stdout, "Skipping Homebrew dependency install: macOS only.")
		return nil
	}
	formulas := missingBrewFormulas(ctx, cfg)
	if len(formulas) > 0 {
		brew, err := exec.LookPath("brew")
		if err != nil {
			return fmt.Errorf("Homebrew is required to install missing runtime tools (%s); install Homebrew or rerun init with --skip-install", strings.Join(formulas, ", "))
		}
		for _, formula := range formulas {
			fmt.Fprintf(stdout, "Installing %s with Homebrew...\n", formula)
			cmd := exec.CommandContext(ctx, brew, "install", formula)
			cmd.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1")
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("brew install %s failed: %w", formula, err)
			}
		}
	} else {
		fmt.Fprintln(stdout, "Runtime tools already installed.")
	}
	if cfg.LLM.Enabled && cfg.LLM.Provider == "ollama" {
		if err := ensureOllamaModel(ctx, cfg, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}

func missingBrewFormulas(ctx context.Context, cfg config.Config) []string {
	missing := []string{}
	if _, err := exec.LookPath("tesseract"); err != nil {
		missing = append(missing, "tesseract")
	}
	for _, tool := range []string{"pdftoppm", "pdftotext", "pdfinfo", "pdfimages"} {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, "poppler")
			break
		}
	}
	if _, err := exec.LookPath("qpdf"); err != nil {
		missing = append(missing, "qpdf")
	}
	if cfg.LLM.Enabled && cfg.LLM.Provider == "ollama" {
		if _, err := exec.LookPath("ollama"); err != nil {
			missing = append(missing, "ollama")
		}
	}
	if needsTesseractLanguageFormula(ctx, cfg.OCR.Languages) {
		missing = append(missing, "tesseract-lang")
	}
	slices.Sort(missing)
	return slices.Compact(missing)
}

func needsTesseractLanguageFormula(ctx context.Context, languages []string) bool {
	needsExtraLanguage := false
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language != "" && language != "eng" && language != "osd" {
			needsExtraLanguage = true
			break
		}
	}
	if !needsExtraLanguage {
		return false
	}
	available, err := ocr.AvailableLanguages(ctx)
	if err != nil {
		return true
	}
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language != "" && !available[language] {
			return true
		}
	}
	return false
}

func ensureOllamaModel(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) error {
	if _, err := exec.LookPath("ollama"); err != nil {
		return fmt.Errorf("ollama command is missing after dependency install")
	}
	models, err := ollamaModelList(ctx, cfg)
	configured := strings.TrimSpace(cfg.LLM.Model)
	if configured == "" || isQwen35Selector(configured) {
		if err == nil {
			if selected, ok := selectQwen35Model(models); ok {
				fmt.Fprintf(stdout, "Using installed Ollama model %s.\n", selected)
				return nil
			}
		}
		configured = defaultOllamaInstallModel
	} else if err == nil {
		if _, ok := selectExactOllamaModel(models, configured); ok {
			fmt.Fprintf(stdout, "Ollama model %s already installed.\n", configured)
			return nil
		}
	}
	fmt.Fprintf(stdout, "Pulling Ollama model %s...\n", configured)
	cmd := exec.CommandContext(ctx, "ollama", "pull", configured)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ollama pull %s failed: %w; start Ollama and rerun init", configured, err)
	}
	return nil
}

func ollamaModelDetail(configured string, models []ollamaModelInfo) (bool, string) {
	configured = strings.TrimSpace(configured)
	if configured == "" || isQwen35Selector(configured) {
		selected, ok := selectQwen35Model(models)
		if ok {
			return true, "using installed model " + selected
		}
		return false, "no installed Qwen 3.5 model; run `ollama pull " + defaultOllamaInstallModel + "`"
	}
	if _, ok := selectExactOllamaModel(models, configured); ok {
		return true, "installed"
	}
	return false, "missing; run `ollama pull " + configured + "` or set llm.model to qwen3.5"
}

func selectExactOllamaModel(models []ollamaModelInfo, configured string) (string, bool) {
	for _, model := range models {
		if model.Name == configured || model.Model == configured {
			return configured, true
		}
	}
	return "", false
}

func selectQwen35Model(models []ollamaModelInfo) (string, bool) {
	for _, candidate := range []func(ollamaModelInfo) bool{
		func(model ollamaModelInfo) bool { return appModelIsQwen35(model) && appModelCanThink(model) },
		appModelIsQwen35,
	} {
		for _, model := range models {
			if candidate(model) {
				return appModelName(model), true
			}
		}
	}
	return "", false
}

func isQwen35Selector(value string) bool {
	normalized := normalizeModelName(value)
	return normalized == "" || normalized == "qwen35"
}

func appModelName(model ollamaModelInfo) string {
	if strings.TrimSpace(model.Model) != "" {
		return strings.TrimSpace(model.Model)
	}
	return strings.TrimSpace(model.Name)
}

func appModelIsQwen35(model ollamaModelInfo) bool {
	name := normalizeModelName(model.Name + " " + model.Model)
	return strings.Contains(name, "qwen35")
}

func normalizeModelName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(".", "", "-", "", "_", "", ":", "", "/", "", " ", "")
	return replacer.Replace(value)
}

func appModelCanThink(model ollamaModelInfo) bool {
	for _, capability := range model.Capabilities {
		if strings.EqualFold(capability, "thinking") {
			return true
		}
	}
	return false
}

func ollamaReachable(ctx context.Context, cfg config.Config) bool {
	if !cfg.LLM.Enabled {
		return true
	}
	_, err := ollamaModelList(ctx, cfg)
	return err == nil
}

func ollamaModels(ctx context.Context, cfg config.Config) (map[string]bool, error) {
	modelList, err := ollamaModelList(ctx, cfg)
	if err != nil {
		return nil, err
	}
	models := map[string]bool{}
	for _, model := range modelList {
		if model.Name != "" {
			models[model.Name] = true
		}
		if model.Model != "" {
			models[model.Model] = true
		}
	}
	return models, nil
}

func ollamaModelList(ctx context.Context, cfg config.Config) ([]ollamaModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.LLM.Endpoint, "/")+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	return tags.Models, nil
}
