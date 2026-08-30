import { expect, test } from "bun:test";
import { replaceFilenameDocumentType } from "./review";

test("updates a document type embedded in a review filename", () => {
  expect(replaceFilenameDocumentType("2025-06-07__total__tax-letter__fuel.pdf", "tax-letter", "receipt"))
    .toBe("2025-06-07__total__receipt__fuel.pdf");
});

test("updates a trailing document type", () => {
  expect(replaceFilenameDocumentType("2025-06-07__total__receipt.pdf", "receipt", "routine-invoice"))
    .toBe("2025-06-07__total__routine-invoice.pdf");
});
