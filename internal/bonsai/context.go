package bonsai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"paperless/internal/config"
)

// CheckContext uses the running runtime's tokenizer and actual per-slot context.
// Reserve output tokens plus 512 tokens for chat-template framing. No input text is cut.
func CheckContext(ctx context.Context, cfg config.Config, instructions, prompt string, schema map[string]any) error {
	root := strings.TrimSuffix(strings.TrimRight(cfg.Bonsai.Endpoint, "/"), "/v1")
	call := func(method, path string, body []byte, out any) error {
		req, err := http.NewRequestWithContext(ctx, method, root+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("Bonsai context check %s returned HTTP %d", path, response.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(out)
	}
	var props struct {
		Defaults struct {
			Context int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := call(http.MethodGet, "/props", nil, &props); err != nil {
		return fmt.Errorf("cannot check Bonsai context window: %w", err)
	}
	if props.Defaults.Context < 1 {
		return fmt.Errorf("Bonsai did not report its context window")
	}
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"content": instructions + "\nReturn only a JSON object matching this schema:\n" + string(schemaBytes) + "\n" + prompt, "add_special": true})
	if err != nil {
		return err
	}
	var tokenized struct {
		Tokens []int `json:"tokens"`
	}
	if err := call(http.MethodPost, "/tokenize", body, &tokenized); err != nil {
		return fmt.Errorf("cannot count Bonsai context tokens: %w", err)
	}
	if len(tokenized.Tokens) == 0 {
		return fmt.Errorf("Bonsai tokenizer returned no tokens")
	}
	limit := min(props.Defaults.Context, cfg.LLM.ContextTokens)
	needed := len(tokenized.Tokens) + cfg.LLM.MaxOutputTokens + 512
	if needed > limit {
		return fmt.Errorf("classification input needs approximately %d tokens including output space, but the context window is %d; increase llm.context_tokens and the running model window, or use smaller blocks", needed, limit)
	}
	return nil
}
