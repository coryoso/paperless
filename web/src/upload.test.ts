import { describe, expect, test } from "bun:test";
import { formatFileSize, isSupportedDocument, pickSupportedDocument } from "./upload";

describe("upload document selection", () => {
  test("accepts supported MIME types", () => {
    expect(isSupportedDocument({ name: "letter", type: "application/pdf" })).toBe(true);
    expect(isSupportedDocument({ name: "scan", type: "image/png" })).toBe(true);
    expect(isSupportedDocument({ name: "receipt", type: "image/jpeg" })).toBe(true);
  });

  test("accepts supported extensions when the browser provides no MIME type", () => {
    expect(isSupportedDocument({ name: "Letter.PDF", type: "" })).toBe(true);
    expect(isSupportedDocument({ name: "receipt.JPEG", type: "" })).toBe(true);
  });

  test("rejects unrelated files", () => {
    expect(isSupportedDocument({ name: "notes.txt", type: "text/plain" })).toBe(false);
  });

  test("picks the first supported document from a multi-file drop", () => {
    const files = [
      { name: "notes.txt", type: "text/plain" },
      { name: "invoice.pdf", type: "application/pdf" },
      { name: "receipt.jpg", type: "image/jpeg" },
    ];
    expect(pickSupportedDocument(files)).toEqual(files[1]);
  });
});

test("formats compact file sizes", () => {
  expect(formatFileSize(800)).toBe("800 B");
  expect(formatFileSize(2048)).toBe("2 KB");
  expect(formatFileSize(1572864)).toBe("1.5 MB");
});
