package app

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"paperless/internal/config"
)

const sampleSharingList = `
List of Share Points
name: Shared
path: /Users/example/Shared
	smb: {
		name: Shared
		shared: 1
		guest access: 1
	}
name: Paperless-Inbox
path: /Users/example/Paperless-Inbox
	smb: {
		name: Paperless-Inbox
		shared: 1
		guest access: 0
	}
`

func TestSharingListIncludesExactSMBPath(t *testing.T) {
	if !sharingListIncludesSMB(sampleSharingList, "/Users/example/Paperless-Inbox") {
		t.Fatal("expected inbox share")
	}
	if sharingListIncludesSMB(sampleSharingList, "/Users/example/Paperless") {
		t.Fatal("parent path must not match")
	}
}

func TestSharingListRequiresEnabledSMB(t *testing.T) {
	output := strings.Replace(sampleSharingList, "shared: 1", "shared: 0", -1)
	if sharingListIncludesSMB(output, "/Users/example/Paperless-Inbox") {
		t.Fatal("disabled share must not match")
	}
}

func TestPrintInboxSetupIncludesConfiguredPath(t *testing.T) {
	cfg := config.Default()
	cfg.Paths.Inbox = filepath.Join(t.TempDir(), "Scanner Inbox")
	var output bytes.Buffer
	PrintInboxSetup(&output, cfg)
	if !strings.Contains(output.String(), cfg.Paths.Inbox) {
		t.Fatalf("setup output missing inbox path: %s", output.String())
	}
}
