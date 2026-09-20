import type { UploadProgress } from "./types";

// A single SSE connection carries dashboard changes and all upload progress.
// Reconnecting replays retained upload histories and invalidates the dashboard.
export function startDashboardRefresh(
  refresh: () => void,
  onUploads: (updates: UploadProgress[]) => void = () => {},
  page: Pick<
    Document,
    "visibilityState" | "addEventListener" | "removeEventListener"
  > = document,
  browser: Pick<Window, "addEventListener" | "removeEventListener"> = window,
  connect: (url: string) => EventSource = (url) => new EventSource(url),
): () => void {
  let stream: EventSource | null = null;
  const update = () => {
    if (page.visibilityState === "visible") refresh();
  };
  const uploads = (message: Event) => {
    const snapshots = JSON.parse(
      (message as MessageEvent).data,
    ) as UploadProgress[];
    onUploads(snapshots);
    if (snapshots.some((snapshot) => snapshot.events.at(-1)?.done)) update();
  };
  const disconnect = () => {
    stream?.removeEventListener("dashboard", update);
    stream?.removeEventListener("uploads", uploads);
    stream?.close();
    stream = null;
  };
  const resume = () => {
    if (page.visibilityState !== "visible") {
      disconnect();
      return;
    }
    if (!stream || stream.readyState === 2) {
      disconnect();
      stream = connect("/api/dashboard/events");
      stream.addEventListener("dashboard", update);
      stream.addEventListener("uploads", uploads);
    }
  };
  const catchUp = () => {
    resume();
    update();
  };
  resume();
  page.addEventListener("visibilitychange", resume);
  browser.addEventListener("focus", catchUp);
  browser.addEventListener("online", catchUp);
  return () => {
    disconnect();
    page.removeEventListener("visibilitychange", resume);
    browser.removeEventListener("focus", catchUp);
    browser.removeEventListener("online", catchUp);
  };
}

// Coalesce bursts, but always fetch again if a change arrives during a request:
// that request may have read the database before the change was committed.
export function createDashboardLoader<T>(
  fetchSnapshot: (signal: AbortSignal) => Promise<T>,
  accept: (snapshot: T) => void,
  failed: (reason: unknown) => void,
) {
  let pending: Promise<void> | null = null;
  let dirty = false;
  let disposed = false;
  let retry: ReturnType<typeof setTimeout> | undefined;
  const controller = new AbortController();
  const load = (): Promise<void> => {
    if (disposed) return Promise.resolve();
    clearTimeout(retry);
    dirty = true;
    if (pending) return pending;
    pending = (async () => {
      while (dirty && !disposed) {
        clearTimeout(retry);
        dirty = false;
        try {
          const snapshot = await fetchSnapshot(
            AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
          );
          if (!disposed) accept(snapshot);
        } catch (reason) {
          if (!disposed) {
            failed(reason);
            // The SSE connection can be healthy while a snapshot request fails.
            // Retry failures even if no further document events arrive.
            retry = setTimeout(() => void load(), 1500);
          }
        }
      }
    })().finally(() => {
      pending = null;
      if (dirty && !disposed) void load();
    });
    return pending;
  };
  return {
    load,
    dispose: () => {
      disposed = true;
      clearTimeout(retry);
      controller.abort();
    },
  };
}
