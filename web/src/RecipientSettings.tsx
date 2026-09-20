import { useState } from "react";
import { Check, Plus, Settings2 } from "lucide-react";
import { api } from "./api";
import { FolderPicker } from "./FolderPicker";
import { recipientScopes, splitAddresses } from "./review";
import type { Dashboard, RecipientProfile } from "./types";

const emptyProfile = (): RecipientProfile => ({ id: 0, name: "", scope: "personal", aliases: [], addresses: [], folder_prefix: "" });

export function RecipientSettings({ dashboard, onRefresh }: { dashboard: Dashboard; onRefresh: () => Promise<void> }) {
  const [draft, setDraft] = useState<RecipientProfile | null>(null);
  const [aliases, setAliases] = useState("");
  const [addresses, setAddresses] = useState("");
  const [sharedDraft, setSharedDraft] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const edit = (profile: RecipientProfile) => { setDraft({ ...profile }); setAliases(profile.aliases.join("\n")); setAddresses((profile.addresses || []).join("\n\n")); setError(""); };
  const save = async () => {
    if (!draft) return;
    setSaving(true); setError("");
    try {
      await api.saveRecipient({ ...draft, aliases: aliases.split("\n").map((s) => s.trim()).filter(Boolean), addresses: splitAddresses(addresses) });
      await onRefresh(); setDraft(null);
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)); }
    finally { setSaving(false); }
  };
  const saveShared = async () => {
    if (sharedDraft === null) return;
    setSaving(true); setError("");
    try {
      await api.saveRecipientAddresses(splitAddresses(sharedDraft));
      await onRefresh(); setSharedDraft(null);
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)); }
    finally { setSaving(false); }
  };
  return <section className="recipient-settings">
    <div className="section-head"><div><span className="eyebrow">Recipients & learning</span><h2>Who your documents belong to</h2></div><button className="icon-text-button" onClick={() => edit(emptyProfile())}><Plus />Add recipient</button></div>
    <p>Recipient names and aliases fill in as you approve documents. Add postal addresses to help identify names in the correct address block. A person can have separate personal and business profiles; a GbR has its own profile.</p>
    <div className="section-head"><h3>Shared postal addresses</h3><button className="icon-text-button" disabled={saving} onClick={() => { setSharedDraft((dashboard.recipient_addresses || []).join("\n\n")); setError(""); }}>Edit shared addresses</button></div>
    <p>Addresses where mail for several people or businesses arrives. These help locate the recipient block; names and context still decide who the document belongs to.</p>
    {sharedDraft === null && (dashboard.recipient_addresses || []).map((address) => <p key={address}><code style={{ whiteSpace: "pre-wrap" }}>{address}</code></p>)}
    {sharedDraft !== null && <div className="routing-form profile-form">
      <label className="filename-field"><span>Shared addresses · separate addresses with a blank line</span><textarea rows={6} value={sharedDraft} onChange={(event) => setSharedDraft(event.target.value)} placeholder={"Musterstraße 12\n12345 Berlin"} /></label>
      <p className="learning-note">Enter the street and house number, then the postcode and city. Remove an address here to stop using it.</p>
      {error && <div className="form-error">{error}</div>}
      <div className="review-actions"><button className="primary-button" disabled={saving} onClick={saveShared}><Check />{saving ? "Saving…" : "Save shared addresses"}</button><button className="icon-text-button" disabled={saving} onClick={() => setSharedDraft(null)}>Cancel</button></div>
    </div>}
    <div className="recipient-profile-list">{(dashboard.recipient_profiles || []).map((profile) => <button key={profile.id} className="recipient-profile" onClick={() => edit(profile)}><div><strong>{profile.name}</strong><span>{recipientScopes.find((scope) => scope.value === profile.scope)?.label || "Unclear"}{profile.aliases.length ? ` · ${profile.aliases.length} aliases` : ""}{profile.folder_prefix ? ` · ${profile.folder_prefix}` : " · No filing area set"}</span></div><Settings2 aria-label="Edit recipient" /></button>)}</div>
    {!dashboard.recipient_profiles?.length && <div className="empty-list">Add a recipient now, or confirm one during review.</div>}
    {draft && <div className="routing-form profile-form">
      <label><span>Recipient name</span><input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} /></label>
      <label><span>Capacity</span><select value={draft.scope} onChange={(event) => setDraft({ ...draft, scope: event.target.value })}>{recipientScopes.map(({ value, label }) => <option key={value} value={value}>{label}</option>)}</select></label>
      <label className="filename-field"><span>Other spellings or names · one per line</span><textarea rows={3} value={aliases} onChange={(event) => setAliases(event.target.value)} placeholder="Names that identify this same recipient" /></label>
      <label className="filename-field"><span>Addresses for this recipient · separate addresses with a blank line</span><textarea rows={5} value={addresses} onChange={(event) => setAddresses(event.target.value)} placeholder={"Musterstraße 12\n12345 Berlin"} /></label>
      <p className="learning-note">Use addresses associated with this person or business. A matching name and address strengthen the suggestion; a shared location alone never identifies a person.</p>
      <FolderPicker key={`${draft.id}-${draft.scope}`} folders={dashboard.folders} value={draft.folder_prefix} onChange={(folder_prefix) => setDraft({ ...draft, folder_prefix })} />
      <p className="learning-note">This optional filing area is reserved for this recipient and capacity. Personal and business documents are kept distinct.</p>
      {draft.folder_prefix && <button className="icon-text-button" onClick={() => setDraft({ ...draft, folder_prefix: "" })}>Clear filing area</button>}
      {error && <div className="form-error">{error}</div>}
      <div className="review-actions"><button className="primary-button" disabled={saving || !draft.name.trim()} onClick={save}><Check />{saving ? "Saving…" : "Save recipient"}</button><button className="icon-text-button" disabled={saving} onClick={() => setDraft(null)}>Cancel</button></div>
    </div>}
    <div className="learning-status"><strong>{dashboard.learning_count || 0} approved filing examples saved</strong><p>Similar approvals are supplied to the local model and guide future folder suggestions. Ambiguous recipients still need review. Moving files directly in Finder does not teach the system.</p><span>Stored on this Mac: {dashboard.learning_path || "Local Paperless database"}</span></div>
  </section>;
}
