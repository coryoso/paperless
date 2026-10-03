import { useEffect, useState } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";
import { api } from "./api";
import type { Job } from "./types";
export type PageChoice = {
  page: number;
  suggested_blank: boolean;
  excluded: boolean;
  reason: string;
};
export function PDFPreview({ job }: { job: Job }) {
  const [pages, setPages] = useState<PageChoice[]>([]);
  const [revision, setRevision] = useState(0);
  const [original, setOriginal] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    api
      .pageSelection(job.id, controller.signal)
      .then((result) => setPages(result.pages))
      .catch((e) => {
        if (!controller.signal.aborted) setError(String(e));
      });
    return () => controller.abort();
  }, [job.id]);
  const kept = pages.filter((p) => !p.excluded).length;
  const editable = job.status === "needs_review";
  async function save(next: PageChoice[]) {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const included = next.filter((p) => !p.excluded).map((p) => p.page);
      const result = await api.savePageSelection(
        job.id,
        included,
        next.map((p) => p.page),
      );
      setPages(result.pages);
      setRevision((v) => v + 1);
      setOriginal(false);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  function move(index: number, direction: number) {
    const next = [...pages];
    const [page] = next.splice(index, 1);
    next.splice(index + direction, 0, page);
    void save(next);
  }
  return (
    <>
      {pages.length > 0 && (
        <details
          className="page-selection"
          open={pages.some((p) => p.suggested_blank)}
        >
          <summary>
            Page tools · {kept} of {pages.length} pages included
            {pages.some((p) => p.suggested_blank)
              ? " · blank pages suggested for removal"
              : ""}
          </summary>
          <p>
            {editable
              ? "Choose pages to keep and use the arrows to change their order. Changes save automatically and update the preview."
              : "Pages included in the filed PDF, in their saved order."}{" "}
            Page numbers refer to the original scan, which is preserved.
          </p>
          <ol className="page-choices" aria-label="Page order" aria-busy={busy}>
            {pages.map((p, index) => (
              <li
                key={p.page}
                className={p.excluded ? "page-choice excluded" : "page-choice"}
              >
                <label title={p.reason}>
                  <input
                    type="checkbox"
                    checked={!p.excluded}
                    disabled={busy || !editable || (!p.excluded && kept === 1)}
                    onChange={() =>
                      void save(
                        pages.map((page) =>
                          page.page === p.page
                            ? { ...page, excluded: !page.excluded }
                            : page,
                        ),
                      )
                    }
                  />
                  Page {p.page}
                  {p.suggested_blank ? " · likely blank" : ""}
                </label>
                {editable && (
                  <div className="page-order-actions">
                    <button
                      type="button"
                      className="secondary-button"
                      aria-label={`Move page ${p.page} up`}
                      title="Move up"
                      disabled={busy || index === 0}
                      onClick={() => move(index, -1)}
                    >
                      <ArrowUp size={16} aria-hidden="true" />
                    </button>
                    <button
                      type="button"
                      className="secondary-button"
                      aria-label={`Move page ${p.page} down`}
                      title="Move down"
                      disabled={busy || index === pages.length - 1}
                      onClick={() => move(index, 1)}
                    >
                      <ArrowDown size={16} aria-hidden="true" />
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ol>
          {editable && (
            <button
              type="button"
              className="secondary-button"
              disabled={busy || pages.every((p, i) => p.page === i + 1)}
              onClick={() =>
                void save([...pages].sort((a, b) => a.page - b.page))
              }
            >
              Restore original order
            </button>
          )}
          {busy && <p role="status">Saving page changes…</p>}
          <label className="full-pdf-toggle">
            <input
              type="checkbox"
              checked={original}
              onChange={(e) => setOriginal(e.target.checked)}
            />
            Show all original pages for comparison
          </label>
        </details>
      )}
      {error && (
        <div className="inline-error" role="alert">
          {error}
        </div>
      )}
      <iframe
        title="Searchable PDF"
        className="pdf-frame"
        src={
          original
            ? job.urls.raw
            : `/api/jobs/${encodeURIComponent(job.id)}/selected.pdf?revision=${revision}`
        }
      />
    </>
  );
}
