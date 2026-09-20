package bonsai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"howett.net/plist"
	"paperless/internal/config"
)

const (
	demoRepository = "https://github.com/PrismML-Eng/Bonsai-demo.git"
	demoRevision   = "17b143e889a45c090816b520e79a50c996164e1f"
	modelRevision  = "48516770dd04643643e9f9019a2a349cf26c5dbd"
	modelFilename  = "Bonsai-8B-Q1_0.gguf"
	modelSHA256    = "284a335aa3fb2ced3b1b01fcb40b08aa783e3b70832767f0dd2e3fdfa134bd54"
	serviceLabel   = "com.paperless.bonsai"
)

// Install downloads the text-only runtime and weights, then starts a per-user
// macOS service. It never runs the demo's interactive, full Python/UI setup.
// On success it returns the ready server's endpoint for the caller to persist.
func Install(ctx context.Context, cfg config.Config, stdout, stderr io.Writer, report func(string)) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("automatic Bonsai installation is available on macOS; on other platforms run Bonsai-demo setup and configure bonsai.endpoint and bonsai.model")
	}
	if _, err := managedPort(cfg.Bonsai); err != nil {
		return "", err
	}
	if cfg.LLM.ContextTokens < 512 || cfg.LLM.MaxOutputTokens < 1 || cfg.LLM.MaxOutputTokens >= cfg.LLM.ContextTokens {
		return "", errors.New("Bonsai needs a context of at least 512 tokens and max_output_tokens smaller than context_tokens")
	}
	for _, command := range []string{"git", "curl"} {
		if _, err := exec.LookPath(command); err != nil {
			return "", fmt.Errorf("Bonsai installation needs %s; install the macOS Command Line Tools and retry", command)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	dir := filepath.Join(cfg.Paths.StateDir, "bonsai")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Serialize CLI and browser installers across processes. flock releases even
	// if a download is interrupted or Paperless exits.
	unlock, err := lockInstall(dir)
	if err != nil {
		return "", err
	}
	defer unlock()
	step := func(message string) {
		fmt.Fprintln(stdout, message)
		if report != nil {
			report(message)
		}
	}
	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		cmd.Stdout, cmd.Stderr = stdout, stderr
		configureInstallCommand(cmd)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Bonsai %s failed: %w", name, err)
		}
		return nil
	}
	step("Preparing the pinned Bonsai runtime…")
	stamp := filepath.Join(dir, ".paperless-revision")
	if revision, _ := os.ReadFile(stamp); strings.TrimSpace(string(revision)) != demoRevision {
		if err := run("git", "init", "."); err != nil {
			return "", err
		}
		if err := run("git", "fetch", "--depth", "1", demoRepository, demoRevision); err != nil {
			return "", err
		}
		if err := run("git", "checkout", "--detach", demoRevision); err != nil {
			return "", err
		}
		if err := os.WriteFile(stamp, []byte(demoRevision), 0o600); err != nil {
			return "", err
		}
	}
	step("Downloading PrismML's llama.cpp binaries…")
	if err := run("/bin/sh", filepath.Join(dir, "scripts", "download_binaries.sh")); err != nil {
		return "", err
	}
	step("Downloading Bonsai 8B weights (1.16 GB); interrupted downloads can be resumed…")
	model := filepath.Join(dir, "models", "gguf", "8B", modelFilename)
	if err := os.MkdirAll(filepath.Dir(model), 0o700); err != nil {
		return "", err
	}
	if err := verifySHA256(model, modelSHA256); err != nil {
		url := "https://huggingface.co/prism-ml/Bonsai-8B-gguf/resolve/" + modelRevision + "/" + modelFilename
		partial := model + ".partial"
		if err := run("curl", "--fail", "--location", "--retry", "3", "--connect-timeout", "30", "--continue-at", "-", "--output", partial, url); err != nil {
			return "", err
		}
		step("Verifying the downloaded model…")
		if err := verifySHA256(partial, modelSHA256); err != nil {
			_ = os.Remove(partial)
			return "", err
		}
		if err := os.Rename(partial, model); err != nil {
			return "", err
		}
	}
	// An existing compatible server can be reused without replacing its service.
	if Available(ctx, cfg.Bonsai) == nil {
		step("Bonsai is ready.")
		return cfg.Bonsai.Endpoint, nil
	}
	listener, err := reservePort(cfg.Bonsai)
	if err != nil {
		return "", err
	}
	defer listener.Close()
	cfg.Bonsai.Endpoint = "http://" + listener.Addr().String()
	step("Starting Bonsai at " + cfg.Bonsai.Endpoint + "…")
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	servicePath := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		return "", err
	}
	data, err := servicePlist(cfg, dir, home)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(servicePath, data, 0o600); err != nil {
		return "", err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	// This label belongs only to Paperless's Bonsai service. A previous failed
	// startup can be retried after installation has repaired its files.
	_ = run("launchctl", "bootout", domain+"/"+serviceLabel)
	// Hold the chosen port until launchd is ready to start the server.
	if err := listener.Close(); err != nil {
		return "", err
	}
	if err := run("launchctl", "bootstrap", domain, servicePath); err != nil {
		return "", err
	}
	step("Loading the model; this can take a few minutes…")
	readyCtx, readyCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer readyCancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if Available(readyCtx, cfg.Bonsai) == nil {
			step("Bonsai is ready.")
			return cfg.Bonsai.Endpoint, nil
		}
		select {
		case <-readyCtx.Done():
			return "", fmt.Errorf("Bonsai did not become ready: %w; see %s", readyCtx.Err(), filepath.Join(dir, "server.log"))
		case <-ticker.C:
		}
	}
}

func verifySHA256(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return fmt.Errorf("Bonsai model checksum mismatch: %s; retry installation", path)
	}
	return nil
}

func servicePlist(cfg config.Config, dir, home string) ([]byte, error) {
	port, err := managedPort(cfg.Bonsai)
	if err != nil {
		return nil, err
	}
	// The pinned runtime's Jinja parser rejects constrained JSON with the 8B
	// template's empty thinking prefix. Its native template path preserves the
	// same non-thinking model while applying the JSON grammar to content only.
	return plist.MarshalIndent(map[string]any{
		"Label":            serviceLabel,
		"ProgramArguments": []string{"/bin/sh", filepath.Join(dir, "scripts", "start_llama_server.sh"), "--alias", config.DefaultBonsaiModel, "--parallel", "1", "--no-jinja"},
		"WorkingDirectory": dir,
		"EnvironmentVariables": map[string]string{
			"HOME":          home,
			"PATH":          "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
			"BONSAI_FAMILY": "bonsai", "BONSAI_MODEL": "8B",
			"BONSAI_HOST": "127.0.0.1", "PORT": port,
			"BONSAI_CTX": strconv.Itoa(cfg.LLM.ContextTokens),
		},
		"RunAtLoad": true, "KeepAlive": true, "ThrottleInterval": 30,
		"StandardOutPath":   filepath.Join(dir, "server.log"),
		"StandardErrorPath": filepath.Join(dir, "server.log"),
	}, plist.XMLFormat, "\t")
}

// Managed servers always stay on IPv4 loopback. Permit previously selected
// ports on reinstall, while leaving remote/custom servers to Save model.
func managedPort(cfg config.Bonsai) (string, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/" || u.Path == "/v1") && cfg.Model == config.DefaultBonsaiModel {
		port, err := strconv.Atoi(u.Port())
		if err == nil && port > 0 && port <= 65535 {
			return strconv.Itoa(port), nil
		}
	}
	return "", errors.New("managed installation requires an http://127.0.0.1:<port> endpoint and bonsai.model = Bonsai-8B; use Save model for an existing custom server")
}

// Try the configured port first, then let the OS reserve a free local port.
// The caller releases the reservation immediately before starting the service.
func reservePort(cfg config.Bonsai) (net.Listener, error) {
	port, err := managedPort(cfg)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:"+port)
	if err == nil {
		return listener, nil
	}
	listener, err = net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("reserve a local Bonsai port: %w", err)
	}
	return listener, nil
}
