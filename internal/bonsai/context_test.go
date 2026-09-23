package bonsai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"paperless/internal/config"
)

func TestContextCheckUsesRuntimeWindowAndCountsInstructions(t *testing.T) {
	for _, window := range []int{4096, 1024} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/props" {
				fmt.Fprintf(w, `{"default_generation_settings":{"n_ctx":%d}}`, window)
				return
			}
			if r.URL.Path != "/tokenize" {
				t.Error(r.URL.Path)
			}
			var body struct {
				Content string `json:"content"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if !strings.Contains(body.Content, "test instructions") || !strings.Contains(body.Content, "all pages") || !strings.Contains(body.Content, `"type":"object"`) {
				t.Error("incomplete tokenizer input")
			}
			json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, 512)})
		}))
		cfg := config.Default()
		cfg.Bonsai.Endpoint = server.URL + "/v1"
		err := CheckContext(t.Context(), cfg, "test instructions", "all pages", map[string]any{"type": "object"})
		server.Close()
		if (err != nil) != (window == 1024) {
			t.Fatalf("window=%d err=%v", window, err)
		}
	}
}
