import {
  Archive,
  Check,
  ChevronRight,
  CircleAlert,
  Clock3,
  FileCheck2,
  FileSearch,
  Files,
  FolderArchive,
  FolderOpen,
  Inbox,
  LoaderCircle,
  RefreshCw,
  RotateCcw,
  ScanLine,
  Settings2,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { api } from "./api";
import { documentTypes, recipientScopes, replaceFilenameDocumentType, savedRecipientChoice, learnedAlias } from "./review";
import { FolderPicker } from "./FolderPicker";
import { TextPreview } from "./TextPreview";
import { SetupGuide } from "./SetupGuide";
import { RecipientSettings } from "./RecipientSettings";
import type { Dashboard, Job, OCRPage, ProgressEvent, RecipientProfile, TextLayout } from "./types";
import { formatFileSize, isProcessingStatus, supportedDocuments } from "./upload";
import "./styles.css";

type View = "overview" | "processing" | "review" | "documents" | "settings";
type PreviewMode = "pdf" | "overlay" | "text";
type UploadTaskState = "uploading" | "processing" | "complete" | "failed";

type UploadTask = {
  id: string;
  jobID: string;
  runID: string;
  filename: string;
  size: number;
  createdAt: string;
  state: UploadTaskState;
  events: ProgressEvent[];
  error: string;
};

const emptyDashboard: Dashboard = {
  database_backup: { directory: "", latest: "", count: 0, available: false },
  settings: {
    inbox: "",
    archive_root: "",
    archive_exists: false,
    archive_error: "",
    setup_required: true,
    setup_step: "documents",
    scanner_share_checked: false,
    scanner_share_ready: false,
    model: "",
    model_provider: "ollama",
    model_enabled: true,
  },
  stats: { review: 0, archived: 0, failed: 0, total: 0 },
  folders: [],
  review_jobs: [],
  recent_jobs: [],
  all_jobs: [],
};

function App() {
  const [dashboard, setDashboard] = useState(emptyDashboard);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [view, setView] = useState<View>("overview");
  const [selectedID, setSelectedID] = useState("");
  const [selectedUploadID, setSelectedUploadID] = useState("");
  const [uploads, setUploads] = useState<UploadTask[]>([]);
  const streamsRef = useRef(new Map<string, EventSource>());

  const load = useCallback(async () => {
    try {
      setError("");
      const next = await api.dashboard();
      setDashboard(next);
      if (next.settings.setup_required) setView("settings");
      setSelectedID((current) => current || next.review_jobs[0]?.id || next.recent_jobs[0]?.id || "");
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => void load(), [load]);

  useEffect(() => () => {
    streamsRef.current.forEach((stream) => stream.close());
    streamsRef.current.clear();
  }, []);

  const processingJobs = useMemo(
    () => dashboard.all_jobs.filter((job) => isProcessingStatus(job.status)),
    [dashboard.all_jobs],
  );
  const activeUploadCount = uploads.filter((item) => item.state === "uploading" || item.state === "processing").length;
  const representedJobs = new Set(uploads.map((item) => item.jobID).filter(Boolean));
  const processingCount = activeUploadCount + processingJobs.filter((job) => !representedJobs.has(job.id)).length;

  useEffect(() => {
    if (!processingCount) return;
    const timer = window.setInterval(() => void load(), 2500);
    return () => window.clearInterval(timer);
  }, [load, processingCount]);

  const updateUpload = useCallback((id: string, update: (item: UploadTask) => UploadTask) => {
    setUploads((current) => current.map((item) => item.id === id ? update(item) : item));
  }, []);

  const appendUploadEvent = useCallback((id: string, event: ProgressEvent) => {
    updateUpload(id, (item) => ({ ...item, events: [...item.events, event] }));
  }, [updateUpload]);

  const startUpload = useCallback(async (file: File) => {
    const id = globalThis.crypto?.randomUUID?.() ?? `upload-${Date.now()}-${Math.random()}`;
    const initialEvent: ProgressEvent = { at: new Date().toISOString(), level: "info", phase: "upload", step: "sending", message: "Uploading document.", percent: 3 };
    setUploads((current) => [{
      id, jobID: "", runID: "", filename: file.name, size: file.size,
      createdAt: initialEvent.at, state: "uploading", events: [initialEvent], error: "",
    }, ...current]);
    setSelectedUploadID(id);

    try {
      const { run_id, job_id } = await api.upload(file);
      updateUpload(id, (item) => ({ ...item, runID: run_id, jobID: job_id, state: "processing" }));
      await load();

      const stream = new EventSource(`/api/uploads/${encodeURIComponent(run_id)}/events`);
      streamsRef.current.set(id, stream);
      stream.addEventListener("progress", (message) => appendUploadEvent(id, JSON.parse((message as MessageEvent).data) as ProgressEvent));
      stream.addEventListener("done", (message) => {
        const event = JSON.parse((message as MessageEvent).data) as ProgressEvent;
        appendUploadEvent(id, event);
        updateUpload(id, (item) => ({ ...item, state: "complete" }));
        stream.close();
        streamsRef.current.delete(id);
        void load();
      });
      stream.addEventListener("failed", (message) => {
        const event = JSON.parse((message as MessageEvent).data) as ProgressEvent;
        appendUploadEvent(id, event);
        updateUpload(id, (item) => ({ ...item, state: "failed", error: event.message }));
        stream.close();
        streamsRef.current.delete(id);
        void load();
      });
    } catch (reason) {
      updateUpload(id, (item) => ({ ...item, state: "failed", error: errorMessage(reason) }));
    }
  }, [appendUploadEvent, load, updateUpload]);

  const enqueueFiles = useCallback((files: File[]) => {
    files.forEach((file) => void startUpload(file));
    setView("processing");
  }, [startUpload]);

  const selected = useMemo(
    () => dashboard.all_jobs.find((job) => job.id === selectedID) ?? null,
    [dashboard.all_jobs, selectedID],
  );

  const openJob = (job: Job) => {
    setSelectedID(job.id);
    setView(job.status === "needs_review" || job.status === "failed" ? "review" : "documents");
  };

  return (
    <div className={`app-shell${dashboard.settings.setup_required ? " onboarding-shell" : view === "overview" ? "" : " workspace-shell"}`}>
      <header className="masthead">
        <div className="masthead-inner">
          <div className="brand-row">
            <div className="brand">
              <div className="brand-mark">P</div>
              <div>
                <strong>Paperless</strong>
                <span>Document archive</span>
              </div>
            </div>
            <nav className="nav-tabs" aria-label="Main navigation">
              {!dashboard.settings.setup_required && <>
              <NavButton active={view === "overview"} icon={<ScanLine />} label="Overview" onClick={() => setView("overview")} />
              <NavButton active={view === "processing"} icon={<LoaderCircle className={processingCount ? "spin" : ""} />} label="Processing" count={processingCount} onClick={() => setView("processing")} />
              <NavButton active={view === "review"} icon={<FileSearch />} label="Review" count={dashboard.stats.review} onClick={() => setView("review")} />
              <NavButton active={view === "documents"} icon={<Files />} label="Documents" onClick={() => setView("documents")} />
              </>}
              <NavButton active={view === "settings"} icon={<Settings2 />} label="Setup" onClick={() => setView("settings")} />
            </nav>
          </div>

          <div className="header-grid">
            <div className="welcome-block">
              <span className="eyebrow">Local document flow</span>
              <h1>{dashboard.settings.setup_required ? "Choose where your documents belong" : dashboard.stats.review ? `${dashboard.stats.review} document${dashboard.stats.review === 1 ? "" : "s"} waiting` : "Your archive is up to date"}</h1>
              <div className="path-line"><Inbox /> <span>{dashboard.settings.inbox || "Loading inbox..."}</span></div>
            </div>
            {!dashboard.settings.setup_required && <UploadPanel activeCount={activeUploadCount} onFiles={enqueueFiles} onOpenQueue={() => setView("processing")} />}
          </div>
        </div>
      </header>

      <main className="main">
        {error && <div className="error-banner"><CircleAlert /> <span>{error}</span><button className="icon-button" onClick={() => setError("")} title="Dismiss"><X /></button></div>}
        {loading ? <LoadingState /> : null}
        {!loading && dashboard.settings.setup_required && <SetupGuide dashboard={dashboard} onRefresh={load} onComplete={() => setView("overview")} />}
        {!loading && !dashboard.settings.setup_required && view === "overview" && <Overview dashboard={dashboard} onOpenJob={openJob} onOpenReview={() => setView("review")} />}
        {!loading && !dashboard.settings.setup_required && view === "processing" && (
          <ProcessingWorkspace
            uploads={uploads}
            jobs={processingJobs}
            allJobs={dashboard.all_jobs}
            selectedID={selectedUploadID}
            onSelect={setSelectedUploadID}
            onOpenJob={openJob}
          />
        )}
        {!loading && !dashboard.settings.setup_required && view === "review" && (
          <ReviewWorkspace
            jobs={dashboard.review_jobs}
            folders={dashboard.folders}
            profiles={dashboard.recipient_profiles || []}
            paperRecommendations={dashboard.paper_recommendations || {}}
            archiveRoot={dashboard.settings.archive_root}
            selected={selected?.status === "needs_review" || selected?.status === "failed" ? selected : dashboard.review_jobs[0] ?? null}
            onSelect={setSelectedID}
            onChanged={load}
          />
        )}
        {!loading && !dashboard.settings.setup_required && view === "documents" && <Documents jobs={dashboard.all_jobs} selected={selected} onSelect={setSelectedID} />}
        {!loading && !dashboard.settings.setup_required && view === "settings" && <Setup dashboard={dashboard} onRefresh={load} />}
      </main>
    </div>
  );
}

function NavButton({ active, icon, label, count, onClick }: { active: boolean; icon: React.ReactNode; label: string; count?: number; onClick: () => void }) {
  return <button aria-label={count ? `${label} ${count}` : label} aria-current={active ? "page" : undefined} className={active ? "nav-button active" : "nav-button"} onClick={onClick}>{icon}<span>{label}</span>{count ? <b>{count}</b> : null}</button>;
}

function UploadPanel({ activeCount, onFiles, onOpenQueue }: { activeCount: number; onFiles: (files: File[]) => void; onOpenQueue: () => void }) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [failed, setFailed] = useState("");
  const [message, setMessage] = useState("Select one or several documents.");
  const [dragDepth, setDragDepth] = useState(0);
  const dragging = dragDepth > 0;

  const queueFiles = (candidates: ArrayLike<File>) => {
    const supported = supportedDocuments(candidates);
    if (!supported.length) {
      setFailed("Choose a PDF, PNG, or JPEG document.");
      return;
    }
    setFailed("");
    setMessage(`${supported.length} document${supported.length === 1 ? "" : "s"} added to the processing queue.`);
    onFiles(supported);
  };

  const openFilePicker = () => inputRef.current?.click();

  const handleDragEnter = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (!Array.from(event.dataTransfer.types).includes("Files")) return;
    setDragDepth((depth) => depth + 1);
  };

  const handleDragOver = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  };

  const handleDragLeave = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragDepth((depth) => Math.max(0, depth - 1));
  };

  const handleDrop = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragDepth(0);
    queueFiles(event.dataTransfer.files);
  };

  return (
    <section className="upload-panel" aria-label="Upload document">
      <div className="upload-top">
        <div className="round-icon"><Upload /></div>
        <div><strong>Add documents</strong><span>PDF, PNG or JPEG</span></div>
        <span className={failed ? "state-badge bad" : activeCount ? "state-badge busy" : "state-badge"}>{failed ? "Check files" : activeCount ? `${activeCount} processing` : "Ready"}</span>
      </div>
      <input
        ref={inputRef}
        hidden
        type="file"
        multiple
        accept="application/pdf,image/png,image/jpeg"
        onChange={(event) => {
          if (event.target.files) queueFiles(event.target.files);
          event.currentTarget.value = "";
        }}
      />
      <div
        className={`drop-zone${dragging ? " is-dragging" : ""}`}
        role="button"
        tabIndex={0}
        aria-label="Drop documents here or choose files"
        onClick={openFilePicker}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            openFilePicker();
          }
        }}
        onDragEnter={handleDragEnter}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        <span className="drop-zone-icon" aria-hidden="true"><Upload /></span>
        <span className="drop-zone-copy">
          <strong>{dragging ? "Drop to add documents" : "Drop documents here"}</strong>
          <span>They start processing immediately</span>
        </span>
        <span className="drop-zone-action"><FileSearch /> Choose files</span>
      </div>
      <div className="upload-actions">
        <span className={failed ? "upload-state error-text" : "upload-state"}>{failed || message}</span>
        <button className="primary-button" disabled={!activeCount} onClick={onOpenQueue}><LoaderCircle className={activeCount ? "spin" : ""} /> View queue</button>
      </div>
    </section>
  );
}

function ProcessingWorkspace({ uploads, jobs, allJobs, selectedID, onSelect, onOpenJob }: {
  uploads: UploadTask[];
  jobs: Job[];
  allJobs: Job[];
  selectedID: string;
  onSelect: (id: string) => void;
  onOpenJob: (job: Job) => void;
}) {
  const represented = new Set(uploads.map((item) => item.jobID).filter(Boolean));
  const entries = [
    ...uploads.map((task) => ({ id: task.id, task, job: allJobs.find((job) => job.id === task.jobID) })),
    ...jobs.filter((job) => !represented.has(job.id)).map((job) => ({ id: job.id, task: undefined, job })),
  ];
  const selected = entries.find((entry) => entry.id === selectedID) ?? entries[0];
  const active = entries.filter((entry) => entry.task ? entry.task.state === "uploading" || entry.task.state === "processing" : true).length;

  return (
    <section className="workspace-grid processing-workspace">
      <aside className="queue-column">
        <SectionHead title="Processing queue" meta={`${active} active`} />
        {entries.length ? <div className="job-list">{entries.map((entry) => {
          const state = processingEntryState(entry.task, entry.job);
          const percent = processingEntryPercent(entry.task, entry.job);
          return <button key={entry.id} className={`job-row queue-row${selected?.id === entry.id ? " selected" : ""}`} onClick={() => onSelect(entry.id)}>
            <div className={`doc-icon queue-icon ${state}`}>
              {state === "failed" ? <CircleAlert /> : state === "complete" ? <Check /> : <LoaderCircle className="spin" />}
            </div>
            <div className="job-copy">
              <strong>{entry.task?.filename || entry.job?.source_filename}</strong>
              <span>{processingEntryLabel(entry.task, entry.job)}</span>
              <div className="queue-row-progress"><span style={{ width: `${percent}%` }} /></div>
            </div>
            <div className="job-side"><b>{percent}%</b><ChevronRight /></div>
          </button>;
        })}</div> : <EmptyState icon={<FileCheck2 />} title="No uploads in this session" />}
      </aside>
      <div className="detail-column">
        {selected
          ? <ProcessingDetail task={selected.task} job={selected.job} onOpenJob={onOpenJob} />
          : <EmptyState icon={<LoaderCircle />} title="Select an upload" />}
      </div>
    </section>
  );
}

function ProcessingDetail({ task, job, onOpenJob }: { task?: UploadTask; job?: Job; onOpenJob: (job: Job) => void }) {
  const state = processingEntryState(task, job);
  const percent = processingEntryPercent(task, job);
  const latest = task?.events.at(-1);
  const canOpen = job && !isProcessingStatus(job.status);
  return <article className="processing-detail">
    <header className="processing-detail-head">
      <div className={`round-icon queue-icon ${state}`}>
        {state === "failed" ? <CircleAlert /> : state === "complete" ? <Check /> : <LoaderCircle className="spin" />}
      </div>
      <div><span className="eyebrow">{processingEntryLabel(task, job)}</span><h2>{task?.filename || job?.source_filename}</h2><p>{latest?.message || processingStatusMessage(job?.status)}</p></div>
      <strong className="queue-percent">{percent}%</strong>
    </header>
    <div className="detail-progress"><span style={{ width: `${percent}%` }} /></div>
    {(task?.error || job?.error) && <div className="inline-error"><CircleAlert /> {task?.error || job?.error}</div>}
    <div className="fact-row queue-facts">
      <Fact label="Status" value={processingEntryLabel(task, job)} />
      <Fact label="Size" value={task ? formatFileSize(task.size) : "—"} />
      <Fact label="Queued" value={formatDate(task?.createdAt || job?.scan_timestamp || "")} />
      <Fact label="Current stage" value={displayName(latest?.phase || job?.status || "upload")} />
    </div>
    <section className="processing-events">
      <div className="route-head"><div><span className="eyebrow">Activity</span><h3>Processing status</h3></div><Clock3 /></div>
      {task?.events.length ? <div className="queue-timeline">{task.events.map((event, index) => <div key={`${event.at}-${index}`} className={event.done ? "done" : ""}>
        <span>{event.percent}%</span><div><strong>{displayName(event.phase)}</strong><p>{event.message}</p></div>
      </div>)}</div> : <p className="processing-note">This document was already processing when the page was opened. Its current database status will refresh automatically.</p>}
      {canOpen && <button className="primary-button" onClick={() => onOpenJob(job)}><FileSearch /> Open document</button>}
    </section>
  </article>;
}

function processingEntryState(task?: UploadTask, job?: Job): UploadTaskState {
  if (task) return task.state;
  if (job?.status === "failed") return "failed";
  return job && isProcessingStatus(job.status) ? "processing" : "complete";
}

function processingEntryLabel(task?: UploadTask, job?: Job): string {
  if (task?.state === "uploading") return "Uploading";
  if (task?.state === "failed" || job?.status === "failed") return "Failed";
  if (task?.state === "complete") return job?.status === "needs_review" ? "Ready for review" : "Complete";
  return displayStatus(job?.status || "queued");
}

function processingEntryPercent(task?: UploadTask, job?: Job): number {
  const eventPercent = task?.events.at(-1)?.percent;
  if (eventPercent !== undefined) return eventPercent;
  const statusPercent: Record<string, number> = { received: 6, copying_raw: 10, processing: 35, ocr_complete: 86, classified: 94 };
  return statusPercent[job?.status || ""] ?? (job && !isProcessingStatus(job.status) ? 100 : 0);
}

function processingStatusMessage(status?: string): string {
  const messages: Record<string, string> = {
    received: "Waiting for its turn in the processing queue.",
    copying_raw: "Saving the original document.",
    processing: "Reading and preparing the document.",
    ocr_complete: "Text extraction is complete.",
    classified: "Finishing document classification.",
  };
  return messages[status || ""] || "Waiting for processing updates.";
}

function Overview({ dashboard, onOpenJob, onOpenReview }: { dashboard: Dashboard; onOpenJob: (job: Job) => void; onOpenReview: () => void }) {
  return (
    <>
      <section className="stats-grid">
        <Stat icon={<Clock3 />} value={dashboard.stats.review} label="Waiting for review" accent onClick={onOpenReview} />
        <Stat icon={<FileCheck2 />} value={dashboard.stats.archived} label="Archived" />
        <Stat icon={<Files />} value={dashboard.stats.total} label="All documents" />
        <Stat icon={<FolderArchive />} value={dashboard.folders.length} label="Known folders" />
      </section>
      <section className="overview-grid">
        <div>
          <SectionHead title="Review queue" meta={`${dashboard.review_jobs.length} waiting`} />
          <JobList jobs={dashboard.review_jobs.slice(0, 4)} empty="Nothing needs review." onSelect={onOpenJob} />
        </div>
        <div>
          <SectionHead title="Recent scans" meta={`${dashboard.recent_jobs.length} latest`} />
          <JobList jobs={dashboard.recent_jobs.slice(0, 5)} empty="No documents scanned yet." onSelect={onOpenJob} compact />
        </div>
      </section>
      <ArchiveStrip dashboard={dashboard} />
    </>
  );
}

function Stat({ icon, value, label, accent, onClick }: { icon: React.ReactNode; value: number; label: string; accent?: boolean; onClick?: () => void }) {
  return <button className={accent ? "stat-tile accent" : "stat-tile"} onClick={onClick} disabled={!onClick}><span>{icon}</span><strong>{value}</strong><small>{label}</small></button>;
}

function ArchiveStrip({ dashboard }: { dashboard: Dashboard }) {
  return <section className="archive-strip"><div className="round-icon dark"><Archive /></div><div><span>Archive root</span><strong>{dashboard.settings.archive_root}</strong></div><div className={dashboard.settings.archive_exists ? "archive-state ok" : "archive-state bad"}>{dashboard.settings.archive_exists ? <Check /> : <CircleAlert />}{dashboard.settings.archive_exists ? "Connected" : "Unavailable"}</div><div className="folder-sample">{dashboard.folders.slice(0, 5).map((folder) => <span key={folder}>{folder}</span>)}</div></section>;
}

function ReviewWorkspace({ jobs, folders, profiles, paperRecommendations, archiveRoot, selected, onSelect, onChanged }: { jobs: Job[]; folders: string[]; profiles: RecipientProfile[]; paperRecommendations: Record<string, string>; archiveRoot: string; selected: Job | null; onSelect: (id: string) => void; onChanged: () => Promise<void> }) {
  return (
    <section className="workspace-grid">
      <aside className="queue-column">
        <SectionHead title="Review queue" meta={`${jobs.length} waiting`} />
        <JobList jobs={jobs} empty="Nothing needs review." selectedID={selected?.id} onSelect={(job) => onSelect(job.id)} />
      </aside>
      <div className="detail-column">
        {selected ? <JobDetail job={selected} folders={folders} profiles={profiles} paperRecommendations={paperRecommendations} archiveRoot={archiveRoot} onChanged={onChanged} review /> : <EmptyState icon={<FileCheck2 />} title="Review complete" />}
      </div>
    </section>
  );
}

function Documents({ jobs, selected, onSelect }: { jobs: Job[]; selected: Job | null; onSelect: (id: string) => void }) {
  const [query, setQuery] = useState("");
  const visible = jobs.filter((job) => `${job.source_filename} ${job.summary} ${job.classification.sender} ${job.classification.recipient}`.toLowerCase().includes(query.toLowerCase()));
  return <section className="documents-workspace"><div className="documents-head"><SectionHead title="All documents" meta={`${visible.length} shown`} /><label className="search-field"><FileSearch /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search documents" /></label></div><div className="workspace-grid"><div className="queue-column"><JobList jobs={visible} empty="No matching documents." selectedID={selected?.id} onSelect={(job) => onSelect(job.id)} compact /></div><div className="detail-column">{selected ? <JobDetail job={selected} folders={[]} archiveRoot="" onChanged={async () => {}} /> : <EmptyState icon={<Files />} title="Select a document" />}</div></div></section>;
}

function JobList({ jobs, empty, onSelect, selectedID, compact }: { jobs: Job[]; empty: string; onSelect: (job: Job) => void; selectedID?: string; compact?: boolean }) {
  if (!jobs.length) return <div className="empty-list">{empty}</div>;
  return <div className="job-list">{jobs.map((job) => <button key={job.id} className={`${compact ? "job-row compact" : "job-row"}${selectedID === job.id ? " selected" : ""}`} onClick={() => onSelect(job)}><div className="doc-icon"><FileSearch /></div><div className="job-copy"><strong>{job.classification.summary || job.summary || job.source_filename}</strong><span>{displayName(job.classification.sender) || job.source_filename}</span><small>{formatDate(job.updated_at)} · {displayStatus(job.status)}</small></div><div className="job-side"><span className={`status-dot ${job.status}`} />{job.confidence ? <b>{Math.round(job.confidence * 100)}%</b> : null}<ChevronRight /></div></button>)}</div>;
}

function JobDetail({ job, folders, profiles = [], paperRecommendations = {}, archiveRoot, onChanged, review }: { job: Job; folders: string[]; profiles?: RecipientProfile[]; paperRecommendations?: Record<string, string>; archiveRoot: string; onChanged: () => Promise<void>; review?: boolean }) {
  const classification = job.classification;
  const [folder, setFolder] = useState(classification.suggested_folder || "");
  const [recipient, setRecipient] = useState(classification.recipient || "");
  const [recipientScope, setRecipientScope] = useState(classification.recipient_scope || "unknown");
  const [filename, setFilename] = useState(classification.suggested_filename || job.source_filename);
  const [documentType, setDocumentType] = useState(classification.document_type || "unknown");
  const [recipientChoice, setRecipientChoice] = useState(() => savedRecipientChoice(classification, profiles));
  const selectedRecipient = profiles.find((p) => String(p.id) === recipientChoice);
  const detectedRecipient = classification.detected_recipient || classification.recipient || "";
  const alias = learnedAlias(detectedRecipient, selectedRecipient);
  const paper = paperRecommendations[documentType] || (documentType === classification.document_type ? classification.physical_original_action : "review") || "review";
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const suggestedFolder = classification.suggested_folder || "";
    setFolder(suggestedFolder);
    setRecipient(classification.recipient || "");
    setRecipientScope(classification.recipient_scope || "unknown");
    setFilename(classification.suggested_filename || job.source_filename);
    setDocumentType(classification.document_type || "unknown");
    setRecipientChoice(savedRecipientChoice(classification, profiles));
    setError("");
    // Keep edits intact while other documents trigger dashboard refreshes.
  }, [job.id]);

  const approve = async () => {
    setSaving(true); setError("");
    try { await api.approve(job.id, { folder, filename, document_type: documentType, ...(recipientChoice === "new" ? { recipient, recipient_scope: recipientScope } : { recipient_profile_id: Number(recipientChoice) }) }); await onChanged(); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  };
  const reject = async () => {
    if (!window.confirm("Reject and permanently delete this document and its stored copies?")) return;
    setSaving(true); setError("");
    try { await api.reject(job.id); await onChanged(); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  };

  return <article className="document-detail">
    <header className="detail-head"><div><span className="eyebrow">{displayStatus(job.status)} · {displayInputKind(job.input_kind)}</span><h2>{classification.summary || job.summary || job.source_filename}</h2><p>{job.source_filename}</p></div><div className="confidence-ring"><strong>{Math.round(job.confidence * 100)}%</strong><span>confidence</span></div></header>
    {job.error && <div className="inline-error"><CircleAlert /> {job.error}</div>}
    <div className="fact-row"><Fact label="Sender" value={displayName(classification.sender) || "Unknown"} /><Fact label={recipientScopes.find((scope) => scope.value === classification.recipient_scope)?.label || "Recipient"} value={displayName(classification.recipient) || "Not detected"} /><Fact label="Type" value={displayName(classification.document_type) || "Unknown"} /><Fact label="Pages" value={String(job.page_count || 0)} /></div>
    {classification.recipient_address && <p className="learning-note">Detected recipient address: {classification.recipient_address.replace(/\n/g, ", ")}</p>}
    {review && job.status === "needs_review" && <section className="routing-form">
      <div className="route-head"><div><span className="eyebrow">Destination</span><h3>{folder ? "Choose the final folder" : "No archive folder matched"}</h3></div><FolderArchive /></div>
      <div className="recipient-review">
        <label className="filename-field"><span>Recipient</span><select value={recipientChoice} onChange={(event) => {
          const choice = event.target.value;
          setRecipientChoice(choice);
          const profile = profiles.find((p) => String(p.id) === choice);
          if (profile?.folder_prefix && folder !== profile.folder_prefix && !folder.startsWith(`${profile.folder_prefix}/`)) setFolder(profile.folder_prefix);
        }}><option value="" disabled>Choose a recipient…</option>{profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name} · {recipientScopes.find((scope) => scope.value === profile.scope)?.label}</option>)}<option value="new">＋ New recipient…</option></select></label>
        {recipientChoice === "new" && <>
          <label><span>Recipient name</span><input maxLength={200} value={recipient} onChange={(event) => setRecipient(event.target.value)} placeholder="Name on the document" /></label>
          <label><span>Addressed to</span><select value={recipientScope} onChange={(event) => setRecipientScope(event.target.value)}>{recipientScopes.map(({ value, label }) => <option key={value} value={value}>{label}</option>)}</select></label>
          <p>This recipient will be saved in Settings when you approve.</p>
        </>}
        <p>{classification.recipient_evidence ? `Detected from: “${classification.recipient_evidence}”` : "Check who the document is addressed to, including their personal or business capacity."}</p>
        {alias && <p className="alias-learning">On approval, “{alias}” will be saved as an alias of {selectedRecipient!.name}.</p>}

      </div>
      <FolderPicker key={job.id} folders={folders} value={folder} onChange={setFolder} />
      <label><span>Document type</span><select value={documentType} onChange={(event) => { const next = event.target.value; setFilename((current) => replaceFilenameDocumentType(current, documentType, next)); setDocumentType(next); }}>{documentTypes.map((value) => <option key={value} value={value}>{displayName(value)}</option>)}</select></label>
      <div className={`paper-recommendation ${paper}`} role="status"><FileCheck2 /><div><span>Paper original · Recommendation</span><strong>{paper === "keep_original" ? "Keep the original" : paper === "discard_candidate" ? "May be discarded after checking the scan" : "Check whether the original is needed"}</strong><p>Based on the document type and your retention policy.</p></div></div>
      <label className="filename-field"><span>Filename</span><input value={filename} onChange={(event) => setFilename(event.target.value)} /></label>
      <div className="resolved-path"><Archive /> <span>{archiveRoot}{folder ? `/${folder}` : ""}/{filename}</span></div>
      {error && <div className="form-error">{error}</div>}
      <p className="learning-note">Approval saves your recipient and folder choices locally to guide similar documents.</p>
      <div className="review-actions"><button className="primary-button" disabled={!folder.trim() || !filename.trim() || !documentType || !recipientChoice || (recipientChoice === "new" && !recipient.trim()) || (recipientChoice !== "new" && !selectedRecipient) || saving} onClick={approve}>{saving ? <LoaderCircle className="spin" /> : <Check />} Approve & archive</button><button className="danger-button" disabled={saving} onClick={reject}><Trash2 /> Reject & delete</button></div>
    </section>}
    {review && job.status === "failed" && <div className="review-actions"><button className="primary-button" onClick={async () => { await api.retry(job.id); await onChanged(); }}><RotateCcw /> Retry from inbox</button></div>}
    <DocumentPreview job={job} />
  </article>;
}

function DocumentPreview({ job }: { job: Job }) {
  const [mode, setMode] = useState<PreviewMode>("pdf");
  const [pages, setPages] = useState<OCRPage[]>([]);
  const [text, setText] = useState("");
  const [layout, setLayout] = useState<TextLayout | null>(null);
  const [opacity, setOpacity] = useState(45);
  const [error, setError] = useState("");

  useEffect(() => {
    setPages([]); setText(""); setLayout(null); setError(""); setMode("pdf");
  }, [job.id]);
  useEffect(() => {
    let active = true;
    setError("");
    if (mode === "overlay" && !pages.length) api.pages(job.id).then((result) => { if (active) setPages(result.pages); }).catch((reason) => { if (active) setError(errorMessage(reason)); });
    if (mode === "text" && !layout) Promise.all([api.layout(job.id), api.text(job.id)]).then(([result, raw]) => { if (active) { setLayout(result); setText(raw); } }).catch((reason) => { if (active) setError(errorMessage(reason)); });
    return () => { active = false; };
  }, [job.id, mode, pages.length, layout]);

  return <section className="preview-section"><div className="preview-toolbar"><div className="segmented" role="tablist"><button className={mode === "pdf" ? "active" : ""} onClick={() => setMode("pdf")}>{job.text_source === "embedded" ? "Original PDF" : "OCR PDF"}</button>{job.text_source === "ocr" && <button className={mode === "overlay" ? "active" : ""} onClick={() => setMode("overlay")}>Overlay</button>}<button className={mode === "text" ? "active" : ""} onClick={() => setMode("text")}>Text</button></div>{mode === "overlay" && <label className="opacity-control"><span>Opacity</span><input type="range" min="10" max="100" value={opacity} onChange={(event) => setOpacity(Number(event.target.value))} /></label>}</div>{error && <div className="inline-error">{error}</div>}{mode === "pdf" && <iframe title="Searchable PDF" className="pdf-frame" src={job.urls.current} />}{mode === "text" && (layout ? <TextPreview key={job.id} layout={layout} raw={text} /> : !error && <LoadingState />)}{mode === "overlay" && <div className="overlay-stack">{pages.length ? pages.map((page) => <div className="ocr-page" key={page.page} style={{ aspectRatio: `${page.width} / ${page.height}` }}><img src={page.image_url} alt={`Cleaned page ${page.page}`} /> <div className="box-layer">{page.boxes.map((box, index) => <span key={index} title={box.text} style={{ left: `${box.left / page.width * 100}%`, top: `${box.top / page.height * 100}%`, width: `${box.width / page.width * 100}%`, height: `${box.height / page.height * 100}%`, opacity: opacity / 100 }} />)}</div></div>) : <LoadingState />}</div>}</section>;
}

function displayInputKind(kind: Job["input_kind"]) {
  if (kind === "digital_pdf") return "Digital PDF";
  if (kind === "mixed_pdf") return "Mixed PDF";
  return "Scan";
}

function Setup({ dashboard, onRefresh }: { dashboard: Dashboard; onRefresh: () => Promise<void> }) {
  const [refreshing, setRefreshing] = useState(false);
  const [choosing, setChoosing] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [setupError, setSetupError] = useState("");
  const [backingUp, setBackingUp] = useState(false);
  const [modelProvider, setModelProvider] = useState(dashboard.settings.model_provider);
  const [savingModel, setSavingModel] = useState(false);
  const [installingModel, setInstallingModel] = useState(false);
  const [installProgress, setInstallProgress] = useState("");
  const saveModel = async (install = false) => {
    setSavingModel(true); setSetupError("");
    try {
      if (install) {
        setInstallingModel(true);
        setInstallProgress("Preparing Bonsai installation…");
        await api.installBonsai(setInstallProgress);
      }
      await api.setModelProvider(modelProvider);
      setRestarting(true);
      window.setTimeout(() => window.location.reload(), 1500);
    } catch (reason) {
      setSetupError(errorMessage(reason)); setSavingModel(false);
    } finally {
      setInstallingModel(false);
    }
  };
  const chooseDirectory = async () => {
    setChoosing(true); setSetupError("");
    try {
      await api.chooseDocumentsDirectory();
      setRestarting(true);
      window.setTimeout(() => window.location.reload(), 1500);
    } catch (reason) {
      setSetupError(errorMessage(reason)); setChoosing(false);
    }
  };
  const openSharing = async () => {
    setSetupError("");
    try { await api.openSharingSettings(); } catch (reason) { setSetupError(errorMessage(reason)); }
  };
  const backupNow = async () => {
    setBackingUp(true); setSetupError("");
    try { await api.backupDatabase(); await onRefresh(); } catch (reason) { setSetupError(errorMessage(reason)); }
    finally { setBackingUp(false); }
  };
  return <section className="setup-layout">
    <div className="setup-title"><span className="eyebrow">{dashboard.settings.setup_required ? "Welcome to Paperless" : "Setup"}</span><h2>Storage, scanner & model</h2></div>
    <section className={dashboard.settings.setup_required ? "setup-wizard required" : "setup-wizard"}>
      <div className="setup-step-number">1</div>
      <div className="setup-step-copy"><h3>Choose your base documents directory</h3><p>Choose an existing folder on this Mac. It can be an ordinary local folder or a locally available Dropbox, Google Drive, iCloud Drive, OneDrive, or mounted file-server folder.</p><p>Paperless files completed documents below this directory. Its live SQLite database remains in local Application Support so a sync client cannot corrupt it. Consistent database snapshots are stored in a hidden backup folder below the selected directory.</p>{dashboard.settings.archive_root && <code>{dashboard.settings.archive_root}</code>}{dashboard.database_backup?.directory && <p>{dashboard.database_backup.count ? `${dashboard.database_backup.count} database backup${dashboard.database_backup.count === 1 ? "" : "s"} · latest ${dashboard.database_backup.latest}` : "The first database backup will be created after startup."} <button className="text-button" disabled={backingUp} onClick={backupNow}>{backingUp ? "Backing up…" : "Back up now"}</button></p>}{dashboard.settings.archive_error && !dashboard.settings.setup_required && <div className="inline-error">{dashboard.settings.archive_error}</div>}</div>
      <button className="primary-button" disabled={choosing || savingModel || restarting} onClick={chooseDirectory}><FolderOpen /> {restarting ? "Restarting Paperless…" : choosing ? "Opening folder chooser…" : dashboard.settings.archive_root ? "Choose another folder" : "Choose documents folder"}</button>
    </section>
    <section className="setup-wizard">
      <div className="setup-step-number">2</div>
      <div className="setup-step-copy"><h3>Share the scanner inbox over SMB</h3><p>Your scanner writes new files to this dedicated inbox:</p><code>{dashboard.settings.inbox}</code><ol><li>Open macOS Sharing settings and turn on File Sharing.</li><li>Add the scanner inbox above as a shared folder.</li><li>Under Options, enable “Share files and folders using SMB” and enable the scanner’s macOS user.</li><li>On the scanner, enter this Mac’s hostname or IP address, the user credentials, and share name <strong>{dashboard.settings.inbox.split("/").filter(Boolean).at(-1) || "inbox"}</strong>.</li></ol><p>Use a dedicated macOS account limited to this inbox when your scanner supports authenticated SMB.</p></div>
      <button className="icon-text-button" onClick={openSharing}>Open Sharing settings</button>
    </section>
    <section className="setup-wizard">
      <div className="setup-step-number">3</div>
      <div className="setup-step-copy"><h3>Choose your local model</h3><p>Choose a model to process documents locally.</p><label className="model-choice"><span>Model provider</span><select value={modelProvider} disabled={savingModel || restarting} onChange={(event) => setModelProvider(event.target.value as "ollama" | "fm" | "bonsai")}><option value="ollama">Ollama · Qwen 3.5</option><option value="fm">Apple Foundation Models</option><option value="bonsai">Bonsai · PrismML</option></select></label><p>{modelProvider === "fm" ? "Uses Apple Intelligence through the fm command. No separate model download or server is needed. Documents that exceed its context limit require review." : modelProvider === "bonsai" ? "Install Bonsai 8B (1-bit) and its local server, or save an existing Bonsai server. Installation downloads about 1.16 GB of model weights plus the runtime and starts the server automatically at login. Keep this page open during installation." : "Uses your configured Ollama model. Ollama must be running with the model installed."}</p>{modelProvider === "bonsai" && <p><button className="icon-text-button" disabled={savingModel || restarting} onClick={() => saveModel(true)}>{installingModel ? "Installing Bonsai…" : "Install & use Bonsai 8B"}</button></p>}{installProgress && <p role="status" aria-live="polite">{installProgress}</p>}<p>Saving restarts Paperless. Wait for active uploads to finish first.</p></div>
      <button className="primary-button" disabled={savingModel || restarting || (modelProvider === dashboard.settings.model_provider && dashboard.settings.model_enabled)} onClick={() => saveModel()}>{restarting ? "Restarting Paperless…" : savingModel ? "Checking model…" : "Save model"}</button>
    </section>
    {setupError && <div className="form-error" role="alert">{setupError}</div>}
    <div className="setup-rows"><SetupRow icon={<Inbox />} title="Scanner inbox" value={dashboard.settings.inbox} state={dashboard.settings.scanner_share_ready ? "SMB ready" : "SMB setup needed"} ok={dashboard.settings.scanner_share_ready} /><SetupRow icon={<Archive />} title="Documents directory" value={dashboard.settings.archive_root || "Not selected"} state={dashboard.settings.archive_exists ? "Connected" : dashboard.settings.setup_required ? "Selection required" : "Unavailable"} ok={dashboard.settings.archive_exists} /><SetupRow icon={<FileSearch />} title="Local model" value={dashboard.settings.model_enabled ? dashboard.settings.model_provider === "fm" ? "Apple Foundation Models · system" : `${dashboard.settings.model_provider === "bonsai" ? "Bonsai" : "Ollama"} · ${dashboard.settings.model}` : "Local rules only"} state={dashboard.settings.model_enabled ? "Configured" : "Disabled"} ok={dashboard.settings.model_enabled} /></div>
    {!dashboard.settings.setup_required && <><RecipientSettings dashboard={dashboard} onRefresh={onRefresh} /><section className="folder-browser"><div><span className="eyebrow">Document folders</span><h3>{dashboard.folders.length} available destinations</h3></div><button className="icon-text-button" disabled={refreshing} onClick={async () => { setRefreshing(true); await api.refreshFolders(); await onRefresh(); setRefreshing(false); }}><RefreshCw className={refreshing ? "spin" : ""} /> Refresh</button><div className="folder-grid">{dashboard.folders.map((folder) => <span key={folder}><FolderArchive /> {folder}</span>)}</div></section></>}
  </section>;
}

function SetupRow({ icon, title, value, state, ok }: { icon: React.ReactNode; title: string; value: string; state: string; ok: boolean }) {
  return <div className="setup-row"><div className="round-icon">{icon}</div><div><strong>{title}</strong><span>{value}</span></div><b className={ok ? "setup-status ok" : "setup-status bad"}>{ok ? <Check /> : <CircleAlert />}{state}</b></div>;
}

function Fact({ label, value }: { label: string; value: string }) { return <div className="fact"><span>{label}</span><strong>{value}</strong></div>; }
function SectionHead({ title, meta }: { title: string; meta: string }) { return <div className="section-head"><h2>{title}</h2><span>{meta}</span></div>; }
function EmptyState({ icon, title }: { icon: React.ReactNode; title: string }) { return <div className="large-empty">{icon}<strong>{title}</strong></div>; }
function LoadingState() { return <div className="loading-state"><LoaderCircle className="spin" /><span>Loading</span></div>; }
function errorMessage(reason: unknown) { return reason instanceof Error ? reason.message : String(reason); }
function displayName(value: string) { return value?.split(/[-_]/).filter(Boolean).map((part) => part.charAt(0).toUpperCase() + part.slice(1)).join(" ") ?? ""; }
function displayStatus(value: string) { return displayName(value) || "Unknown"; }
function formatDate(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date); }

createRoot(document.getElementById("root")!).render(<App />);
