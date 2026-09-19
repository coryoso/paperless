import type { Dashboard, OCRPage, RecipientProfile, TextLayout } from "./types";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || `${response.status} ${response.statusText}`);
  }
  return response.json() as Promise<T>;
}

export const api = {
  setModelProvider: (provider: "ollama" | "fm") => request<{ provider: string; restarting: boolean }>("/api/setup/model", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ provider }) }),
  chooseDocumentsDirectory: () => request<{ documents_directory: string; restarting: boolean }>("/api/setup/documents-directory", { method: "POST" }),
  openSharingSettings: () => request<{ ok: boolean }>("/api/setup/open-sharing-settings", { method: "POST" }),
  backupDatabase: () => request<{ path: string }>("/api/backups", { method: "POST" }),
  saveRecipient: (profile: RecipientProfile) => request<{ ok: boolean }>("/api/recipients", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(profile) }),
  dashboard: () => request<Dashboard>("/api/dashboard"),
  pages: (jobID: string) => request<{ pages: OCRPage[] }>(`/api/jobs/${jobID}/pages`),
  layout: (jobID: string) => request<TextLayout>(`/api/jobs/${jobID}/layout`),
  text: async (jobID: string) => {
    const response = await fetch(`/files/${jobID}/text`);
    if (!response.ok) throw new Error(await response.text());
    return response.text();
  },
  upload: (file: File) => {
    const body = new FormData();
    body.append("document", file);
    return request<{ run_id: string; job_id: string }>("/api/uploads", { method: "POST", body });
  },
  approve: (jobID: string, data: { folder: string; filename: string; document_type: string; physical_original_action?: string; recipient_profile_id?: number; recipient?: string; recipient_scope?: string }) =>
    request<{ final_path: string }>(`/api/jobs/${jobID}/approve`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    }),
  reject: (jobID: string) => request<{ ok: boolean }>(`/api/jobs/${jobID}/reject`, { method: "POST" }),
  retry: (jobID: string) => request<{ inbox_path: string }>(`/api/jobs/${jobID}/retry`, { method: "POST" }),
  refreshFolders: () => request<{ folders: string[] }>("/api/folders/refresh", { method: "POST" }),
};
