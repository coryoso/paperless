// Package embeddings provides local semantic vectors independently of classification.
package embeddings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"paperless/internal/config"
)

type Client struct{ Config config.Embeddings }

func (c Client) request(ctx context.Context, method, path string, input, output any) error {
	if err := c.Config.Validate(); err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Config.Endpoint, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: time.Duration(c.Config.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("embedding endpoint redirects are not supported")
	}}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("local embedding service unavailable; start Ollama: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama embeddings returned HTTP %d; ensure model %q is installed", res.StatusCode, c.Config.Model)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(output); err != nil {
		return fmt.Errorf("invalid embedding response: %w", err)
	}
	return nil
}

// Identity includes the installed weights, not just a mutable model tag. Model
// replacements and preprocessing changes must never mix incompatible vectors.
func (c Client) Identity(ctx context.Context) (string, error) {
	var response struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := c.request(ctx, "GET", "/api/tags", nil, &response); err != nil {
		return "", err
	}
	name := c.Config.Model
	if !strings.Contains(name, ":") {
		name += ":latest"
	}
	for _, model := range response.Models {
		if (model.Name == name || model.Name == c.Config.Model) && model.Digest != "" {
			return Hash("markdown-v1\n" + c.Config.Endpoint + "\n" + name + "\n" + model.Digest), nil
		}
	}
	return "", fmt.Errorf("embedding model %q is missing; run ollama pull %s", c.Config.Model, c.Config.Model)
}

func (c Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	var response struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	input := map[string]any{"model": c.Config.Model, "input": texts, "truncate": false, "keep_alive": "5m"}
	if err := c.request(ctx, "POST", "/api/embed", input, &response); err != nil {
		return nil, err
	}
	if len(response.Embeddings) != len(texts) {
		return nil, errors.New("embedding response has the wrong number of vectors")
	}
	dimension := 0
	for _, vector := range response.Embeddings {
		if len(vector) == 0 || len(vector) > 4096 {
			return nil, errors.New("invalid embedding dimensions")
		}
		if dimension != 0 && len(vector) != dimension {
			return nil, errors.New("inconsistent embedding dimensions")
		}
		dimension = len(vector)
		norm := float64(0)
		for _, v := range vector {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil, errors.New("non-finite embedding")
			}
			norm += float64(v) * float64(v)
		}
		if norm == 0 {
			return nil, errors.New("empty embedding vector")
		}
		for i := range vector {
			vector[i] = float32(float64(vector[i]) / math.Sqrt(norm))
		}
	}
	return response.Embeddings, nil
}

func Hash(text string) string { sum := sha256.Sum256([]byte(text)); return hex.EncodeToString(sum[:]) }

// Chunks retain Markdown headings and table contents. Paragraph boundaries are
// preferred; unusually long paragraphs are split with overlap, never truncated.
func Chunks(markdown string) ([]string, error) {
	markdown = strings.TrimSpace(strings.ReplaceAll(markdown, "\r\n", "\n"))
	if markdown == "" {
		return nil, errors.New("document text is empty")
	}
	if len(markdown) > 2<<20 {
		return nil, errors.New("document text is too large for similarity indexing")
	}
	var chunks []string
	const size = 1200
	for _, paragraph := range strings.Split(markdown, "\n\n") {
		runes := []rune(strings.TrimSpace(paragraph))
		if len(runes) == 0 {
			continue
		}
		if len(chunks) > 0 && len([]rune(chunks[len(chunks)-1]))+len(runes)+2 <= size {
			chunks[len(chunks)-1] += "\n\n" + string(runes)
			continue
		}
		for len(runes) > size {
			chunks = append(chunks, string(runes[:size]))
			runes = runes[size-120:]
		}
		chunks = append(chunks, string(runes))
	}
	if len(chunks) > 256 {
		return nil, errors.New("document has too many sections for similarity indexing")
	}
	return chunks, nil
}
