package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomebrewUpgradeRestart(t *testing.T) {
	formula, err := os.ReadFile("../../.github/homebrew/paperless.rb.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	_, script, ok := strings.Cut(string(formula), "<<~SH]\n")
	if !ok {
		t.Fatal("missing Homebrew restart hook")
	}
	script, _, ok = strings.Cut(script, "\n    SH")
	if !ok {
		t.Fatal("unterminated Homebrew restart hook")
	}

	for _, tc := range []struct {
		name    string
		domain  string
		label   string
		running bool
		fail    bool
	}{
		{name: "first install or stopped service"},
		{name: "current Homebrew", domain: "gui", label: "sh.brew.paperless", running: true},
		{name: "legacy Homebrew", domain: "gui", label: "homebrew.mxcl.paperless", running: true},
		{name: "headless service", domain: "user", label: "sh.brew.paperless", running: true},
		{name: "registered but not running", domain: "gui", label: "sh.brew.paperless"},
		{name: "unrelated service", domain: "gui", label: "com.paperless.bonsai", running: true},
		{name: "signal failure", domain: "gui", label: "sh.brew.paperless", running: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			launchctl := filepath.Join(dir, "launchctl")
			log := filepath.Join(dir, "signals")
			fake := `#!/bin/sh
case "$1" in
  print)
    [ "$2" = "$TEST_SERVICE" ] || exit 113
    if [ "$TEST_RUNNING" = true ]; then
      echo '    pid = 1234'
    fi
    ;;
  kill)
    [ "$2" = SIGTERM ] && [ "$3" = "$TEST_SERVICE" ] || exit 2
    [ "$TEST_FAIL" = false ] || exit 1
    echo "$3" >> "$TEST_SIGNALS"
    ;;
  *) exit 2 ;;
esac
`
			if err := os.WriteFile(launchctl, []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			service := fmt.Sprintf("%s/%d/%s", tc.domain, os.Getuid(), tc.label)
			cmd := exec.Command("/bin/sh", "-eu", "-c", strings.ReplaceAll(script, "/bin/launchctl", `"`+launchctl+`"`))
			cmd.Env = append(os.Environ(),
				"TEST_SERVICE="+service,
				fmt.Sprintf("TEST_RUNNING=%t", tc.running),
				fmt.Sprintf("TEST_FAIL=%t", tc.fail),
				"TEST_SIGNALS="+log,
			)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail {
				t.Fatalf("hook error = %v, want failure %t; output: %s", err, tc.fail, output)
			}
			signals, err := os.ReadFile(log)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			want := ""
			if tc.running && !tc.fail && strings.HasSuffix(tc.label, ".paperless") {
				want = service + "\n"
			}
			if string(signals) != want {
				t.Fatalf("signalled services = %q, want %q", signals, want)
			}
		})
	}
}
