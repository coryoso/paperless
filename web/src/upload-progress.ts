import type { Job, ProgressEvent, UploadProgress } from "./types";
import { isProcessingStatus } from "./upload";

export type UploadTaskState =
  | "uploading"
  | "processing"
  | "complete"
  | "failed";
export type UploadTask = {
  id: string;
  jobID: string;
  runID: string;
  filename: string;
  size: number;
  createdAt: string;
  state: UploadTaskState;
  events: ProgressEvent[];
  serverEventCount: number;
  error: string;
};

export function mergeUploadProgress(
  task: UploadTask,
  snapshots: UploadProgress[],
): UploadTask {
  const snapshot = snapshots.find(
    (item) =>
      item.client_upload_id === task.id ||
      (task.runID && item.run_id === task.runID),
  );
  if (!snapshot || snapshot.events.length <= task.serverEventCount) return task;
  const last = snapshot.events.at(-1);
  if (!last) return task;
  const terminal = task.state === "complete" || task.state === "failed";
  const events = [task.events[0], ...snapshot.events];
  // A dashboard snapshot may already have reconciled completion after a restart.
  const previous = task.events.at(-1);
  if (terminal && !last.done && previous?.done) events.push(previous);
  return {
    ...task,
    runID: snapshot.run_id,
    events,
    serverEventCount: snapshot.events.length,
    state: last.done
      ? last.level === "error"
        ? "failed"
        : "complete"
      : terminal
        ? task.state
        : "processing",
    error: last.done
      ? last.level === "error"
        ? last.message
        : ""
      : task.error,
  };
}

// Run histories are in memory. Durable job state still settles uploads when a
// hidden tab returns after the service restarted or its history was pruned.
export function reconcileUpload(task: UploadTask, jobs: Job[]): UploadTask {
  if (task.state === "complete" || task.state === "failed") return task;
  const job = jobs.find((item) => item.id === task.jobID);
  if (!job || isProcessingStatus(job.status)) return task;
  const failed = job.status === "failed";
  const message = failed
    ? job.error || "Processing failed."
    : "Analysis complete.";
  return {
    ...task,
    state: failed ? "failed" : "complete",
    error: failed ? message : "",
    events: [
      ...task.events,
      {
        at: job.updated_at,
        level: failed ? "error" : "info",
        phase: failed ? "failed" : "complete",
        step: failed ? "error" : "done",
        message,
        percent: failed ? (task.events.at(-1)?.percent ?? 0) : 100,
        done: true,
      },
    ],
  };
}
