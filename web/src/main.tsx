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
import { documentTypes, replaceFilenameDocumentType } from "./review";
import type { Dashboard, Job, OCRPage, ProgressEvent } from "./types";
import { formatFileSize, isSupportedDocument, pickSupportedDocument } from "./upload";
import "./styles.css";

type View = "overview" | "review" | "documents" | "settings";
type PreviewMode = "pdf" | "overlay" | "text";

const emptyDashboard: Dashboard = {
  settings: {
    inbox: "",
    archive_root: "",
    archive_exists: false,
    scanner_share_checked: false,
    scanner_share_ready: false,
    model: "",
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

  const load = useCallback(async () => {
    try {
      setError("");
      const next = await api.dashboard();
      setDashboard(next);
      setSelectedID((current) => current || next.review_jobs[0]?.id || next.recent_jobs[0]?.id || "");
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => void load(), [load]);

  const selected = useMemo(
    () => dashboard.all_jobs.find((job) => job.id === selectedID) ?? null,
    [dashboard.all_jobs, selectedID],
  );

  const openJob = (job: Job) => {
    setSelectedID(job.id);
    setView(job.status === "needs_review" || job.status === "failed" ? "review" : "documents");
  };

  return (
    <div className="app-shell">
      <header className="masthead">
        <div className="masthead-inner">
          <div className="brand-row">
            <div className="brand">
              <div className="brand-mark">P</div>
              <div>
                <strong>Papyrus</strong>
                <span>Document archive</span>
              </div>
            </div>
            <nav className="nav-tabs" aria-label="Main navigation">
              <NavButton active={view === "overview"} icon={<ScanLine />} label="Overview" onClick={() => setView("overview")} />
              <NavButton active={view === "review"} icon={<FileSearch />} label="Review" count={dashboard.stats.review} onClick={() => setView("review")} />
              <NavButton active={view === "documents"} icon={<Files />} label="Documents" onClick={() => setView("documents")} />
              <NavButton active={view === "settings"} icon={<Settings2 />} label="Setup" onClick={() => setView("settings")} />
            </nav>
          </div>

          <div className="header-grid">
            <div className="welcome-block">
              <span className="eyebrow">Local document flow</span>
              <h1>{dashboard.stats.review ? `${dashboard.stats.review} document${dashboard.stats.review === 1 ? "" : "s"} waiting` : "Your archive is up to date"}</h1>
              <div className="path-line"><Inbox /> <span>{dashboard.settings.inbox || "Loading inbox..."}</span></div>
            </div>
            <UploadPanel onComplete={async (jobID) => { await load(); setSelectedID(jobID); setView("review"); }} />
          </div>
        </div>
      </header>

      <main className="main">
        {error && <div className="error-banner"><CircleAlert /> <span>{error}</span><button className="icon-button" onClick={() => setError("")} title="Dismiss"><X /></button></div>}
        {loading ? <LoadingState /> : null}
        {!loading && view === "overview" && <Overview dashboard={dashboard} onOpenJob={openJob} onOpenReview={() => setView("review")} />}
        {!loading && view === "review" && (
          <ReviewWorkspace
            jobs={dashboard.review_jobs}
            folders={dashboard.folders}
            archiveRoot={dashboard.settings.archive_root}
            selected={selected?.status === "needs_review" || selected?.status === "failed" ? selected : dashboard.review_jobs[0] ?? null}
            onSelect={setSelectedID}
            onChanged={load}
          />
        )}
        {!loading && view === "documents" && <Documents jobs={dashboard.all_jobs} selected={selected} onSelect={setSelectedID} />}
        {!loading && view === "settings" && <Setup dashboard={dashboard} onRefresh={load} />}
      </main>
    </div>
  );
}

function NavButton({ active, icon, label, count, onClick }: { active: boolean; icon: React.ReactNode; label: string; count?: number; onClick: () => void }) {
  return <button className={active ? "nav-button active" : "nav-button"} onClick={onClick}>{icon}<span>{label}</span>{count ? <b>{count}</b> : null}</button>;
}

function UploadPanel({ onComplete }: { onComplete: (jobID: string) => Promise<void> }) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [events, setEvents] = useState<ProgressEvent[]>([]);
  const [active, setActive] = useState(false);
  const [failed, setFailed] = useState("");
  const [dragDepth, setDragDepth] = useState(0);
  const latest = events.at(-1);
  const dragging = dragDepth > 0;

  const selectFile = (candidate: File | null) => {
    if (!candidate) return;
    if (!isSupportedDocument(candidate)) {
      setFile(null);
      setEvents([]);
      setFailed("Choose a PDF, PNG, or JPEG document.");
      return;
    }
    setFile(candidate);
    setEvents([]);
    setFailed("");
  };

  const openFilePicker = () => {
    if (!active) inputRef.current?.click();
  };

  const handleDragEnter = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (active || !Array.from(event.dataTransfer.types).includes("Files")) return;
    setDragDepth((depth) => depth + 1);
  };

  const handleDragOver = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (!active) event.dataTransfer.dropEffect = "copy";
  };

  const handleDragLeave = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragDepth((depth) => Math.max(0, depth - 1));
  };

  const handleDrop = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragDepth(0);
    if (active) return;

    const droppedFile = pickSupportedDocument(event.dataTransfer.files);
    if (!droppedFile) {
      setFile(null);
      setEvents([]);
      setFailed("Drop a PDF, PNG, or JPEG document.");
      return;
    }
    selectFile(droppedFile);
  };

  const analyze = async () => {
    if (!file || active) return;
    setActive(true);
    setFailed("");
    setEvents([{ at: new Date().toISOString(), level: "info", phase: "upload", step: "sending", message: "Uploading document.", percent: 3 }]);
    try {
      const { run_id, job_id } = await api.upload(file);
      const stream = new EventSource(`/api/uploads/${encodeURIComponent(run_id)}/events`);
      stream.onmessage = (message) => setEvents((current) => [...current, JSON.parse(message.data) as ProgressEvent]);
      stream.addEventListener("progress", (message) => setEvents((current) => [...current, JSON.parse((message as MessageEvent).data) as ProgressEvent]));
      stream.addEventListener("done", async (message) => {
        const event = JSON.parse((message as MessageEvent).data) as ProgressEvent;
        setEvents((current) => [...current, event]);
        stream.close();
        setActive(false);
        await onComplete(job_id);
      });
      stream.addEventListener("failed", (message) => {
        const event = JSON.parse((message as MessageEvent).data) as ProgressEvent;
        setEvents((current) => [...current, event]);
        setFailed(event.message);
        setActive(false);
        stream.close();
      });
      stream.onerror = () => {
        if (stream.readyState === EventSource.CLOSED) return;
      };
    } catch (reason) {
      setFailed(errorMessage(reason));
      setActive(false);
    }
  };

  return (
    <section className="upload-panel" aria-label="Upload document">
      <div className="upload-top">
        <div className="round-icon"><Upload /></div>
        <div><strong>Upload a document</strong><span>{file?.name || "PDF, PNG or JPEG"}</span></div>
        <span className={failed ? "state-badge bad" : active ? "state-badge busy" : "state-badge"}>{failed ? "Failed" : active ? `${latest?.percent ?? 0}%` : "Ready"}</span>
      </div>
      <input
        ref={inputRef}
        hidden
        type="file"
        accept="application/pdf,image/png,image/jpeg"
        onChange={(event) => {
          selectFile(event.target.files?.[0] ?? null);
          event.currentTarget.value = "";
        }}
      />
      <div
        className={`drop-zone${dragging ? " is-dragging" : ""}${file ? " has-file" : ""}${active ? " is-disabled" : ""}`}
        role="button"
        tabIndex={active ? -1 : 0}
        aria-disabled={active}
        aria-label={file ? `Selected document: ${file.name}. Choose another document.` : "Drop a document here or choose a file"}
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
          <strong>{dragging ? "Drop to add document" : file ? file.name : "Drop a document here"}</strong>
          <span>{file ? `${formatFileSize(file.size)} · Ready to analyze` : "PDF, PNG or JPEG"}</span>
        </span>
        <span className="drop-zone-action"><FileSearch /> Choose file</span>
      </div>
      <div className="upload-actions">
        <span className={failed ? "upload-state error-text" : "upload-state"}>{failed || latest?.message || (file ? "Ready for manual analysis." : "One document at a time.")}</span>
        <button className="primary-button" disabled={!file || active} onClick={analyze}>{active ? <LoaderCircle className="spin" /> : <ScanLine />} Analyze</button>
      </div>
      <div className="progress-track"><span style={{ width: `${latest?.percent ?? 0}%` }} /></div>
      {events.length > 1 && <div className="event-log">{events.slice(-4).map((event, index) => <div key={`${event.at}-${index}`}><span>{event.phase}</span><p>{event.message}</p></div>)}</div>}
    </section>
  );
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

function ReviewWorkspace({ jobs, folders, archiveRoot, selected, onSelect, onChanged }: { jobs: Job[]; folders: string[]; archiveRoot: string; selected: Job | null; onSelect: (id: string) => void; onChanged: () => Promise<void> }) {
  return (
    <section className="workspace-grid">
      <aside className="queue-column">
        <SectionHead title="Review queue" meta={`${jobs.length} waiting`} />
        <JobList jobs={jobs} empty="Nothing needs review." selectedID={selected?.id} onSelect={(job) => onSelect(job.id)} />
      </aside>
      <div className="detail-column">
        {selected ? <JobDetail job={selected} folders={folders} archiveRoot={archiveRoot} onChanged={onChanged} review /> : <EmptyState icon={<FileCheck2 />} title="Review complete" />}
      </div>
    </section>
  );
}

function Documents({ jobs, selected, onSelect }: { jobs: Job[]; selected: Job | null; onSelect: (id: string) => void }) {
  const [query, setQuery] = useState("");
  const visible = jobs.filter((job) => `${job.source_filename} ${job.summary} ${job.classification.sender} ${job.classification.recipient}`.toLowerCase().includes(query.toLowerCase()));
  return <section><div className="documents-head"><SectionHead title="All documents" meta={`${visible.length} shown`} /><label className="search-field"><FileSearch /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search documents" /></label></div><div className="workspace-grid"><div className="queue-column"><JobList jobs={visible} empty="No matching documents." selectedID={selected?.id} onSelect={(job) => onSelect(job.id)} compact /></div><div className="detail-column">{selected ? <JobDetail job={selected} folders={[]} archiveRoot="" onChanged={async () => {}} /> : <EmptyState icon={<Files />} title="Select a document" />}</div></div></section>;
}

function JobList({ jobs, empty, onSelect, selectedID, compact }: { jobs: Job[]; empty: string; onSelect: (job: Job) => void; selectedID?: string; compact?: boolean }) {
  if (!jobs.length) return <div className="empty-list">{empty}</div>;
  return <div className="job-list">{jobs.map((job) => <button key={job.id} className={`${compact ? "job-row compact" : "job-row"}${selectedID === job.id ? " selected" : ""}`} onClick={() => onSelect(job)}><div className="doc-icon"><FileSearch /></div><div className="job-copy"><strong>{job.classification.summary || job.summary || job.source_filename}</strong><span>{displayName(job.classification.sender) || job.source_filename}</span><small>{formatDate(job.updated_at)} · {displayStatus(job.status)}</small></div><div className="job-side"><span className={`status-dot ${job.status}`} />{job.confidence ? <b>{Math.round(job.confidence * 100)}%</b> : null}<ChevronRight /></div></button>)}</div>;
}

function JobDetail({ job, folders, archiveRoot, onChanged, review }: { job: Job; folders: string[]; archiveRoot: string; onChanged: () => Promise<void>; review?: boolean }) {
  const classification = job.classification;
  const [folder, setFolder] = useState(classification.suggested_folder || "");
  const [filename, setFilename] = useState(classification.suggested_filename || job.source_filename);
  const [documentType, setDocumentType] = useState(classification.document_type || "unknown");
  const [paper, setPaper] = useState(classification.physical_original_action || "review");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setFolder(classification.suggested_folder || "");
    setFilename(classification.suggested_filename || job.source_filename);
    setDocumentType(classification.document_type || "unknown");
    setPaper(classification.physical_original_action || "review");
    setError("");
  }, [job.id, classification, job.source_filename]);

  const approve = async () => {
    setSaving(true); setError("");
    try { await api.approve(job.id, { folder, filename, document_type: documentType, physical_original_action: paper }); await onChanged(); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  };
  const reject = async () => {
    setSaving(true); setError("");
    try { await api.reject(job.id); await onChanged(); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  };

  return <article className="document-detail">
    <header className="detail-head"><div><span className="eyebrow">{displayStatus(job.status)} · {displayInputKind(job.input_kind)}</span><h2>{classification.summary || job.summary || job.source_filename}</h2><p>{job.source_filename}</p></div><div className="confidence-ring"><strong>{Math.round(job.confidence * 100)}%</strong><span>confidence</span></div></header>
    {job.error && <div className="inline-error"><CircleAlert /> {job.error}</div>}
    <div className="fact-row"><Fact label="Sender" value={displayName(classification.sender) || "Unknown"} /><Fact label="Recipient" value={displayName(classification.recipient) || "Not detected"} /><Fact label="Type" value={displayName(classification.document_type) || "Unknown"} /><Fact label="Pages" value={String(job.page_count || 0)} /></div>
    {review && job.status === "needs_review" && <section className="routing-form"><div className="route-head"><div><span className="eyebrow">Destination</span><h3>{folder ? "Choose the final folder" : "No archive folder matched"}</h3></div><FolderArchive /></div><label><span>Folder below archive root</span><input list={`folders-${job.id}`} value={folder} onChange={(event) => setFolder(event.target.value)} placeholder="Select or type a folder" /><datalist id={`folders-${job.id}`}>{folders.map((value) => <option key={value} value={value} />)}</datalist></label><label><span>Document type</span><select value={documentType} onChange={(event) => { const next = event.target.value; setFilename((current) => replaceFilenameDocumentType(current, documentType, next)); setDocumentType(next); }}>{documentTypes.map((value) => <option key={value} value={value}>{displayName(value)}</option>)}</select></label><label><span>Paper original</span><select value={paper} onChange={(event) => setPaper(event.target.value)}><option value="keep_original">Keep original</option><option value="discard_candidate">Discard candidate</option><option value="review">Review separately</option></select></label><label className="filename-field"><span>Filename</span><input value={filename} onChange={(event) => setFilename(event.target.value)} /></label><div className="resolved-path"><Archive /> <span>{archiveRoot}{folder ? `/${folder}` : ""}/{filename}</span></div>{error && <div className="form-error">{error}</div>}<div className="review-actions"><button className="primary-button" disabled={!folder || !filename || !documentType || saving} onClick={approve}>{saving ? <LoaderCircle className="spin" /> : <Check />} Approve & archive</button><button className="danger-button" disabled={saving} onClick={reject}><Trash2 /> Reject</button></div></section>}
    {review && job.status === "failed" && <div className="review-actions"><button className="primary-button" onClick={async () => { await api.retry(job.id); await onChanged(); }}><RotateCcw /> Retry from inbox</button></div>}
    <DocumentPreview job={job} />
  </article>;
}

function DocumentPreview({ job }: { job: Job }) {
  const [mode, setMode] = useState<PreviewMode>("pdf");
  const [pages, setPages] = useState<OCRPage[]>([]);
  const [text, setText] = useState("");
  const [opacity, setOpacity] = useState(45);
  const [error, setError] = useState("");

  useEffect(() => {
    setPages([]); setText(""); setError(""); setMode("pdf");
  }, [job.id]);
  useEffect(() => {
    if (mode === "overlay" && !pages.length) api.pages(job.id).then((result) => setPages(result.pages)).catch((reason) => setError(errorMessage(reason)));
    if (mode === "text" && !text) api.text(job.id).then(setText).catch((reason) => setError(errorMessage(reason)));
  }, [job.id, mode, pages.length, text]);

  return <section className="preview-section"><div className="preview-toolbar"><div className="segmented" role="tablist"><button className={mode === "pdf" ? "active" : ""} onClick={() => setMode("pdf")}>{job.text_source === "embedded" ? "Original PDF" : "OCR PDF"}</button>{job.text_source === "ocr" && <button className={mode === "overlay" ? "active" : ""} onClick={() => setMode("overlay")}>Overlay</button>}<button className={mode === "text" ? "active" : ""} onClick={() => setMode("text")}>Text</button></div>{mode === "overlay" && <label className="opacity-control"><span>Opacity</span><input type="range" min="10" max="100" value={opacity} onChange={(event) => setOpacity(Number(event.target.value))} /></label>}</div>{error && <div className="inline-error">{error}</div>}{mode === "pdf" && <iframe title="Searchable PDF" className="pdf-frame" src={job.urls.current} />}{mode === "text" && <pre className="ocr-text">{text || "Loading text..."}</pre>}{mode === "overlay" && <div className="overlay-stack">{pages.length ? pages.map((page) => <div className="ocr-page" key={page.page} style={{ aspectRatio: `${page.width} / ${page.height}` }}><img src={page.image_url} alt={`Cleaned page ${page.page}`} /> <div className="box-layer">{page.boxes.map((box, index) => <span key={index} title={box.text} style={{ left: `${box.left / page.width * 100}%`, top: `${box.top / page.height * 100}%`, width: `${box.width / page.width * 100}%`, height: `${box.height / page.height * 100}%`, opacity: opacity / 100 }} />)}</div></div>) : <LoadingState />}</div>}</section>;
}

function displayInputKind(kind: Job["input_kind"]) {
  if (kind === "digital_pdf") return "Digital PDF";
  if (kind === "mixed_pdf") return "Mixed PDF";
  return "Scan";
}

function Setup({ dashboard, onRefresh }: { dashboard: Dashboard; onRefresh: () => Promise<void> }) {
  const [refreshing, setRefreshing] = useState(false);
  return <section className="setup-layout"><div className="setup-title"><span className="eyebrow">Setup</span><h2>Storage & scanner</h2></div><div className="setup-rows"><SetupRow icon={<Inbox />} title="Scanner inbox" value={dashboard.settings.inbox} state={dashboard.settings.scanner_share_ready ? "SMB ready" : "SMB setup needed"} ok={dashboard.settings.scanner_share_ready} /><SetupRow icon={<Archive />} title="Archive root" value={dashboard.settings.archive_root} state={dashboard.settings.archive_exists ? "Connected" : "Unavailable"} ok={dashboard.settings.archive_exists} /><SetupRow icon={<FileSearch />} title="Local model" value={dashboard.settings.model} state="Ollama" ok /></div>{!dashboard.settings.scanner_share_ready && <div className="setup-note"><strong>Scanner sharing</strong><ol><li>Open System Settings → General → Sharing.</li><li>Turn on File Sharing and add the scanner inbox.</li><li>Enable SMB and use the inbox share on the scanner.</li></ol></div>}<section className="folder-browser"><div><span className="eyebrow">Archive folders</span><h3>{dashboard.folders.length} available destinations</h3></div><button className="icon-text-button" disabled={refreshing} onClick={async () => { setRefreshing(true); await api.refreshFolders(); await onRefresh(); setRefreshing(false); }}><RefreshCw className={refreshing ? "spin" : ""} /> Refresh</button><div className="folder-grid">{dashboard.folders.map((folder) => <span key={folder}><FolderArchive /> {folder}</span>)}</div></section></section>;
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
