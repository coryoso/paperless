// Package fm invokes Apple's Foundation Models CLI without a shell or server.
package fm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Available checks model readiness as well as the presence of the executable.
func Available(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := run(ctx, "", "available", "--model", "system")
	if err != nil {
		return fmt.Errorf("Apple Foundation Models unavailable; check `fm available --model system`: %w", err)
	}
	// Require explicit readiness; a successful exit alone is insufficient.
	if strings.TrimSpace(out) != "System model available" {
		return fmt.Errorf("Apple Foundation Models unavailable: %s", strings.TrimSpace(out))
	}
	return nil
}

func CountTokens(ctx context.Context, instructions, prompt string) (int, error) {
	out, err := run(ctx, prompt, "count-tokens", "--quiet", "--instructions", instructions)
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || count < 0 {
		return 0, fmt.Errorf("fm returned an invalid token count")
	}
	return count, nil
}

func Respond(ctx context.Context, instructions, prompt string, schema []byte) (string, error) {
	file, err := os.CreateTemp("", "paperless-fm-schema-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(schema); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return run(ctx, prompt, "respond", "--model", "system", "--no-stream", "--greedy", "--guardrails", "permissive-content-transformations", "--instructions", instructions, "--schema", file.Name())
}

func run(ctx context.Context, input string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "fm", args...)
	// Keep document contents out of process arguments, and never invoke a shell.
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("fm %s: %w", args[0], ctx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
		return "", fmt.Errorf("fm %s: %w: %s", args[0], err, detail)
	}
	return strings.TrimSpace(stdout.String()), nil
}
