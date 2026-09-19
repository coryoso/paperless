package bonsai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"paperless/internal/config"
)

func TestAvailableChecksModelAndReadiness(t *testing.T) {
	for _, tt := range []struct {
		name, models, health string
		status               int
		wantOK               bool
	}{
		{"ready", `{"data":[{"id":"Bonsai-8B"}]}`, `{"status":"ok"}`, 200, true},
		{"wrong model", `{"data":[{"id":"other"}]}`, `{"status":"ok"}`, 200, false},
		{"loading", `{"data":[{"id":"Bonsai-8B"}]}`, `{"error":"Loading model"}`, 503, false},
		{"invalid list", `not json`, `{"status":"ok"}`, 200, false},
		{"empty list", `{"data":[]}`, `{"status":"ok"}`, 200, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					fmt.Fprint(w, tt.models)
				case "/v1/health":
					w.WriteHeader(tt.status)
					fmt.Fprint(w, tt.health)
				default:
					t.Errorf("unexpected URL %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			for _, suffix := range []string{"", "/", "/v1", "/v1/"} {
				err := Available(t.Context(), config.Bonsai{Endpoint: server.URL + suffix, Model: config.DefaultBonsaiModel})
				if (err == nil) != tt.wantOK {
					t.Fatalf("availability = %v", err)
				}
			}
		})
	}
}

func TestCompleteUsesConstrainedChatAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["model"] != "Bonsai-8B" || request["stream"] != false || request["max_tokens"] != float64(321) || request["reasoning_effort"] != "none" {
			t.Errorf("request = %+v", request)
		}
		format := request["response_format"].(map[string]any)
		if format["type"] != "json_schema" || format["json_schema"].(map[string]any)["schema"].(map[string]any)["type"] != "object" {
			t.Error("missing schema")
		}
		messages := request["messages"].([]any)
		instructions := messages[0].(map[string]any)["content"].(string)
		if !strings.HasPrefix(instructions, "instructions\n") || !strings.Contains(instructions, `{"type":"object"}`) || messages[1].(map[string]any)["content"] != "private document" {
			t.Error("missing prompt")
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL + "/v1"
	cfg.LLM.MaxOutputTokens = 321
	got, err := Complete(t.Context(), cfg, "instructions", "private document", map[string]any{"type": "object"})
	if err != nil || got != `{"ok":true}` {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestCompleteRejectsFailedOrIncompleteResponses(t *testing.T) {
	for _, body := range []string{`{}`, `not JSON`, `{"choices":[]}`, `{"choices":[{"message":{"content":"{}"},"finish_reason":"length"}]}`, `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		cfg := config.Default()
		cfg.Bonsai.Endpoint = server.URL
		if _, err := Complete(t.Context(), cfg, "", "", nil); err == nil {
			t.Errorf("accepted %s", body)
		}
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		fmt.Fprint(w, "private upstream details")
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL
	if _, err := Complete(t.Context(), cfg, "", "", nil); err == nil || !strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "private") {
		t.Fatalf("error=%v", err)
	}
}

func TestCompleteHonorsCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := Complete(ctx, cfg, "", "", nil); err == nil {
		t.Fatal("expected cancellation")
	}
}
