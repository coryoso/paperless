package embeddings

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"paperless/internal/config"
)

func TestEmbedValidatesAndNormalizes(t *testing.T) {
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"normalized", `{"embeddings":[[3,4]]}`, true},
		{"zero", `{"embeddings":[[0,0]]}`, false},
		{"missing", `{"embeddings":[]}`, false},
		{"empty", `{"embeddings":[[]]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/embed" {
					t.Errorf("path=%s", r.URL.Path)
				}
				var request struct {
					Truncate bool   `json:"truncate"`
					Model    string `json:"model"`
				}
				request.Truncate = true
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Truncate || request.Model != "embeddinggemma" {
					t.Errorf("request=%+v", request)
				}
				w.Write([]byte(test.body))
			}))
			defer server.Close()
			cfg := config.Default().Embeddings
			cfg.Endpoint = server.URL
			vectors, err := (Client{Config: cfg}).Embed(t.Context(), []string{"Rechnung"})
			if !test.valid {
				if err == nil {
					t.Fatal("accepted invalid vector")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(float64(vectors[0][0])-.6) > .00001 {
				t.Fatal(vectors)
			}
		})
	}
}

func TestIdentityTracksModelWeights(t *testing.T) {
	digest := "v1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "embeddinggemma:latest", "digest": digest}}})
	}))
	defer server.Close()
	cfg := config.Default().Embeddings
	cfg.Endpoint = server.URL
	client := Client{Config: cfg}
	first, err := client.Identity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	digest = "v2"
	second, err := client.Identity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("mutable model tag mixed different weights")
	}
}

func TestChunksPreserveLongUnicodeDocument(t *testing.T) {
	text := "# Versicherung\n\n" + strings.Repeat("Änderung des Vertrags. ", 200) + "FINAL CLAUSE"
	chunks, err := Chunks(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 || !strings.HasSuffix(chunks[len(chunks)-1], "FINAL CLAUSE") {
		t.Fatal("lost document tail")
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 1200 || strings.ContainsRune(chunk, '\uFFFD') {
			t.Fatal("bad chunk")
		}
	}
	if _, err = Chunks(" \n"); err == nil {
		t.Fatal("accepted empty text")
	}
}
