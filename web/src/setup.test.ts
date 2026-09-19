import { describe, expect, it, mock } from "bun:test";
import { waitForSetup } from "./api";
import type { Dashboard } from "./types";

describe("setup reload", () => {
  it("waits past a stale dashboard until saved settings are active", async () => {
    const original = globalThis.fetch;
    let calls = 0;
    const settings = { setup_step: "model", model_provider: "ollama", model_enabled: true } as Dashboard["settings"];
    globalThis.fetch = mock(() => {
      calls++;
      return Promise.resolve(new Response(JSON.stringify({ settings: calls === 1 ? settings : { ...settings, setup_step: "ready", model_enabled: false } })));
    }) as unknown as typeof fetch;
    try {
      await waitForSetup({ setup_step: "ready", model_enabled: false });
      expect(calls).toBe(2);
    } finally { globalThis.fetch = original; }
  });
  it("retries while the service is restarting", async () => {
    const original = globalThis.fetch;
    let calls = 0;
    globalThis.fetch = mock(() => {
      calls++;
      if (calls === 1) return Promise.reject(new Error("connection refused"));
      return Promise.resolve(new Response(JSON.stringify({ settings: { setup_required: false } })));
    }) as unknown as typeof fetch;
    try {
      await waitForSetup({ setup_required: false });
      expect(calls).toBe(2);
    } finally { globalThis.fetch = original; }
  });
});
