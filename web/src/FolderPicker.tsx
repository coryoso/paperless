import { ChevronRight, FolderArchive } from "lucide-react";
import { useState } from "react";
import { changeFolderPart, folderChildren, usesOtherDirectory } from "./review";

export function FolderPicker({ folders, value, onChange }: { folders: string[]; value: string; onChange: (folder: string) => void }) {
  const [custom, setCustom] = useState(() => usesOtherDirectory(value, folders));
  const parts = value.split("/").filter(Boolean);
  const levels = [...parts];
  if (folderChildren(folders, value).length || !parts.length) levels.push("");

  return <div className="destination-folder-field">
    <div className="folder-picker-head"><span>Folder below archive root</span><button type="button" className="icon-text-button" aria-expanded={custom} onClick={() => setCustom(!custom)}><FolderArchive />{custom ? "Browse folders" : "Other directory…"}</button></div>
    {custom ? <label className="custom-folder-field"><span>Directory path</span><input aria-label="Directory path" autoFocus value={value} onChange={(event) => onChange(event.target.value)} placeholder="For example: Family/School" /><small>Relative to your archive root. New folders are created when you approve.</small></label> :
      <nav className="folder-breadcrumbs" aria-label="Destination folders"><span className="folder-root"><FolderArchive />Archive</span>{levels.map((part, depth) => {
        const parent = parts.slice(0, depth).join("/");
        const children = folderChildren(folders, parent);
        if (part && !children.includes(part)) children.push(part);
        return <div className="folder-level" key={depth}><ChevronRight aria-hidden="true" /><select aria-label={`Folder level ${depth + 1}`} value={part} onChange={(event) => onChange(changeFolderPart(value, depth, event.target.value))}><option value="">{depth === 0 ? "Choose folder" : part ? "Use parent folder" : "Subfolder…"}</option>{children.map((child) => <option key={child} value={child}>{child}</option>)}</select></div>;
      })}</nav>}
  </div>;
}
