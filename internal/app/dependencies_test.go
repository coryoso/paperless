package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"paperless/internal/config"
)

func TestOllamaModelsIncludesNameAndModelTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"mistral:latest","model":"mistral:latest","capabilities":["completion"]},{"name":"qwen3.5:9b-q4_K_M","model":"qwen3.5:9b-q4_K_M","capabilities":["completion","thinking"]}]}`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.LLM.Endpoint = server.URL
	models, err := ollamaModels(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"qwen3.5:9b-q4_K_M", "mistral:latest"} {
		if !models[want] {
			t.Fatalf("model %q missing from %#v", want, models)
		}
	}
}

func TestSelectQwen35ModelPrefersThinkingFlavor(t *testing.T) {
	got, ok := selectQwen35Model([]ollamaModelInfo{
		{Name: "mistral:latest", Model: "mistral:latest", Capabilities: []string{"completion"}},
		{Name: "qwen3.5:4b", Model: "qwen3.5:4b", Capabilities: []string{"completion"}},
		{Name: "qwen3.5:9b-q4_K_M", Model: "qwen3.5:9b-q4_K_M", Capabilities: []string{"completion", "thinking"}},
	})
	if !ok {
		t.Fatal("expected model selection")
	}
	if got != "qwen3.5:9b-q4_K_M" {
		t.Fatalf("model = %q", got)
	}
}

func TestOllamaModelDetailExplainsMissingTag(t *testing.T) {
	ok, got := ollamaModelDetail("qwen3.5", nil)
	if ok {
		t.Fatal("expected missing model")
	}
	if got != "no installed Qwen 3.5 model; run `ollama pull qwen3.5:9b-q4_K_M`" {
		t.Fatalf("detail = %q", got)
	}
}
