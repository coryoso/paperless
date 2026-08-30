package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"howett.net/plist"

	"paperless/internal/config"
)

const launchAgentLabel = "com.paperless.scanner"

func ServiceCommand(cfg config.Config, configPath string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: paperless service install|uninstall|start|stop|status")
	}
	if runtime.GOOS != "darwin" {
		return errors.New("service commands are macOS-only")
	}
	switch args[0] {
	case "install":
		path, err := writeLaunchAgent(cfg, configPath)
		if err != nil {
			return err
		}
		fmt.Printf("Wrote LaunchAgent: %s\n", path)
		return nil
	case "start":
		return launchctl("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), launchAgentPath())
	case "stop":
		return launchctl("bootout", fmt.Sprintf("gui/%d", os.Getuid()), launchAgentPath())
	case "uninstall":
		_ = launchctl("bootout", fmt.Sprintf("gui/%d", os.Getuid()), launchAgentPath())
		return os.Remove(launchAgentPath())
	case "status":
		return launchctl("print", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel))
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}

func writeLaunchAgent(cfg config.Config, configPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(launchAgentPath()), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cfg.Paths.Logs, 0o755); err != nil {
		return "", err
	}
	payload := map[string]any{
		"Label": launchAgentLabel,
		"ProgramArguments": []string{
			exe,
			"--config",
			configPath,
			"run",
		},
		"RunAtLoad":         true,
		"KeepAlive":         true,
		"StandardOutPath":   filepath.Join(cfg.Paths.Logs, "launchd.out.log"),
		"StandardErrorPath": filepath.Join(cfg.Paths.Logs, "launchd.err.log"),
	}
	data, err := plist.MarshalIndent(payload, plist.XMLFormat, "\t")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(launchAgentPath(), data, 0o644); err != nil {
		return "", err
	}
	return launchAgentPath(), nil
}

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func launchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
