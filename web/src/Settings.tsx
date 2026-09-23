import {
  ArrowLeft,
  ArrowRight,
  Check,
  CircleAlert,
  FileSearch,
  FolderArchive,
  FolderOpen,
  Inbox,
  RefreshCw,
  ScanLine,
  Users,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api } from "./api";
import { EmbeddingSettings } from "./EmbeddingSettings";
import { RecipientSettings } from "./RecipientSettings";
import type { Dashboard } from "./types";

type Section = "scanner" | "model" | "embeddings" | "documents" | "recipients";
const titles: Record<Section, string> = {
  scanner: "Scanner inbox",
  model: "Local model",
  embeddings: "Document similarity",
  documents: "Documents",
  recipients: "Recipients",
};

export function Settings({
  dashboard,
  onRefresh,
}: {
  dashboard: Dashboard;
  onRefresh: () => Promise<void>;
}) {
  const [section, setSection] = useState<Section | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const layout = useRef<HTMLElement>(null);
  const returnTo = useRef<string | null>(null);
  useEffect(() => {
    if (section) {
      layout.current?.scrollTo(0, 0);
      heading.current?.focus();
    } else {
      if (returnTo.current) document.getElementById(returnTo.current)?.focus();
    }
  }, [section]);
  const [refreshing, setRefreshing] = useState(false);
  const [choosing, setChoosing] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [setupError, setSetupError] = useState("");
  const [backingUp, setBackingUp] = useState(false);
  const [modelProvider, setModelProvider] = useState(
    dashboard.settings.model_provider,
  );
  const [savingModel, setSavingModel] = useState(false);
  const [installingModel, setInstallingModel] = useState(false);
  const [installProgress, setInstallProgress] = useState("");
  const saveModel = async (install = false) => {
    setSavingModel(true);
    setSetupError("");
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
      setSetupError(reason instanceof Error ? reason.message : String(reason));
      setSavingModel(false);
    } finally {
      setInstallingModel(false);
    }
  };
  const chooseDirectory = async () => {
    setChoosing(true);
    setSetupError("");
    try {
      await api.chooseDocumentsDirectory();
      setRestarting(true);
      window.setTimeout(() => window.location.reload(), 1500);
    } catch (reason) {
      setSetupError(reason instanceof Error ? reason.message : String(reason));
      setChoosing(false);
    }
  };
  const openSharing = async () => {
    setSetupError("");
    try {
      await api.openSharingSettings();
    } catch (reason) {
      setSetupError(reason instanceof Error ? reason.message : String(reason));
    }
  };
  const backupNow = async () => {
    setBackingUp(true);
    setSetupError("");
    try {
      await api.backupDatabase();
      await onRefresh();
    } catch (reason) {
      setSetupError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBackingUp(false);
    }
  };
  const settings = dashboard.settings;
  const profiles = dashboard.recipient_profiles || [];
  const modelName = settings.model_enabled
    ? settings.model_provider === "fm"
      ? "Apple Foundation Models"
      : `${settings.model_provider === "bonsai" ? "Bonsai" : "Ollama"} · ${settings.model}`
    : "Local rules only";
  const scannerStatus = settings.scanner_share_ready
    ? "Inbox shared"
    : settings.scanner_share_checked
      ? "Not shared"
      : "Status unknown";
  const similarityStatus = !settings.embeddings_enabled
    ? "Not enabled"
    : dashboard.similarity?.status === "unavailable"
      ? "Needs attention"
      : dashboard.similarity?.status === "indexing"
        ? "Indexing"
        : "Enabled";
  const open = (next: Section, button: HTMLButtonElement) => {
    returnTo.current = button.id;
    setSetupError("");
    setSection(next);
  };
  return (
    <section
      className="setup-layout"
      ref={layout}
      aria-labelledby="settings-title"
    >
      <header className="setup-title">
        {section && (
          <button
            type="button"
            className="icon-text-button settings-back"
            disabled={choosing || savingModel || restarting}
            onClick={() => {
              setSetupError("");
              setSection(null);
            }}
          >
            <ArrowLeft /> Back to setup
          </button>
        )}
        <span className="eyebrow">{section ? "Setup" : "Your Paperless"}</span>
        <h1 id="settings-title" tabIndex={-1} ref={heading}>
          {section ? titles[section] : "Setup overview"}
        </h1>
      </header>
      {!section && (
        <div className="settings-grid">
          <SettingsCard
            title={titles.scanner}
            icon={<Inbox />}
            status={scannerStatus}
            tone={settings.scanner_share_ready ? "ok" : "neutral"}
            description="Send scans straight to Paperless from your network scanner."
            value={settings.inbox}
            onOpen={(button) => open("scanner", button)}
          />
          <SettingsCard
            title={titles.model}
            icon={<FileSearch />}
            status={settings.model_enabled ? "Configured" : "Optional"}
            tone={settings.model_enabled ? "ok" : "neutral"}
            description="Local suggestions for filenames, recipients, and folders."
            value={modelName}
            onOpen={(button) => open("model", button)}
          />
          <SettingsCard
            title={titles.embeddings}
            icon={<ScanLine />}
            status={similarityStatus}
            tone={
              !settings.embeddings_enabled
                ? "neutral"
                : dashboard.similarity?.status === "unavailable"
                  ? "bad"
                  : "ok"
            }
            description="Find related documents with a local embedding model."
            value={
              settings.embeddings_enabled
                ? `${settings.embedding_model || "embeddinggemma"} · ${dashboard.similarity?.indexed ?? 0} documents indexed`
                : "Set up document similarity when you need it"
            }
            onOpen={(button) => open("embeddings", button)}
          />
          <SettingsCard
            title={titles.documents}
            icon={<FolderArchive />}
            status={settings.archive_exists ? "Connected" : "Unavailable"}
            tone={settings.archive_exists ? "ok" : "bad"}
            description="Your archive folder, filing destinations, and database backups."
            value={settings.archive_root || "No documents folder selected"}
            onOpen={(button) => open("documents", button)}
          />
          <SettingsCard
            title={titles.recipients}
            icon={<Users />}
            status={`${profiles.length} recipient${profiles.length === 1 ? "" : "s"}`}
            tone="neutral"
            description="Help Paperless recognize who your documents belong to. Manage names, addresses, and personal or business filing areas."
            value={
              profiles.length
                ? profiles
                    .slice(0, 3)
                    .map((profile) => profile.name)
                    .join(" · ") +
                  (profiles.length > 3 ? ` · +${profiles.length - 3} more` : "")
                : "Add your first recipient, or let Paperless learn during review"
            }
            featured
            onOpen={(button) => open("recipients", button)}
          >
            <span className="settings-card-learning">
              {dashboard.learning_count || 0} approved filing examples
            </span>
          </SettingsCard>
        </div>
      )}
      {section === "documents" && (
        <>
          <section className="setup-wizard">
            <div className="setup-step-copy">
              <h3>Choose your base documents directory</h3>
              <p>
                Choose an existing folder on this Mac. It can be an ordinary
                local folder or a locally available Dropbox, Google Drive,
                iCloud Drive, OneDrive, or mounted file-server folder.
              </p>
              <p>
                Paperless files completed documents below this directory. Its
                live SQLite database remains in local Application Support so a
                sync client cannot corrupt it. Consistent database snapshots are
                stored in a hidden backup folder below the selected directory.
              </p>
              {dashboard.settings.archive_root && (
                <code>{dashboard.settings.archive_root}</code>
              )}
              {dashboard.database_backup?.directory && (
                <p>
                  {dashboard.database_backup.count
                    ? `${dashboard.database_backup.count} database backup${dashboard.database_backup.count === 1 ? "" : "s"} · latest ${dashboard.database_backup.latest}`
                    : "The first database backup will be created after startup."}{" "}
                  <button
                    type="button"
                    className="text-button"
                    disabled={backingUp}
                    onClick={backupNow}
                  >
                    {backingUp ? "Backing up…" : "Back up now"}
                  </button>
                </p>
              )}
              {dashboard.settings.archive_error &&
                !dashboard.settings.setup_required && (
                  <div className="inline-error">
                    {dashboard.settings.archive_error}
                  </div>
                )}
            </div>
            <button
              type="button"
              className="primary-button"
              disabled={choosing || savingModel || restarting}
              onClick={chooseDirectory}
            >
              <FolderOpen />{" "}
              {restarting
                ? "Restarting Paperless…"
                : choosing
                  ? "Opening folder chooser…"
                  : dashboard.settings.archive_root
                    ? "Choose another folder"
                    : "Choose documents folder"}
            </button>
          </section>
          <section className="folder-browser">
            <div>
              <span className="eyebrow">Document folders</span>
              <h3>{dashboard.folders.length} available destinations</h3>
            </div>
            <button
              type="button"
              className="icon-text-button"
              disabled={refreshing}
              onClick={async () => {
                setRefreshing(true);
                setSetupError("");
                try {
                  await api.refreshFolders();
                  await onRefresh();
                } catch (reason) {
                  setSetupError(
                    reason instanceof Error ? reason.message : String(reason),
                  );
                } finally {
                  setRefreshing(false);
                }
              }}
            >
              <RefreshCw className={refreshing ? "spin" : ""} /> Refresh
            </button>
            <div className="folder-grid">
              {dashboard.folders.map((folder) => (
                <span key={folder}>
                  <FolderArchive /> {folder}
                </span>
              ))}
            </div>
          </section>
        </>
      )}
      {section === "scanner" && (
        <>
          <div className="settings-detail-status">
            <span
              className={`setup-status ${settings.scanner_share_ready ? "ok" : ""}`}
            >
              {scannerStatus}
            </span>
            <button
              type="button"
              className="icon-text-button"
              disabled={refreshing}
              onClick={async () => {
                setRefreshing(true);
                setSetupError("");
                try {
                  await onRefresh();
                } catch (reason) {
                  setSetupError(
                    reason instanceof Error ? reason.message : String(reason),
                  );
                } finally {
                  setRefreshing(false);
                }
              }}
            >
              <RefreshCw className={refreshing ? "spin" : ""} /> Check again
            </button>
          </div>
          <section className="setup-wizard">
            <div className="setup-step-copy">
              <h3>Share the scanner inbox over SMB</h3>
              <p>Your scanner writes new files to this dedicated inbox:</p>
              <code>{dashboard.settings.inbox}</code>
              <ol>
                <li>Open macOS Sharing settings and turn on File Sharing.</li>
                <li>Add the scanner inbox above as a shared folder.</li>
                <li>
                  Under Options, enable “Share files and folders using SMB” and
                  enable the scanner’s macOS user.
                </li>
                <li>
                  On the scanner, enter this Mac’s hostname or IP address, the
                  user credentials, and share name{" "}
                  <strong>
                    {dashboard.settings.inbox
                      .split("/")
                      .filter(Boolean)
                      .at(-1) || "inbox"}
                  </strong>
                  .
                </li>
              </ol>
              <p>
                Use a dedicated macOS account limited to this inbox when your
                scanner supports authenticated SMB.
              </p>
            </div>
            <button
              type="button"
              className="icon-text-button"
              onClick={openSharing}
            >
              Open Sharing settings
            </button>
          </section>
        </>
      )}
      {section === "model" && (
        <section className="setup-wizard">
          <div className="setup-step-copy">
            <h3>Choose your local model</h3>
            <p>Choose a model to process documents locally.</p>
            <label className="model-choice">
              <span>Model provider</span>
              <select
                value={modelProvider}
                disabled={savingModel || restarting}
                onChange={(event) =>
                  setModelProvider(
                    event.target.value as "ollama" | "fm" | "bonsai",
                  )
                }
              >
                <option value="ollama">Ollama · Qwen 3.5</option>
                <option value="fm">Apple Foundation Models</option>
                <option value="bonsai">Bonsai · PrismML</option>
              </select>
            </label>
            <p>
              {modelProvider === "fm"
                ? "Uses Apple Intelligence through the fm command. No separate model download or server is needed. Documents that exceed its context limit require review."
                : modelProvider === "bonsai"
                  ? "Install Bonsai 8B (1-bit) and its local server, or save an existing Bonsai server. Installation downloads about 1.16 GB of model weights plus the runtime and starts the server automatically at login. Keep this page open during installation."
                  : "Uses your configured Ollama model. Ollama must be running with the model installed."}
            </p>
            {modelProvider === "bonsai" && (
              <p>
                <button
                  type="button"
                  className="icon-text-button"
                  disabled={savingModel || restarting}
                  onClick={() => saveModel(true)}
                >
                  {installingModel
                    ? "Installing Bonsai…"
                    : "Install & use Bonsai 8B"}
                </button>
              </p>
            )}
            {installProgress && (
              <p role="status" aria-live="polite">
                {installProgress}
              </p>
            )}
            <p>
              Saving restarts Paperless. Wait for active uploads to finish
              first.
            </p>
          </div>
          <button
            type="button"
            className="primary-button"
            disabled={
              savingModel ||
              restarting ||
              (modelProvider === dashboard.settings.model_provider &&
                dashboard.settings.model_enabled)
            }
            onClick={() => saveModel()}
          >
            {restarting
              ? "Restarting Paperless…"
              : savingModel
                ? "Checking model…"
                : "Save model"}
          </button>
        </section>
      )}
      {section === "embeddings" && (
        <EmbeddingSettings dashboard={dashboard} onRefresh={onRefresh} />
      )}
      {section === "recipients" && (
        <RecipientSettings dashboard={dashboard} onRefresh={onRefresh} />
      )}
      {setupError && (
        <div className="form-error" role="alert">
          {setupError}
        </div>
      )}
    </section>
  );
}

function SettingsCard({
  title,
  icon,
  status,
  tone,
  description,
  value,
  featured = false,
  children,
  onOpen,
}: {
  title: string;
  icon: ReactNode;
  status: string;
  tone: "ok" | "bad" | "neutral";
  description: string;
  value: string;
  featured?: boolean;
  children?: ReactNode;
  onOpen: (button: HTMLButtonElement) => void;
}) {
  return (
    <button
      type="button"
      id={`settings-${title.toLowerCase().replaceAll(" ", "-")}`}
      className={`settings-card${featured ? " settings-card-featured" : ""}`}
      onClick={(event) => onOpen(event.currentTarget)}
    >
      <span className="settings-card-top">
        <span className="round-icon">{icon}</span>
        <span className={`setup-status ${tone}`}>
          {tone === "ok" ? <Check /> : tone === "bad" ? <CircleAlert /> : null}
          {status}
        </span>
      </span>
      <span className="settings-card-title">
        {title}
        <ArrowRight aria-hidden="true" />
      </span>
      <span className="settings-card-description">{description}</span>
      <span className="settings-card-value" title={value}>
        {value}
      </span>
      {children}
    </button>
  );
}
