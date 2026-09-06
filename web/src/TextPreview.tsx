import { useEffect, useState } from "react";
import type { TextBlock, TextLayout } from "./types";

export function TextPreview({ layout, raw }: { layout: TextLayout; raw: string }) {
  const [mode, setMode] = useState<"formatted" | "markdown" | "raw">("formatted");
  const [downloadURL, setDownloadURL] = useState("");
  useEffect(() => {
    const url = URL.createObjectURL(new Blob([layout.markdown], { type: "text/markdown;charset=utf-8" }));
    setDownloadURL(url);
    return () => URL.revokeObjectURL(url);
  }, [layout.markdown]);
  return <div className="text-preview">
    <div className="text-view-toolbar"><div className="segmented" aria-label="Text format">
      <button className={mode === "formatted" ? "active" : ""} onClick={() => setMode("formatted")}>Formatted</button>
      <button className={mode === "markdown" ? "active" : ""} onClick={() => setMode("markdown")}>Markdown</button>
      <button className={mode === "raw" ? "active" : ""} onClick={() => setMode("raw")}>Raw text</button>
    </div><a href={downloadURL} download="document.md">Save Markdown</a></div>
    <p className="text-layout-note">Layout reconstructed from the document. Check the scan for uncertain words or table alignment.</p>
    {mode === "formatted" ? <div className="formatted-text">{layout.pages.map((page) => <article className="text-page" key={page.page}><span className="text-page-number">Page {page.page}</span><TextBlocks blocks={page.blocks} /></article>)}</div> : <pre className="ocr-text">{mode === "markdown" ? layout.markdown || "No text detected." : raw || "No text detected."}</pre>}
  </div>;
}

function TextBlocks({ blocks }: { blocks: TextBlock[] }) {
  return <>{blocks.map((block, i) => {
    if (block.kind === "columns") return <div className="text-columns" key={i}>{block.columns?.map((column, j) => <div key={j}><TextBlocks blocks={column} /></div>)}</div>;
    if (block.kind === "table") return <div className="text-table-wrap" key={i}><table><tbody>{block.rows?.map((row, r) => <tr key={r}>{row.map((cell, c) => <td key={c}>{cell}</td>)}</tr>)}</tbody></table></div>;
    if (block.kind === "heading") return <h3 key={i}>{block.text}</h3>;
    if (block.kind === "pre") return <pre className="text-fixed" key={i}>{block.text || "No text detected."}</pre>;
    return <p key={i}>{block.text}</p>;
  })}</>;
}
