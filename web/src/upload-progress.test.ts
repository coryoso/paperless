import { expect, test } from "bun:test";
import type { Job, ProgressEvent, UploadProgress } from "./types";
import {
  mergeUploadProgress,
  reconcileUpload,
  type UploadTask,
} from "./upload-progress";

const sending: ProgressEvent = {
  at: "2026-09-20T12:00:00Z",
  level: "info",
  phase: "upload",
  step: "sending",
  message: "Uploading document.",
  percent: 3,
};
const received: ProgressEvent = { ...sending, step: "received", percent: 4 };
const done: ProgressEvent = {
  ...sending,
  phase: "complete",
  step: "done",
  percent: 100,
  done: true,
};
const task = (): UploadTask => ({
  id: "client-1",
  runID: "",
  jobID: "",
  filename: "scan.pdf",
  size: 12,
  createdAt: sending.at,
  state: "uploading",
  events: [sending],
  serverEventCount: 0,
  error: "",
});
const snapshot = (events: ProgressEvent[]): UploadProgress => ({
  client_upload_id: "client-1",
  run_id: "run-1",
  events,
});

test("routes progress before the upload response and ignores other uploads", () => {
  const original = task();
  expect(
    mergeUploadProgress(original, [
      { ...snapshot([received]), client_upload_id: "other" },
    ]),
  ).toBe(original);
  const next = mergeUploadProgress(original, [snapshot([received, done])]);
  expect(next.runID).toBe("run-1");
  expect(next.jobID).toBe("");
  expect(next.state).toBe("complete");
  expect(next.events).toEqual([sending, received, done]);
});

test("reconnect replay and stale snapshots do not duplicate or regress progress", () => {
  const first = mergeUploadProgress(task(), [snapshot([received])]);
  const complete = mergeUploadProgress(first, [snapshot([received, done])]);
  expect(mergeUploadProgress(complete, [snapshot([received, done])])).toBe(
    complete,
  );
  expect(mergeUploadProgress(complete, [snapshot([received])])).toBe(complete);
  const failed = mergeUploadProgress(first, [
    snapshot([received, { ...done, level: "error", message: "OCR failed" }]),
  ]);
  expect(failed.state).toBe("failed");
  expect(failed.error).toBe("OCR failed");
});

test("durable job state settles an upload when its run history was lost", () => {
  const active = {
    ...mergeUploadProgress(task(), [snapshot([received])]),
    jobID: "job-1",
  };
  const job = {
    id: "job-1",
    status: "processing",
    updated_at: sending.at,
    error: "",
  } as Job;
  expect(reconcileUpload(active, [job])).toBe(active);
  const complete = reconcileUpload(active, [
    { ...job, status: "needs_review" },
  ]);
  expect(complete.state).toBe("complete");
  expect(complete.events.at(-1)?.done).toBe(true);
  const stale = mergeUploadProgress(complete, [
    snapshot([received, { ...received, percent: 90 }]),
  ]);
  expect(stale.state).toBe("complete");
  expect(stale.events.at(-1)?.done).toBe(true);
  const final = mergeUploadProgress(stale, [
    snapshot([received, { ...received, percent: 90 }, done]),
  ]);
  expect(final.events).toEqual([
    sending,
    received,
    { ...received, percent: 90 },
    done,
  ]);
  const failed = reconcileUpload(active, [
    { ...job, status: "failed", error: "OCR failed" },
  ]);
  expect(failed.state).toBe("failed");
  expect(failed.error).toBe("OCR failed");
});
