package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Paths         Paths             `toml:"paths"`
	Service       Service           `toml:"service"`
	OCR           OCR               `toml:"ocr"`
	LLM           LLM               `toml:"llm"`
	Policy        Policy            `toml:"policy"`
	SenderFolders map[string]string `toml:"sender_folders"`
}

type Paths struct {
	Inbox       string `toml:"inbox"`
	Raw         string `toml:"raw"`
	Processing  string `toml:"processing"`
	Archive     string `toml:"archive"`
	Review      string `toml:"review"`
	Rejected    string `toml:"rejected"`
	Duplicates  string `toml:"duplicates"`
	Logs        string `toml:"logs"`
	StateDir    string `toml:"state_dir"`
	ArchiveRoot string `toml:"archive_root"`
}

type Service struct {
	Host                 string `toml:"host"`
	Port                 int    `toml:"port"`
	PollSeconds          int    `toml:"poll_seconds"`
	FileStabilitySeconds int    `toml:"file_stability_seconds"`
}

type OCR struct {
	Languages         []string `toml:"languages"`
	RenderDPI         int      `toml:"render_dpi"`
	CropContent       bool     `toml:"crop_content"`
	CropPaddingPixels int      `toml:"crop_padding_pixels"`
	MinCropConfidence float64  `toml:"min_crop_confidence"`
}

type LLM struct {
	Provider        string `toml:"provider"`
	Endpoint        string `toml:"endpoint"`
	Model           string `toml:"model"`
	Enabled         bool   `toml:"enabled"`
	TimeoutSeconds  int    `toml:"timeout_seconds"`
	ContextTokens   int    `toml:"context_tokens"`
	ReasoningTokens int    `toml:"reasoning_tokens"`
	MaxOutputTokens int    `toml:"max_output_tokens"`
	KeepAlive       string `toml:"keep_alive"`
}

type Policy struct {
	AutoFileMinConfidence   int      `toml:"auto_file_min_confidence"`
	MinApprovedExamples     int      `toml:"min_approved_examples"`
	AllowNewTopLevelFolders bool     `toml:"allow_new_top_level_folders"`
	KnownFolders            []string `toml:"known_folders"`
	DiscardDocumentTypes    []string `toml:"discard_document_types"`
	KeepDocumentTypes       []string `toml:"keep_document_types"`
}

func DefaultPath() string {
	return filepath.Join(homeDir(), ".paperless", "config.toml")
}

func Default() Config {
	base := filepath.Join(homeDir(), "Paperless")
	return Config{
		Paths: Paths{
			Inbox:       filepath.Join(base, "inbox"),
			Raw:         filepath.Join(base, "raw"),
			Processing:  filepath.Join(base, "processing"),
			Archive:     filepath.Join(base, "archive"),
			Review:      filepath.Join(base, "review"),
			Rejected:    filepath.Join(base, "rejected"),
			Duplicates:  filepath.Join(base, "duplicates"),
			Logs:        filepath.Join(base, "logs"),
			StateDir:    filepath.Join(homeDir(), "Library", "Application Support", "Paperless"),
			ArchiveRoot: defaultArchiveRoot(),
		},
		Service: Service{
			Host:                 "127.0.0.1",
			Port:                 8844,
			PollSeconds:          5,
			FileStabilitySeconds: 10,
		},
		OCR: OCR{
			Languages:         []string{"deu", "eng"},
			RenderDPI:         300,
			CropContent:       true,
			CropPaddingPixels: 30,
			MinCropConfidence: 0.75,
		},
		LLM: LLM{
			Provider:        "ollama",
			Endpoint:        "http://localhost:11434",
			Model:           "qwen3.5:9b-q4_K_M",
			Enabled:         true,
			TimeoutSeconds:  360,
			ContextTokens:   16_384,
			ReasoningTokens: 2_048,
			MaxOutputTokens: 2_048,
			KeepAlive:       "0",
		},
		Policy: Policy{
			AutoFileMinConfidence:   92,
			MinApprovedExamples:     2,
			AllowNewTopLevelFolders: false,
			KnownFolders:            []string{},
			DiscardDocumentTypes: []string{
				"receipt",
				"delivery-receipt",
				"utility-information",
				"routine-invoice",
				"marketing",
			},
			KeepDocumentTypes: []string{
				"contract",
				"insurance-policy",
				"tax-letter",
				"government-letter",
				"legal-letter",
				"medical-document",
				"bank-document",
				"identity-document",
			},
		},
		SenderFolders: map[string]string{},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	path = expand(path)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}
	return cfg.Resolve()
}

func (c Config) Resolve() (Config, error) {
	paths := []*string{
		&c.Paths.Inbox,
		&c.Paths.Raw,
		&c.Paths.Processing,
		&c.Paths.Archive,
		&c.Paths.Review,
		&c.Paths.Rejected,
		&c.Paths.Duplicates,
		&c.Paths.Logs,
		&c.Paths.StateDir,
		&c.Paths.ArchiveRoot,
	}
	for _, value := range paths {
		*value = expand(*value)
	}
	if c.Service.Host == "" {
		c.Service.Host = "127.0.0.1"
	}
	if c.Service.Port == 0 {
		c.Service.Port = 8844
	}
	if c.Service.PollSeconds == 0 {
		c.Service.PollSeconds = 5
	}
	if c.Service.FileStabilitySeconds == 0 {
		c.Service.FileStabilitySeconds = 10
	}
	if c.OCR.RenderDPI == 0 {
		c.OCR.RenderDPI = 300
	}
	if c.OCR.CropPaddingPixels == 0 {
		c.OCR.CropPaddingPixels = 30
	}
	if c.OCR.MinCropConfidence == 0 {
		c.OCR.MinCropConfidence = 0.75
	}
	if c.LLM.Endpoint == "" {
		c.LLM.Endpoint = "http://localhost:11434"
	}
	if c.LLM.Model == "" {
		c.LLM.Model = "qwen3.5:9b-q4_K_M"
	}
	if c.LLM.TimeoutSeconds == 0 {
		c.LLM.TimeoutSeconds = 360
	}
	if c.LLM.ContextTokens == 0 {
		c.LLM.ContextTokens = 16_384
	}
	if c.LLM.ReasoningTokens == 0 {
		c.LLM.ReasoningTokens = 2_048
	}
	if c.LLM.MaxOutputTokens == 0 {
		c.LLM.MaxOutputTokens = 2_048
	}
	if c.LLM.KeepAlive == "" {
		c.LLM.KeepAlive = "0"
	}
	return c, nil
}

func (c Config) DBPath() string {
	return filepath.Join(c.Paths.StateDir, "paperless.sqlite")
}

func (c Config) RuntimeDirs() []string {
	return []string{
		c.Paths.Inbox,
		c.Paths.Raw,
		c.Paths.Processing,
		c.Paths.Archive,
		c.Paths.Review,
		c.Paths.Rejected,
		c.Paths.Duplicates,
		c.Paths.Logs,
		c.Paths.StateDir,
	}
}

func (c Config) EnsureDirs() error {
	for _, dir := range c.RuntimeDirs() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func WriteDefault(path string, cfg Config, force bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	path = expand(path)
	if _, err := os.Stat(path); err == nil && !force {
		return "", fmt.Errorf("config already exists: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	cfg, err := cfg.Resolve()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func expand(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(homeDir(), path[2:])
	}
	path = os.ExpandEnv(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	return "."
}

func defaultArchiveRoot() string {
	home := homeDir()
	candidates := []string{
		filepath.Join(home, "Library", "CloudStorage", "Dropbox", "Dokumente"),
		filepath.Join(home, "Library", "CloudStorage", "Dropbox", "Documents"),
		filepath.Join(home, "Dropbox", "Dokumente"),
		filepath.Join(home, "Dropbox", "Documents"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}
