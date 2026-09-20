import { afterEach, expect, mock, spyOn, test } from "bun:test";
import { createDashboardLoader, startDashboardRefresh } from "./refresh";

afterEach(() => mock.restore());

class Stream extends EventTarget {
  readyState = 1;
  close = mock(() => { this.readyState = 2; });
}

function setup() {
  const page = Object.assign(new EventTarget(), { visibilityState: "visible" as DocumentVisibilityState });
  const browser = new EventTarget();
  const refresh = mock(() => {});
  const streams: Stream[] = [];
  const connect = mock((_url: string) => {
    const stream = new Stream();
    streams.push(stream);
    return stream as unknown as EventSource;
  });
  const stop = startDashboardRefresh(refresh, page, browser, connect);
  return { page, browser, refresh, streams, connect, stop };
}

test("uses same-origin SSE and reloads on changes and reconnect snapshots without polling", () => {
  const interval = spyOn(globalThis, "setInterval");
  const { refresh, streams, connect, stop } = setup();
  expect(connect).toHaveBeenCalledWith("/api/dashboard/events");
  expect(refresh).not.toHaveBeenCalled();
  streams[0].dispatchEvent(new Event("dashboard"));
  streams[0].dispatchEvent(new Event("dashboard"));
  expect(refresh).toHaveBeenCalledTimes(2);
  expect(interval).not.toHaveBeenCalled();
  stop();
});

test("closes hidden streams and reconnects on return, with focus/online catch-up", () => {
  const { page, browser, refresh, streams, connect, stop } = setup();
  page.visibilityState = "hidden";
  page.dispatchEvent(new Event("visibilitychange"));
  streams[0].dispatchEvent(new Event("dashboard"));
  browser.dispatchEvent(new Event("focus"));
  expect(streams[0].close).toHaveBeenCalledTimes(1);
  expect(refresh).not.toHaveBeenCalled();
  page.visibilityState = "visible";
  page.dispatchEvent(new Event("visibilitychange"));
  expect(connect).toHaveBeenCalledTimes(2);
  streams[1].dispatchEvent(new Event("dashboard"));
  browser.dispatchEvent(new Event("focus"));
  browser.dispatchEvent(new Event("online"));
  expect(refresh).toHaveBeenCalledTimes(3);
  expect(connect).toHaveBeenCalledTimes(2);
  stop();
  streams[1].dispatchEvent(new Event("dashboard"));
  page.dispatchEvent(new Event("visibilitychange"));
  browser.dispatchEvent(new Event("online"));
  expect(refresh).toHaveBeenCalledTimes(3);
  expect(streams[1].close).toHaveBeenCalledTimes(1);
});

test("revives permanently closed streams without duplicating reconnecting streams", () => {
  const { browser, streams, connect, stop } = setup();
  streams[0].readyState = 0;
  browser.dispatchEvent(new Event("online"));
  expect(connect).toHaveBeenCalledTimes(1);
  streams[0].readyState = 2;
  browser.dispatchEvent(new Event("online"));
  expect(connect).toHaveBeenCalledTimes(2);
  stop();
});

test("coalesces events during a slow request into one trailing fresh snapshot", async () => {
  let resolve!: (value: number) => void;
  const fetch = mock(() => new Promise<number>(r => { resolve = r; }));
  const accept = mock((_snapshot: number) => {});
  const loader = createDashboardLoader(fetch, accept, () => {});
  const first = loader.load();
  void loader.load();
  void loader.load();
  expect(fetch).toHaveBeenCalledTimes(1);
  resolve(1);
  await Promise.resolve();
  expect(fetch).toHaveBeenCalledTimes(2);
  resolve(2);
  await first;
  expect(accept.mock.calls).toEqual([[1], [2]]);
  loader.dispose();
});

test("retries a failed snapshot even without another SSE event", async () => {
  const timers: { callback: () => void; delay: number }[] = [];
  const realTimeout = globalThis.setTimeout;
  spyOn(globalThis, "setTimeout").mockImplementation(((callback: () => void, delay: number) => {
    timers.push({ callback, delay });
    return 123;
  }) as typeof setTimeout);
  const clear = spyOn(globalThis, "clearTimeout").mockImplementation(() => {});
  const fetch = mock(async () => { if (fetch.mock.calls.length === 1) throw new Error("Offline"); return 2; });
  const accept = mock((_snapshot: number) => {});
  const failed = mock(() => {});
  const loader = createDashboardLoader(fetch, accept, failed);
  await loader.load();
  expect(failed).toHaveBeenCalledTimes(1);
  expect(accept).not.toHaveBeenCalled();
  expect(timers[0].delay).toBe(1500);
  timers[0].callback();
  // Let the retry and its cleanup finish without another call to load().
  await new Promise(resolve => realTimeout(resolve, 0));
  expect(accept).toHaveBeenCalledWith(2);
  expect(fetch).toHaveBeenCalledTimes(2);
  loader.dispose();
  expect(clear).toHaveBeenCalled();
});

test("aborts requests and suppresses late results after disposal", async () => {
  let resolve!: (value: number) => void;
  let signal!: AbortSignal;
  const fetch = mock((s: AbortSignal) => { signal = s; return new Promise<number>(r => { resolve = r; }); });
  const accept = mock((_snapshot: number) => {});
  const loader = createDashboardLoader(fetch, accept, () => {});
  const pending = loader.load();
  loader.dispose();
  expect(signal.aborted).toBe(true);
  resolve(1);
  await pending;
  await loader.load();
  expect(accept).not.toHaveBeenCalled();
  expect(fetch).toHaveBeenCalledTimes(1);
});
