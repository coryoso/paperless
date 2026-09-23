import { localDate } from "./document-metadata";
import { useEffect, useState } from "react";
import {
  clusterJSON,
  type BlockDocument,
  type DocumentMetadata,
  type Identity,
} from "./ocr-clusters";

export function TextPreview({
  document,
  metadata,
}: {
  document: BlockDocument;
  metadata?: DocumentMetadata;
}) {
  const [mode, setMode] = useState<"formatted" | "json" | "fields">(
    "formatted",
  );
  const [downloadURL, setDownloadURL] = useState("");
  const json =
    mode === "fields"
      ? JSON.stringify(
          metadata ?? document.unified?.normalized ?? null,
          null,
          2,
        )
      : clusterJSON(document.blocks);
  useEffect(() => {
    const url = URL.createObjectURL(
      new Blob([json], { type: "application/json;charset=utf-8" }),
    );
    setDownloadURL(url);
    return () => URL.revokeObjectURL(url);
  }, [json]);
  const pages = [...new Set(document.blocks.map((block) => block.page))];
  return (
    <div className="text-preview">
      <div className="text-view-toolbar">
        <div className="segmented" role="toolbar" aria-label="Text format">
          {(["formatted", "json", "fields"] as const).map((value) => (
            <button
              key={value}
              type="button"
              className={mode === value ? "active" : ""}
              onClick={() => setMode(value)}
            >
              {value === "json"
                ? "JSON"
                : value === "fields"
                  ? "Fields"
                  : "Formatted"}
            </button>
          ))}
        </div>
        <a
          href={downloadURL}
          download={mode === "fields" ? "fields.json" : "blocks.json"}
        >
          Save JSON
        </a>
      </div>
      {mode === "json" ? (
        <pre className="ocr-text">{json}</pre>
      ) : mode === "fields" ? (
        <FieldsView
          metadata={metadata ?? document.unified?.normalized}
          status={document.unified?.extraction_status}
        />
      ) : (
        <div className="formatted-text">
          {pages.map((page) => (
            <article className="text-page" key={page}>
              <span className="text-page-number">Page {page}</span>
              {document.blocks
                .filter((block) => block.page === page)
                .map((block) => (
                  <div
                    key={block.id}
                    className="reading-block"
                    data-block-type={block.type}
                  >
                    {block.type === "subject" ? (
                      <h3>{block.content}</h3>
                    ) : block.representation === "table" ? (
                      <pre className="text-fixed">{block.content}</pre>
                    ) : (
                      <p>{block.content}</p>
                    )}
                  </div>
                ))}
            </article>
          ))}
        </div>
      )}
    </div>
  );
}

function PartyRows({ identity }: { identity: Identity }) {
  return (
    <table className="metadata-table">
      <tbody>
        <tr>
          <th scope="row">Names</th>
          <td>{identity.names.join("\n") || "—"}</td>
        </tr>
        {identity.addresses.map((address, index) => (
          <tr key={address.lines.join("\n")}>
            <th scope="row">
              {index === (identity.primary_address ?? 0)
                ? "Primary address"
                : "Alternative address"}
            </th>
            <td>
              <dl className="postal-components">
                {(
                  [
                    ["Street", address.street_name],
                    ["Number", address.house_number],
                    ["Postal code", address.postal_code],
                    ["City", address.city],
                  ] as const
                ).map(([label, value]) => (
                  <div key={label}>
                    <dt>{label}</dt>
                    <dd>{value || "—"}</dd>
                  </div>
                ))}
              </dl>
              <details>
                <summary>Source address</summary>
                {address.lines.join("\n")}
              </details>
            </td>
          </tr>
        ))}
        {(["phones", "faxes", "emails", "websites"] as const).map((field) => (
          <tr key={field}>
            <th scope="row">
              {
                {
                  phones: "Phone",
                  faxes: "Fax",
                  emails: "Email",
                  websites: "Website",
                }[field]
              }
            </th>
            <td>{identity[field].join("\n") || "—"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
function FieldsView({
  metadata,
  status,
}: {
  metadata?: DocumentMetadata;
  status?: string;
}) {
  if (!metadata)
    return (
      <p className="text-layout-note">
        Structured fields are available after processing this document.
      </p>
    );
  return (
    <div className="structured-fields">
      {status && status !== "complete" && (
        <p className="text-layout-note">
          Some fields could not be confidently extracted. Check the source
          blocks for missing or uncertain values.
        </p>
      )}
      <table className="metadata-table">
        <tbody>
          <tr>
            <th scope="row">Date</th>
            <td>{localDate(metadata.date) || "—"}</td>
          </tr>
          <tr>
            <th scope="row">Subject</th>
            <td>{metadata.subject || "—"}</td>
          </tr>
        </tbody>
      </table>
      <section>
        <h3>Sender</h3>
        <PartyRows identity={metadata.sender} />
      </section>
      <section>
        <h3>Recipient</h3>
        <PartyRows identity={metadata.recipient} />
      </section>
      {(metadata.fields ?? [])
        .filter((g) => g.type !== "text")
        .map((group) => (
          <details
            className="consolidated-field"
            open={group.type === "reference" || group.type === "payment"}
            key={group.type}
          >
            <summary>
              {(
                {
                  reference: "References",
                  payment: "Payment",
                  sender: "Sender source blocks",
                  recipient: "Recipient source blocks",
                  date: "Date source blocks",
                  subject: "Subject source blocks",
                } as Record<string, string>
              )[group.type] ?? group.type}
            </summary>
            <table className="metadata-table">
              <tbody>
                {group.entries.map((entry) => (
                  <tr key={entry.source_block_ids.join(",")}>
                    <th scope="row">
                      Blocks {entry.source_block_ids.join(", ")}
                    </th>
                    <td>{entry.content}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </details>
        ))}
    </div>
  );
}
