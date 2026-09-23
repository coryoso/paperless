import type { Identity, PostalAddress } from "./ocr-clusters";
export function identityText(identity?: Identity): string {
  if (!identity) return "";
  return [...identity.names, primaryAddress(identity)?.lines.join("\n") ?? ""]
    .filter(Boolean)
    .join("\n");
}

export function localDate(value?: string): string {
  if (!value) return "";
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(
    new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3])),
  );
}
export function parseAddress(value: string): PostalAddress {
  const lines = value
    .split(/\n/)
    .map((s) => s.trim())
    .filter(Boolean);
  const postal = lines.map((s) => /^(\d{4,6})\s+(.+)$/.exec(s)).find(Boolean);
  const street = lines
    .map((s) =>
      /^(.+?[\p{L}.])\s*(\d+\s*[\p{L}]?(?:\s*[-/]\s*\d+\s*[\p{L}]?)?)$/u.exec(
        s,
      ),
    )
    .find(Boolean);
  return {
    lines,
    street_name: street?.[1] ?? "",
    house_number: street?.[2] ?? "",
    postal_code: postal?.[1] ?? "",
    city: postal?.[2] ?? "",
  };
}
export function primaryAddress(identity?: Identity): PostalAddress | undefined {
  return (
    identity?.addresses[identity.primary_address ?? 0] ?? identity?.addresses[0]
  );
}
