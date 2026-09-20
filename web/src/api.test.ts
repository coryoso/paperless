import { afterEach, describe, expect, it, mock } from "bun:test";
import { api } from "./api";
import type { Dashboard } from "./types";

afterEach(() => mock.restore());

it("shows a readable setup error when the selected model is unavailable", async () => {
  globalThis.fetch = mock(() =>
    Promise.resolve(
      new Response(
        JSON.stringify({ error: "Start Ollama before continuing." }),
        { status: 400 },
      ),
    ),
  ) as unknown as typeof fetch;
  await expect(api.setModelProvider("ollama", true)).rejects.toThrow(
    /^Start Ollama before continuing\.$/,
  );
});

describe("dashboard API", () => {
  it("saves Apple Foundation Models as the provider", async () => {
    const fetchMock = mock(() =>
      Promise.resolve(
        new Response(JSON.stringify({ provider: "fm", restarting: true }), {
          status: 202,
        }),
      ),
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    await expect(api.setModelProvider("fm")).resolves.toEqual({
      provider: "fm",
      restarting: true,
    });
    expect(fetch).toHaveBeenCalledWith("/api/setup/model", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ provider: "fm" }),
    });
  });
  it("loads the database-backed dashboard", async () => {
    const payload: Dashboard = {
      settings: {
        inbox: "/inbox",
        archive_root: "/archive",
        archive_exists: true,
        archive_error: "",
        setup_required: false,
        setup_step: "complete",
        scanner_share_checked: true,
        scanner_share_ready: true,
        model: "qwen3.5",
        model_provider: "ollama",
        model_enabled: true,
      },
      stats: { review: 0, archived: 0, failed: 0, total: 0 },
      folders: ["Tax/2026"],
      review_jobs: [],
      recent_jobs: [],
      all_jobs: [],
    };
    const fetchMock = mock(() =>
      Promise.resolve(new Response(JSON.stringify(payload), { status: 200 })),
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await expect(api.dashboard()).resolves.toEqual(payload);
    expect(fetch).toHaveBeenCalledWith("/api/dashboard", undefined);
  });

  it("starts the native documents-directory chooser", async () => {
    const payload = { documents_directory: "/Documents", restarting: true };
    const fetchMock = mock(() =>
      Promise.resolve(new Response(JSON.stringify(payload), { status: 202 })),
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await expect(api.chooseDocumentsDirectory()).resolves.toEqual(payload);
    expect(fetch).toHaveBeenCalledWith("/api/setup/documents-directory", {
      method: "POST",
    });
  });

  it("submits the chosen archive destination as JSON", async () => {
    const fetchMock = mock(() =>
      Promise.resolve(
        new Response(JSON.stringify({ final_path: "/archive/Tax/file.pdf" }), {
          status: 200,
        }),
      ),
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await api.approve("job-12345678", {
      folder: "Tax/2026",
      filename: "2026-02-25__finanzamt__tax-letter.pdf",
      document_type: "tax-letter",
      physical_original_action: "keep_original",
    });

    const [, init] = fetchMock.mock.calls[0] as unknown as [
      string,
      RequestInit,
    ];
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toMatchObject({
      folder: "Tax/2026",
      document_type: "tax-letter",
    });
  });
});

describe("Bonsai setup", () => {
  it("saves Bonsai as the provider", async () => {
    globalThis.fetch = mock(() =>
      Promise.resolve(
        new Response(JSON.stringify({ provider: "bonsai", restarting: true }), {
          status: 202,
        }),
      ),
    ) as unknown as typeof fetch;
    await expect(api.setModelProvider("bonsai")).resolves.toEqual({
      provider: "bonsai",
      restarting: true,
    });
  });
  it("reads installation updates split across network chunks", async () => {
    const chunks = [
      '{"message":"Down',
      'loading…"}\n{"message":"Ready"}\n',
      '{"done":true}\n',
    ];
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const chunk of chunks)
          controller.enqueue(new TextEncoder().encode(chunk));
        controller.close();
      },
    });
    globalThis.fetch = mock(() =>
      Promise.resolve(new Response(stream)),
    ) as unknown as typeof fetch;
    const updates: string[] = [];
    await api.installBonsai((message) => updates.push(message));
    expect(updates).toEqual(["Downloading…", "Ready"]);
    expect(fetch).toHaveBeenCalledWith("/api/setup/bonsai/install", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: '{"install":true}',
    });
  });
  it("reports installation failures and premature disconnects", async () => {
    for (const [body, error] of [
      ['{"error":"Download failed"}\n', "Download failed"],
      ['{"message":"Downloading"}\n', "connection closed"],
    ]) {
      globalThis.fetch = mock(() =>
        Promise.resolve(new Response(body)),
      ) as unknown as typeof fetch;
      await expect(api.installBonsai(() => {})).rejects.toThrow(error);
    }
  });
  it("reports rejected installation requests", async () => {
    globalThis.fetch = mock(() =>
      Promise.resolve(new Response("Already installing", { status: 409 })),
    ) as unknown as typeof fetch;
    await expect(api.installBonsai(() => {})).rejects.toThrow(
      "Already installing",
    );
  });
});

describe("first-run guide", () => {
  it("saves the choice to continue without AI", async () => {
    globalThis.fetch = mock(() =>
      Promise.resolve(
        new Response(JSON.stringify({ provider: "ollama", restarting: true }), {
          status: 202,
        }),
      ),
    ) as unknown as typeof fetch;
    await api.setModelProvider("ollama", false);
    expect(fetch).toHaveBeenCalledWith("/api/setup/model", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: '{"provider":"ollama","enabled":false}',
    });
  });
  it("records progress and explicit completion", async () => {
    globalThis.fetch = mock(() =>
      Promise.resolve(
        new Response(JSON.stringify({ restarting: true }), { status: 202 }),
      ),
    ) as unknown as typeof fetch;
    await api.setupProgress("model");
    expect(fetch).toHaveBeenLastCalledWith("/api/setup/progress", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: '{"step":"model"}',
    });
    await api.setupProgress("complete");
    expect(fetch).toHaveBeenLastCalledWith("/api/setup/progress", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: '{"step":"complete"}',
    });
  });
});
