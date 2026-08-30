package app

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"paperless/internal/config"
)

type ScannerShareStatus struct {
	Checked bool
	Shared  bool
}

func InboxShareStatus(inbox string) ScannerShareStatus {
	if runtime.GOOS != "darwin" {
		return ScannerShareStatus{}
	}
	output, err := exec.Command("/usr/sbin/sharing", "-l").Output()
	if err != nil {
		return ScannerShareStatus{}
	}
	return ScannerShareStatus{Checked: true, Shared: sharingListIncludesSMB(string(output), inbox)}
}

func PrintInboxSetup(w io.Writer, cfg config.Config) {
	status := InboxShareStatus(cfg.Paths.Inbox)
	fmt.Fprintf(w, "Scanner inbox: %s\n", cfg.Paths.Inbox)
	if status.Checked && status.Shared {
		fmt.Fprintln(w, "SMB sharing: ready")
		return
	}
	fmt.Fprintln(w, "SMB sharing: setup needed for scanner access")
	fmt.Fprintln(w, "  1. Open System Settings > General > Sharing > File Sharing.")
	fmt.Fprintf(w, "  2. Add %s as a shared folder.\n", cfg.Paths.Inbox)
	fmt.Fprintln(w, "  3. Open Options and enable Share files and folders using SMB.")
	fmt.Fprintf(w, "  4. Configure the scanner to use the %q share on this Mac.\n", filepath.Base(cfg.Paths.Inbox))
}

func sharingListIncludesSMB(output, target string) bool {
	target = cleanSharePath(target)
	currentPath := ""
	inSMB := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case !inSMB && strings.HasPrefix(line, "name:"):
			currentPath = ""
			inSMB = false
		case strings.HasPrefix(line, "path:"):
			currentPath = cleanSharePath(fieldValue(line))
		case strings.HasPrefix(line, "smb:"):
			inSMB = true
		case line == "}":
			inSMB = false
		case inSMB && strings.HasPrefix(line, "shared:"):
			if currentPath == target && fieldValue(line) == "1" {
				return true
			}
		}
	}
	return false
}

func fieldValue(line string) string {
	_, value, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func cleanSharePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}
