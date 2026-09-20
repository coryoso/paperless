package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Embeddings         Embeddings         `toml:"embeddings"`
	Paths              Paths              `toml:"paths"`
	Service            Service            `toml:"service"`
	OCR                OCR                `toml:"ocr"`
	LLM                LLM                `toml:"llm"`
	Bonsai             Bonsai             `toml:"bonsai"`
	Setup              Setup              `toml:"setup"`
	Policy             Policy             `toml:"policy"`
	SenderFolders      map[string]string  `toml:"sender_folders"`
	RecipientProfiles  []RecipientProfile `toml:"recipient_profiles"`
	RecipientAddresses []string           `toml:"recipient_addresses"`
}

type RecipientProfile struct {
	ID           int64    `json:"id" toml:"-"`
	Name         string   `json:"name" toml:"name"`
	Scope        string   `json:"scope" toml:"scope"`
	Aliases      []string `json:"aliases" toml:"aliases"`
	Addresses    []string `json:"addresses" toml:"addresses"`
	FolderPrefix string   `json:"folder_prefix" toml:"folder_prefix"`
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
	Workers           int      `toml:"workers"`
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

const DefaultBonsaiEndpoint = "http://127.0.0.1:8080"
const DefaultBonsaiModel = "Bonsai-8B"

// Bonsai keeps its server settings separate so selecting it preserves Ollama's
// endpoint and model. Shared inference limits remain in LLM.
type Bonsai struct {
	Endpoint string `toml:"endpoint"`
	Model    string `toml:"model"`
}

// Embeddings are independent of the document classification provider.
type Embeddings struct {
	Enabled        bool   `toml:"enabled" json:"enabled"`
	Endpoint       string `toml:"endpoint" json:"endpoint"`
	Model          string `toml:"model" json:"model"`
	TimeoutSeconds int    `toml:"timeout_seconds" json:"-"`
}

func (e Embeddings) Validate() error {
	u, err := url.Parse(e.Endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("embeddings.endpoint must be a local HTTP server URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("embeddings.endpoint must use localhost or a loopback IP address")
	}
	if strings.TrimSpace(e.Model) == "" || len(e.Model) > 200 || strings.ContainsAny(e.Model, " \t\r\n") {
		return fmt.Errorf("embeddings.model must be a model name")
	}
	if e.TimeoutSeconds < 1 || e.TimeoutSeconds > 600 {
		return fmt.Errorf("embeddings.timeout_seconds must be between 1 and 600")
	}
	return nil
}

type Setup struct {
	Step string `toml:"step"`
}

// Older configurations with a documents folder are already set up. New
// installations enter the guide without forcing existing users through it.
func (c Config) SetupStep() string {
	if strings.TrimSpace(c.Paths.ArchiveRoot) == "" {
		return "documents"
	}
	if c.Setup.Step == "" {
		return "complete"
	}
	return c.Setup.Step
}

func (c Config) NeedsSetup() bool { return c.SetupStep() != "complete" }

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
			Inbox:      filepath.Join(base, "inbox"),
			Raw:        filepath.Join(base, "raw"),
			Processing: filepath.Join(base, "processing"),
			Archive:    filepath.Join(base, "archive"),
			Review:     filepath.Join(base, "review"),
			Rejected:   filepath.Join(base, "rejected"),
			Duplicates: filepath.Join(base, "duplicates"),
			Logs:       filepath.Join(base, "logs"),
			StateDir:   filepath.Join(homeDir(), "Library", "Application Support", "Paperless"),
			// The document root is intentionally unset until the user chooses it.
			// Cloud-provider folders are valid once they are mounted locally.
			ArchiveRoot: "",
		},
		Service: Service{
			Host:                 "127.0.0.1",
			Port:                 8844,
			PollSeconds:          5,
			FileStabilitySeconds: 10,
		},
		OCR: OCR{
			Workers:           2,
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
		Embeddings: Embeddings{Endpoint: "http://127.0.0.1:11434", Model: "embeddinggemma", TimeoutSeconds: 120},
		Bonsai:     Bonsai{Endpoint: DefaultBonsaiEndpoint, Model: DefaultBonsaiModel},
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
	if c.Service.Port < 1 || c.Service.Port > 65535 {
		return Config{}, fmt.Errorf("service port must be between 1 and 65535")
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
	c.LLM.Provider = strings.ToLower(strings.TrimSpace(c.LLM.Provider))
	if c.LLM.Provider == "" {
		c.LLM.Provider = "ollama"
	}
	if c.LLM.Provider != "ollama" && c.LLM.Provider != "fm" && c.LLM.Provider != "bonsai" {
		return Config{}, fmt.Errorf("llm.provider must be ollama, fm, or bonsai")
	}
	if c.LLM.Provider == "fm" {
		// fm exposes Apple's on-device model as system. Ignore any Ollama tag
		// left in an existing configuration when only the provider is changed.
		c.LLM.Model = "system"
	} else if c.LLM.Model == "" || c.LLM.Model == "system" {
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
	if c.Bonsai.Endpoint == "" {
		c.Bonsai.Endpoint = DefaultBonsaiEndpoint
	}
	if c.Bonsai.Model == "" {
		c.Bonsai.Model = DefaultBonsaiModel
	}
	if c.Embeddings.Endpoint == "" {
		c.Embeddings.Endpoint = "http://127.0.0.1:11434"
	}
	if c.Embeddings.Model == "" {
		c.Embeddings.Model = "embeddinggemma"
	}
	if c.Embeddings.TimeoutSeconds == 0 {
		c.Embeddings.TimeoutSeconds = 120
	}
	if err := c.Embeddings.Validate(); err != nil {
		return Config{}, err
	}
	switch c.Setup.Step {
	case "", "documents", "scanner", "model", "ready", "complete":
	default:
		return Config{}, fmt.Errorf("invalid setup.step %q", c.Setup.Step)
	}
	return c, nil
}

func (c Config) ModelName() string {
	if c.LLM.Provider == "bonsai" {
		return c.Bonsai.Model
	}
	return c.LLM.Model
}

// DashboardURL returns the browser-facing URL for the configured service.
// Wildcard bind addresses are replaced with loopback because they are not
// useful destinations for a local browser.
func (c Config) DashboardURL() string {
	host := strings.TrimSpace(c.Service.Host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(c.Service.Port))
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
	return Write(path, cfg)
}

// Write atomically replaces a Paperless configuration file. It is used by the
// guided local setup as well as the command-line configure flow.
func Write(path string, cfg Config) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	path = expand(path)
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".paperless-config-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	return path, nil
}

func expand(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
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
