import { afterEach, describe, expect, it, mock } from "bun:test";
import { api } from "./api";
import type { Dashboard } from "./types";

afterEach(() => mock.restore());

describe("dashboard API", () => {
  it("loads the database-backed dashboard", async () => {
    const payload: Dashboard = {
      settings: { inbox: "/inbox", archive_root: "/archive", archive_exists: true, scanner_share_checked: true, scanner_share_ready: true, model: "qwen3.5" },
      stats: { review: 0, archived: 0, failed: 0, total: 0 },
      folders: ["Tax/2026"],
      review_jobs: [],
      recent_jobs: [],
      all_jobs: [],
    };
    const fetchMock = mock(() => Promise.resolve(new Response(JSON.stringify(payload), { status: 200 })));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await expect(api.dashboard()).resolves.toEqual(payload);
    expect(fetch).toHaveBeenCalledWith("/api/dashboard", undefined);
  });

  it("submits the chosen archive destination as JSON", async () => {
    const fetchMock = mock(() => Promise.resolve(new Response(JSON.stringify({ final_path: "/archive/Tax/file.pdf" }), { status: 200 })));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await api.approve("job-12345678", {
      folder: "Tax/2026",
      filename: "2026-02-25__finanzamt__tax-letter.pdf",
      document_type: "tax-letter",
      physical_original_action: "keep_original",
    });

    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toMatchObject({ folder: "Tax/2026", document_type: "tax-letter" });
  });
});
