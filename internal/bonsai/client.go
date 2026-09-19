// Package bonsai integrates the OpenAI-compatible server from PrismML's demo.
package bonsai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"paperless/internal/config"
)

func apiURL(endpoint, path string) string {
	return strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/v1") + "/v1/" + path
}

func Available(ctx context.Context, cfg config.Bonsai) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL(cfg.Endpoint, "models"), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Bonsai is unavailable at %s; install it in Setup or start your Bonsai server: %w", cfg.Endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Bonsai models endpoint returned HTTP %d", resp.StatusCode)
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&models); err != nil {
		return fmt.Errorf("invalid Bonsai model list: %w", err)
	}
	for _, model := range models.Data {
		if model.ID == cfg.Model {
			return healthy(ctx, cfg.Endpoint)
		}
	}
	return fmt.Errorf("Bonsai model %q is not served at %s; check bonsai.model or install Bonsai in Setup", cfg.Model, cfg.Endpoint)
}

func healthy(ctx context.Context, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL(endpoint, "health"), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var health struct {
		Status string `json:"status"`
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Bonsai is not ready (HTTP %d); the model may still be loading", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return fmt.Errorf("Bonsai is not ready: %s", health.Status)
	}
	return nil
}

// Complete requests constrained JSON. Reasoning is disabled so the token budget
// is available for the classification, including on Bonsai reasoning models.
func Complete(ctx context.Context, cfg config.Config, instructions, prompt string, schema map[string]any) (string, error) {
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	// The native template constrains tokens without exposing the schema's field
	// descriptions to the model. Include those instructions in its context too.
	instructions += "\nReturn only a JSON object matching this schema:\n" + string(schemaJSON)
	body, err := json.Marshal(map[string]any{
		"model":                cfg.Bonsai.Model,
		"messages":             []map[string]string{{"role": "system", "content": instructions}, {"role": "user", "content": prompt}},
		"stream":               false,
		"reasoning_effort":     "none",
		"temperature":          0,
		"max_tokens":           cfg.LLM.MaxOutputTokens,
		"chat_template_kwargs": map[string]bool{"enable_thinking": false},
		"response_format":      map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "document_classification", "strict": true, "schema": schema}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL(cfg.Bonsai.Endpoint, "chat/completions"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Bonsai completion returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid Bonsai response: %w", err)
	}
	if len(result.Choices) != 1 || result.Choices[0].FinishReason != "stop" || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("Bonsai returned an empty or incomplete classification")
	}
	return result.Choices[0].Message.Content, nil
}
