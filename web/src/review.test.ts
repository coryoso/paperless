import { expect, test } from "bun:test";
import { replaceFilenameDocumentType, usesOtherDirectory } from "./review";

test("updates a document type embedded in a review filename", () => {
  expect(replaceFilenameDocumentType("2025-06-07__total__tax-letter__fuel.pdf", "tax-letter", "receipt"))
    .toBe("2025-06-07__total__receipt__fuel.pdf");
});

test("updates a trailing document type", () => {
  expect(replaceFilenameDocumentType("2025-06-07__total__receipt.pdf", "receipt", "routine-invoice"))
    .toBe("2025-06-07__total__routine-invoice.pdf");
});

test("uses the regular folder choice for a known archive folder", () => {
  expect(usesOtherDirectory("Tax/2026", ["Tax/2026", "Insurance"])).toBe(false);
});

test("uses the other directory field for a new relative folder", () => {
  expect(usesOtherDirectory("Family/School", ["Tax/2026", "Insurance"])).toBe(true);
});

test("does not treat an empty destination as another directory", () => {
  expect(usesOtherDirectory("", ["Tax/2026"])).toBe(false);
});

test("folder navigation exposes immediate children, including implicit parents", async () => {
  const { folderChildren, changeFolderPart } = await import("./review");
  const folders = ["Tax/2026/Letters", "Tax/2026/Invoices", "Tax/2025", "Family/School"];
  expect(folderChildren(folders, "")).toEqual(["Family", "Tax"]);
  expect(folderChildren(folders, "Tax")).toEqual(["2025", "2026"]);
  expect(folderChildren(folders, "Tax/2026")).toEqual(["Invoices", "Letters"]);
  expect(changeFolderPart("Tax/2026/Letters", 0, "Family")).toBe("Family");
  expect(changeFolderPart("Tax/2026/Letters", 1, "2025")).toBe("Tax/2025");
  expect(changeFolderPart("Tax/2026/Letters", 2, "")).toBe("Tax/2026");
  expect(usesOtherDirectory("Family", folders)).toBe(false);
});

test("saved recipients and aliases are preferred within the detected capacity", async () => {
  const { savedRecipientChoice, learnedAlias } = await import("./review");
  const profiles = [
    { id: 1, name: "Alex Example", scope: "personal", aliases: ["A. Example"], folder_prefix: "" },
    { id: 2, name: "Alex Example", scope: "sole_proprietor", aliases: [], folder_prefix: "Business" },
  ];
  const c = { recipient: "a-example", recipient_scope: "personal" } as import("./types").Classification;
  expect(savedRecipientChoice(c, profiles)).toBe("1");
  expect(savedRecipientChoice({ ...c, recipient_scope: "unknown" }, profiles)).toBe("");
  expect(savedRecipientChoice({ ...c, recipient: "Someone else" }, profiles)).toBe("");
  expect(savedRecipientChoice(c, [])).toBe("new");
  expect(learnedAlias("A. Example", profiles[0])).toBe("");
  expect(learnedAlias("Alex Examp1e", profiles[0])).toBe("Alex Examp1e");
  expect(learnedAlias("unknown", profiles[0])).toBe("");
});
