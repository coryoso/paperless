import { test, expect } from "bun:test";
import { parseAddress, localDate } from "./document-metadata";
test("postal components keep leading zeros and house number suffixes", () => {
  expect(parseAddress("Example Road 12a\n01234 Example City")).toEqual({
    lines: ["Example Road 12a", "01234 Example City"],
    street_name: "Example Road",
    house_number: "12a",
    postal_code: "01234",
    city: "Example City",
  });
});
test("date-only values use local dates without timezone shifts", () => {
  expect(localDate("2026-08-20")).toBe(
    new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(
      new Date(2026, 7, 20),
    ),
  );
  expect(localDate("")).toBe("");
});
