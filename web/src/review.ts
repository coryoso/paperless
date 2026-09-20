export const documentTypes = [
  "receipt",
  "routine-invoice",
  "payment-reminder",
  "insurance-letter",
  "insurance-policy",
  "tax-letter",
  "government-letter",
  "bank-document",
  "medical-document",
  "contract",
  "legal-letter",
  "delivery-receipt",
  "marketing",
  "letter",
  "unknown",
] as const;

export const recipientScopes = [
  { value: "personal", label: "Personal" },
  { value: "sole_proprietor", label: "Sole proprietor" },
  { value: "gbr", label: "GbR" },
  { value: "organization", label: "Other organization" },
  { value: "unknown", label: "Unclear" },
] as const;

export function splitAddresses(value: string): string[] {
  return value
    .replace(/\r\n/g, "\n")
    .split(/\n\s*\n/)
    .map((block) => block.trim())
    .filter(Boolean);
}

export function folderChildren(folders: string[], parent: string): string[] {
  const prefix = parent ? `${parent}/` : "";
  return [
    ...new Set(
      folders
        .filter((folder) => folder.startsWith(prefix))
        .map((folder) => folder.slice(prefix.length).split("/")[0])
        .filter(Boolean),
    ),
  ].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));
}

export function changeFolderPart(
  folder: string,
  depth: number,
  value: string,
): string {
  return [...folder.split("/").filter(Boolean).slice(0, depth), value]
    .filter(Boolean)
    .join("/");
}

export function usesOtherDirectory(folder: string, folders: string[]): boolean {
  return (
    folder !== "" &&
    !folders.some((known) => known === folder || known.startsWith(`${folder}/`))
  );
}

export function replaceFilenameDocumentType(
  filename: string,
  previous: string,
  next: string,
): string {
  const token = `__${previous}__`;
  if (filename.includes(token)) return filename.replace(token, `__${next}__`);
  const suffix = `__${previous}.pdf`;
  if (filename.endsWith(suffix))
    return `${filename.slice(0, -suffix.length)}__${next}.pdf`;
  return filename;
}

export function recipientKey(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(
      /[äöüß]/g,
      (letter) => ({ ä: "ae", ö: "oe", ü: "ue", ß: "ss" })[letter] ?? letter,
    )
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
}

export function savedRecipientChoice(
  classification: import("./types").Classification,
  profiles: import("./types").RecipientProfile[],
): string {
  const byID = profiles.find(
    (p) => p.id === classification.recipient_profile_id,
  );
  if (byID) return String(byID.id);
  const key = recipientKey(classification.recipient || "");
  const matches = key
    ? profiles.filter(
        (p) =>
          p.scope === classification.recipient_scope &&
          [p.name, ...p.aliases].some((name) => recipientKey(name) === key),
      )
    : [];
  return matches.length === 1
    ? String(matches[0].id)
    : profiles.length
      ? ""
      : "new";
}

export function learnedAlias(
  detected: string,
  profile?: import("./types").RecipientProfile,
): string {
  const key = recipientKey(detected);
  if (
    !profile ||
    detected.length > 200 ||
    [
      "",
      "unknown",
      "unclear",
      "unbekannt",
      "herr",
      "herrn",
      "frau",
      "recipient",
    ].includes(key)
  )
    return "";
  return [profile.name, ...profile.aliases].some(
    (name) => recipientKey(name) === key,
  )
    ? ""
    : detected;
}
