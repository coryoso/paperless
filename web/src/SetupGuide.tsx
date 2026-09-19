import { ArrowLeft, ArrowRight, Check, CircleAlert, FolderOpen, LoaderCircle, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import { api, waitForSetup } from "./api";
import type { Dashboard } from "./types";
import { guideSteps, modelChoices, type GuideStep, type ModelChoice } from "./setup";

export function SetupGuide({ dashboard, onRefresh, onComplete }: { dashboard: Dashboard; onRefresh: () => Promise<void>; onComplete: () => void }) {
  const settings = dashboard.settings;
  const savedStep = settings.setup_step === "complete" ? "ready" : settings.setup_step;
  const [step, setStep] = useState<GuideStep>(savedStep);
  const [choice, setChoice] = useState<ModelChoice>(settings.model_enabled ? settings.model_provider : "rules");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  useEffect(() => { setStep(savedStep); }, [savedStep]);
  const index = guideSteps.findIndex((item) => item.id === step);
  const reached = guideSteps.findIndex((item) => item.id === savedStep);

  const run = async (action: () => Promise<void>) => {
    setBusy(true); setError(""); setMessage("");
    try { await action(); } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)); }
    finally { setBusy(false); }
  };
  const moveTo = (next: GuideStep) => { setStep(next); setError(""); setMessage(""); };
  const chooseFolder = () => run(async () => {
    setMessage("Choose a folder in the macOS window…");
    const result = await api.chooseDocumentsDirectory();
    setMessage("Saving your documents folder…");
    await waitForSetup({ setup_step: "scanner", archive_root: result.documents_directory });
    await onRefresh();
    moveTo("scanner");
  });
  const continueScanner = () => run(async () => {
    await api.setupProgress("model");
    setMessage("Saving your setup progress…");
    await waitForSetup({ setup_step: "model" });
    await onRefresh();
    moveTo("model");
  });
  const saveModel = (install: boolean) => run(async () => {
    if (install) {
      setMessage("Preparing Bonsai installation…");
      await api.installBonsai(setMessage);
    }
    setMessage(choice === "rules" ? "Saving your choice…" : "Checking your local model…");
    const provider = choice === "rules" ? settings.model_provider : choice;
    await api.setModelProvider(provider, choice !== "rules");
    await waitForSetup({ setup_step: "ready", model_provider: provider, model_enabled: choice !== "rules" });
    await onRefresh();
    moveTo("ready");
  });
  const finish = () => run(async () => {
    setMessage("Checking your settings and opening your archive…");
    await api.setupProgress("complete");
    await waitForSetup({ setup_required: false });
    await onRefresh();
    onComplete();
  });
  const modelName = settings.model_enabled ? `${modelChoices.find((item) => item.id === settings.model_provider)?.name} · ${settings.model}` : "Local rules · AI can be added later";

  return <section className="setup-guide" aria-labelledby="guide-title">
    <header className="guide-intro">
      <span className="eyebrow">A few steps to your first document</span>
      <h1 id="guide-title">Welcome to Paperless</h1>
      <p>Choose where your documents belong and how you want to work. Your progress is saved as you go.</p>
    </header>
    <ol className="guide-progress" aria-label="Setup progress">
      {guideSteps.map((item, i) => <li key={item.id} className={i === index ? "current" : i < reached ? "visited" : ""}>
        <button aria-current={i === index ? "step" : undefined} disabled={busy || i > reached} onClick={() => moveTo(item.id)}>
          <span>{i < reached ? <Check aria-hidden="true" /> : i + 1}</span><strong>{item.label}</strong>
        </button>
      </li>)}
    </ol>
    <article className="guide-card">
      <span className="eyebrow">Step {index + 1} of {guideSteps.length}{step === "scanner" ? " · Optional" : ""}</span>
      {step === "documents" && <>
        <h2>A home for your documents</h2>
        <p>Pick the folder where Paperless should file your documents. A local folder, mounted server folder, or a folder synced with iCloud Drive, Dropbox, or Google Drive all work.</p>
        <div className="guide-callout"><ShieldCheck aria-hidden="true" /><p>Your originals stay in your archive. Paperless keeps its database on this Mac and saves database backups alongside your documents.</p></div>
        {settings.archive_root && <code className="guide-path">{settings.archive_root}</code>}
        <div className="guide-actions"><button className="primary-button" disabled={busy} onClick={chooseFolder}><FolderOpen />{settings.archive_root ? "Choose another folder" : "Choose documents folder"}</button>{settings.archive_root && reached > 0 && <button className="icon-text-button" disabled={busy} onClick={() => moveTo("scanner")}>Continue <ArrowRight /></button>}</div>
      </>}
      {step === "scanner" && <>
        <h2>Connect your scanner</h2>
        <p>A network scanner can send scans straight to this inbox. You can also skip this step and upload documents in Paperless.</p>
        <code className="guide-path">{settings.inbox}</code>
        <ol className="guide-instructions">
          <li>Open macOS Sharing settings and turn on <strong>File Sharing</strong>.</li>
          <li>Add the scanner inbox above to your shared folders.</li>
          <li>Under Options, turn on SMB sharing and enable the scanner’s macOS user.</li>
          <li>On your scanner, enter this Mac’s hostname or IP address, that user’s credentials, and the share name <strong>{settings.inbox.split("/").filter(Boolean).at(-1) || "inbox"}</strong>.</li>
        </ol>
        <div className="guide-inline-actions">
          <button className="icon-text-button" disabled={busy} onClick={() => run(async () => { await api.openSharingSettings(); })}>Open Sharing settings</button>
          <button className="text-button" disabled={busy} onClick={() => run(onRefresh)}>Check again</button>
          <span className={`guide-share ${settings.scanner_share_ready ? "ready" : ""}`}>{settings.scanner_share_ready ? <><Check /> Inbox is shared</> : settings.scanner_share_checked ? "Inbox is not shared yet" : "Sharing status could not be checked"}</span>
        </div>
        <div className="guide-actions"><button className="icon-text-button" disabled={busy} onClick={() => moveTo("documents")}><ArrowLeft /> Back</button><button className="primary-button" disabled={busy} onClick={continueScanner}>{settings.scanner_share_ready ? "Continue" : "Skip scanner for now"}<ArrowRight /></button></div>
      </>}
      {step === "model" && <>
        <h2>Choose how to organize your documents</h2>
        <p>A local AI model can suggest filenames, recipients, and archive folders. You can start with local rules and add AI later in Setup.</p>
        <fieldset className="guide-models" disabled={busy}><legend className="sr-only">Document classification</legend>
          {modelChoices.map((model) => <label key={model.id} className={choice === model.id ? "selected" : ""}>
            <input type="radio" name="guide-model" value={model.id} checked={choice === model.id} onChange={() => { setChoice(model.id); setError(""); setMessage(""); }} />
            <span><strong>{model.name}</strong><span>{model.description}</span></span>
          </label>)}
        </fieldset>
        {choice === "bonsai" && <p className="guide-help">Installation downloads about 1.16 GB of model weights plus the runtime and starts a local server at login. Keep this page open until it finishes. Interrupted downloads can be resumed.</p>}
        {choice === "ollama" && <div className="guide-help"><p>Start Ollama with your configured model installed. If you need it, run these commands in Terminal:</p><code className="guide-path">brew install ollama<br />brew services start ollama<br />ollama pull {settings.model_provider === "ollama" ? settings.model : "qwen3.5:9b-q4_K_M"}</code></div>}
        {choice === "fm" && <p className="guide-help">Requires Apple Intelligence to be enabled and the fm command to be installed. Paperless checks that the system model is available before continuing.</p>}
        <div className="guide-actions"><button className="icon-text-button" disabled={busy} onClick={() => moveTo("scanner")}><ArrowLeft /> Back</button>{choice === "bonsai" && <button className="icon-text-button" disabled={busy} onClick={() => saveModel(false)}>Use existing Bonsai server</button>}<button className="primary-button" disabled={busy} onClick={() => saveModel(choice === "bonsai")}>{choice === "bonsai" ? "Install & continue" : choice === "rules" ? "Continue with local rules" : "Check & continue"}<ArrowRight /></button></div>
      </>}
      {step === "ready" && <>
        <h2>You’re ready to start</h2>
        <p>Review your choices, then open your archive. You can change these settings whenever you need to.</p>
        <dl className="guide-summary"><div><dt>Documents folder</dt><dd>{settings.archive_root}</dd></div><div><dt>Scanner inbox</dt><dd>{settings.inbox}<span>{settings.scanner_share_ready ? "Shared with your scanner" : "Sharing can be set up later"}</span></dd></div><div><dt>Document classification</dt><dd>{modelName}</dd></div></dl>
        <div className="guide-callout"><Check aria-hidden="true" /><p>Upload your first PDF or image, or place a scan in the inbox. Paperless reads the text and suggests where to file it. Documents that need your input appear in Review.</p></div>
        <div className="guide-actions"><button className="icon-text-button" disabled={busy} onClick={() => moveTo("model")}><ArrowLeft /> Back</button><button className="primary-button" disabled={busy} onClick={finish}>Open my archive<ArrowRight /></button></div>
      </>}
      {busy && <p className="guide-feedback" role="status" aria-live="polite"><LoaderCircle className="spin" />{message || "Working…"}</p>}
      {error && <div className="form-error guide-feedback" role="alert"><CircleAlert />{error}</div>}
    </article>
    <p className="guide-footer">Inbox processing starts after you finish setup. Scanner sharing and AI are optional.</p>
  </section>;
}
