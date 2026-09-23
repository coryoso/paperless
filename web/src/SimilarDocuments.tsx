import { useEffect, useState } from "react";
import { FileSearch, LoaderCircle, RefreshCw } from "lucide-react";
import { api } from "./api";
import type { SimilarityResult } from "./types";

export function SimilarDocuments({ jobID }: { jobID: string }) {
  const [result, setResult] = useState<SimilarityResult | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    setResult(null);
    setError("");
    const load = async () => {
      try {
        const next = await api.similar(jobID, controller.signal);
        if (!active) return;
        setResult(next);
        if (next.status === "indexing") timer = setTimeout(load, 3000);
      } catch (reason) {
        if (active)
          setError(reason instanceof Error ? reason.message : String(reason));
      }
    };
    void load();
    return () => {
      active = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [jobID, attempt]);

  return (
    <section className="similar-documents" aria-labelledby="similar-title">
      <div className="route-head">
        <div>
          <span className="eyebrow">Compare & review</span>
          <h3 id="similar-title">Similar previously approved documents</h3>
        </div>
        <FileSearch aria-hidden="true" />
      </div>
      {error ? (
        <div className="inline-error" role="alert">
          {error}
        </div>
      ) : !result ? (
        <p role="status">
          <LoaderCircle className="spin" aria-hidden="true" /> Finding related
          documents…
        </p>
      ) : (
        <>
          {result.message && (
            <p role="status">
              {result.status === "indexing" && (
                <LoaderCircle className="spin" aria-hidden="true" />
              )}{" "}
              {result.message}
            </p>
          )}
          {result.status === "ready" && !result.matches.length && (
            <p>
              No approved examples are available yet. Approve documents to build
              your local comparison history.
            </p>
          )}
          {result.matches.length > 0 && (
            <>
              <p>
                Closest matches by content. Compare their purpose and recipient
                before choosing a destination.
              </p>
              <div className="similar-list">
                {result.matches.map((match) => (
                  <article key={match.job_id} className="similar-card">
                    <a
                      href={`/files/${encodeURIComponent(match.job_id)}/current`}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {match.filename}
                    </a>
                    <div className="similar-meta">
                      <span>
                        {match.recipient || "Recipient not recorded"} ·{" "}
                        {match.recipient_scope.replaceAll("_", " ")}
                      </span>
                    </div>
                    <p className="similar-folder">
                      Filed in <strong>{match.folder}</strong>
                    </p>
                    <details>
                      <summary>Compare matching passages</summary>
                      <div className="similar-passages">
                        <div>
                          <strong>This document</strong>
                          <blockquote>{match.query_excerpt}</blockquote>
                        </div>
                        <div>
                          <strong>Previously approved</strong>
                          <blockquote>{match.excerpt}</blockquote>
                        </div>
                      </div>
                    </details>
                  </article>
                ))}
              </div>
            </>
          )}
        </>
      )}
      {(error ||
        result?.status === "unavailable" ||
        result?.status === "ready") && (
        <button
          type="button"
          className="text-button"
          onClick={() => setAttempt((value) => value + 1)}
        >
          <RefreshCw aria-hidden="true" /> Refresh comparisons
        </button>
      )}
    </section>
  );
}
