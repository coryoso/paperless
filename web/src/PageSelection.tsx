import { useEffect, useState } from "react";
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
  async function toggle(page: PageChoice) {
    setBusy(true);
    setError("");
    try {
      const included = pages
        .filter((p) => (p.page === page.page ? p.excluded : !p.excluded))
        .map((p) => p.page);
      const result = await api.savePageSelection(job.id, included);
      setPages(result.pages);
      setRevision((v) => v + 1);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      {pages.length > 0 && (
        <details
          className="page-selection"
          open={pages.some((p) => p.suggested_blank)}
        >
          <summary>
            {kept} of {pages.length} pages included
            {pages.some((p) => p.suggested_blank)
              ? " · blank pages suggested for removal"
              : ""}
          </summary>
          <p>
            Choose pages to keep in the filed PDF. The original scan is
            preserved.
          </p>
          <div className="page-choices">
            {pages.map((p) => (
              <label key={p.page} title={p.reason}>
                <input
                  type="checkbox"
                  checked={!p.excluded}
                  disabled={
                    busy ||
                    job.status !== "needs_review" ||
                    (!p.excluded && kept === 1)
                  }
                  onChange={() => toggle(p)}
                />
                Page {p.page}
                {p.suggested_blank ? " · likely blank" : ""}
              </label>
            ))}
          </div>
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
      {error && <div className="inline-error">{error}</div>}
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
