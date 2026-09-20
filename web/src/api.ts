import type {
  Dashboard,
  SimilarityResult,
  OCRPage,
  RecipientProfile,
  TextLayout,
} from "./types";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  if (!response.ok) {
    let message = await response.text();
    try {
      const body = JSON.parse(message) as { error?: unknown };
      if (typeof body.error === "string") message = body.error;
    } catch {
      /* Non-JSON errors already contain readable text. */
    }
    throw new Error(message || `${response.status} ${response.statusText}`);
  }
  return response.json() as Promise<T>;
}

export const api = {
  similar: (jobID: string, signal?: AbortSignal) =>
    request<SimilarityResult>(
      `/api/jobs/${encodeURIComponent(jobID)}/similar`,
      { signal },
    ),
  setEmbeddings: (data: {
    enabled: boolean;
    model: string;
    endpoint: string;
  }) =>
    request<{ restarting: boolean }>("/api/setup/embeddings", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    }),
  setModelProvider: (provider: "ollama" | "fm" | "bonsai", enabled?: boolean) =>
    request<{ provider: string; restarting: boolean }>("/api/setup/model", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        provider,
        ...(enabled === undefined ? {} : { enabled }),
      }),
    }),
  installBonsai,
  setupProgress: (step: "model" | "complete") =>
    request<{ step: string; restarting: boolean }>("/api/setup/progress", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ step }),
    }),
  chooseDocumentsDirectory: () =>
    request<{ documents_directory: string; restarting: boolean }>(
      "/api/setup/documents-directory",
      { method: "POST" },
    ),
  openSharingSettings: () =>
    request<{ ok: boolean }>("/api/setup/open-sharing-settings", {
      method: "POST",
    }),
  backupDatabase: () =>
    request<{ path: string }>("/api/backups", { method: "POST" }),
  saveRecipient: (profile: RecipientProfile) =>
    request<{ ok: boolean }>("/api/recipients", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(profile),
    }),
  saveRecipientAddresses: (addresses: string[]) =>
    request<{ ok: boolean }>("/api/recipient-addresses", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ addresses }),
    }),
  dashboard: (signal: AbortSignal = AbortSignal.timeout(10000)) =>
    request<Dashboard>("/api/dashboard", { cache: "no-store", signal }),
  pages: (jobID: string) =>
    request<{ pages: OCRPage[] }>(`/api/jobs/${jobID}/pages`),
  layout: (jobID: string) => request<TextLayout>(`/api/jobs/${jobID}/layout`),
  text: async (jobID: string) => {
    const response = await fetch(`/files/${jobID}/text`);
    if (!response.ok) throw new Error(await response.text());
    return response.text();
  },
  upload: (file: File, uploadID: string) => {
    const body = new FormData();
    body.append("document", file);
    body.append("upload_id", uploadID);
    return request<{ run_id: string; job_id: string }>("/api/uploads", {
      method: "POST",
      body,
    });
  },
  approve: (
    jobID: string,
    data: {
      folder: string;
      filename: string;
      document_type: string;
      physical_original_action?: string;
      recipient_profile_id?: number;
      recipient?: string;
      recipient_scope?: string;
    },
  ) =>
    request<{ final_path: string }>(`/api/jobs/${jobID}/approve`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    }),
  reject: (jobID: string) =>
    request<{ ok: boolean }>(`/api/jobs/${jobID}/reject`, { method: "POST" }),
  retry: (jobID: string) =>
    request<{ inbox_path: string }>(`/api/jobs/${jobID}/retry`, {
      method: "POST",
    }),
  refreshFolders: () =>
    request<{ folders: string[] }>("/api/folders/refresh", { method: "POST" }),
};

async function installBonsai(
  onProgress: (message: string) => void,
): Promise<void> {
  const response = await fetch("/api/setup/bonsai/install", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ install: true }),
  });
  if (!response.ok) throw new Error(await response.text());
  if (!response.body) throw new Error("No installation progress received.");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let pending = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      pending += decoder.decode(value, { stream: !done });
      const lines = pending.split("\n");
      pending = lines.pop() ?? "";
      for (const line of lines) {
        if (!line.trim()) continue;
        const event = JSON.parse(line) as {
          message?: string;
          error?: string;
          done?: boolean;
        };
        if (event.error) throw new Error(event.error);
        if (event.message) onProgress(event.message);
        if (event.done) return;
      }
      if (done)
        throw new Error(
          "Installation connection closed. Retry to resume the download.",
        );
    }
  } finally {
    await reader.cancel();
    reader.releaseLock();
  }
}

// Wait for the saved choices to be loaded, including when the HTTP server
// briefly closes during a setup restart. Ignore stale dashboards from before it.
export async function waitForSetup(
  expected: Partial<Dashboard["settings"]>,
): Promise<void> {
  for (let attempt = 0; attempt < 30; attempt++) {
    try {
      const response = await fetch("/api/dashboard", {
        signal: AbortSignal.timeout(3000),
        cache: "no-store",
      });
      if (response.ok) {
        const dashboard = (await response.json()) as Dashboard;
        if (
          Object.entries(expected).every(
            ([key, value]) =>
              dashboard.settings[key as keyof Dashboard["settings"]] === value,
          )
        )
          return;
      }
    } catch {
      /* The service may be restarting. */
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(
    "Your settings were saved, but Paperless is still restarting. Refresh this page to resume setup.",
  );
}
