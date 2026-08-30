export const documentTypes = [
  "receipt",
  "routine-invoice",
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

export function replaceFilenameDocumentType(filename: string, previous: string, next: string): string {
  const token = `__${previous}__`;
  if (filename.includes(token)) return filename.replace(token, `__${next}__`);
  const suffix = `__${previous}.pdf`;
  if (filename.endsWith(suffix)) return `${filename.slice(0, -suffix.length)}__${next}.pdf`;
  return filename;
}
