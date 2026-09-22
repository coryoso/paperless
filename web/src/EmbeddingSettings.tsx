import { useEffect, useState } from "react";
import { api, waitForSetup } from "./api";
import type { Dashboard } from "./types";

export function EmbeddingSettings({
  dashboard,
  onRefresh,
}: {
  dashboard: Dashboard;
  onRefresh: () => Promise<void>;
}) {
  const settings = dashboard.settings;
  const [enabled, setEnabled] = useState(!!settings.embeddings_enabled);
  const [model, setModel] = useState(
    settings.embedding_model || "embeddinggemma",
  );
  const [endpoint, setEndpoint] = useState(
    settings.embedding_endpoint || "http://127.0.0.1:11434",
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    setEnabled(!!settings.embeddings_enabled);
    setModel(settings.embedding_model || "embeddinggemma");
    setEndpoint(settings.embedding_endpoint || "http://127.0.0.1:11434");
  }, [
    settings.embeddings_enabled,
    settings.embedding_model,
    settings.embedding_endpoint,
  ]);
  useEffect(() => {
    if (!settings.embeddings_enabled || busy) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        await onRefresh();
      } finally {
        if (active) timer = setTimeout(refresh, 2500);
      }
    };
    timer = setTimeout(refresh, 2500);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [settings.embeddings_enabled, busy, onRefresh]);
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await api.setEmbeddings({ enabled, model, endpoint });
      await waitForSetup({
        embeddings_enabled: enabled,
        embedding_model: model.trim(),
        embedding_endpoint: endpoint.trim(),
      });
      await onRefresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="setup-wizard embedding-settings">
      <div className="setup-step-copy">
        <h3>Compare similar documents</h3>
        <p>
          Find previously approved documents with related content. Uses a
          separate local embedding model, alongside Ollama, Apple Foundation
          Models, or Bonsai classification.
        </p>
        <label className="embedding-toggle">
          <input
            type="checkbox"
            checked={enabled}
            disabled={busy}
            onChange={(event) => setEnabled(event.target.checked)}
          />{" "}
          Enable document similarity
        </label>
        <p>
          Install and start Ollama, then download the embedding model in
          Terminal:
        </p>
        <code>
          brew install ollama
          <br />
          brew services start ollama
          <br />
          ollama pull embeddinggemma
        </code>
        <p>
          If Ollama is already running, only the model download is needed.
          Documents stay on this Mac. Existing documents are indexed in the
          background after enabling.
        </p>
        <details>
          <summary>Embedding model settings</summary>
          <label className="model-choice">
            <span>Ollama embedding model</span>
            <input
              value={model}
              disabled={busy}
              onChange={(event) => setModel(event.target.value)}
            />
          </label>
          <label className="model-choice">
            <span>Local Ollama address</span>
            <input
              value={endpoint}
              disabled={busy}
              onChange={(event) => setEndpoint(event.target.value)}
            />
          </label>
          <p>
            For another model, install it with Ollama first. Changing models
            rebuilds the index.
          </p>
        </details>
        {settings.embeddings_enabled && (
          <p role="status">
            {dashboard.similarity?.status === "unavailable"
              ? dashboard.similarity.message
              : `${dashboard.similarity?.indexed ?? 0} documents indexed${dashboard.similarity?.status === "indexing" ? " · indexing…" : ""}`}
            {!!dashboard.similarity?.skipped &&
              ` · ${dashboard.similarity.skipped} documents skipped`}
          </p>
        )}
        <p>
          Saving restarts Paperless. Wait for active uploads to finish first.
        </p>
        {error && (
          <div className="form-error" role="alert">
            {error}
          </div>
        )}
      </div>
      <button
        type="button"
        className="primary-button"
        disabled={busy || !model.trim() || !endpoint.trim()}
        onClick={save}
      >
        {busy ? "Checking & saving…" : "Save similarity settings"}
      </button>
    </section>
  );
}
