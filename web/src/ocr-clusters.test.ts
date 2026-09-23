import { expect, test } from "bun:test";
import {
  clusterJSON,
  clusterOCRPage,
  clusterBlocks,
  applyBlockLabels,
} from "./ocr-clusters";
import type { OCRPage } from "./types";

function word(
  text: string,
  left: number,
  top: number,
  width = 30,
  height = 20,
) {
  return { text, left, top, width, height, confidence: 90 };
}
function page(boxes: OCRPage["boxes"], number = 1): OCRPage {
  return { page: number, width: 1000, height: 1400, image_url: "", boxes };
}

test("groups a multiline address separately from a distant invoice body", () => {
  const result = clusterOCRPage(
    page([
      word("Berlin", 50, 40),
      word("Invoice", 10, 180),
      word("Example", 50, 10),
      word("Alex", 10, 10),
      word("12345", 10, 40),
    ]),
  );
  expect(result.clusters).toMatchObject([
    {
      left: 10,
      top: 10,
      width: 70,
      height: 50,
      content: "Alex Example\n12345 Berlin",
    },
    { left: 10, top: 180, width: 30, height: 20, content: "Invoice" },
  ]);
});

test("scales distances with OCR resolution without changing group contents", () => {
  const original = page([
    word("one", 10, 10),
    word("two", 50, 10),
    word("three", 110, 10),
  ]);
  const enlarged = page(
    original.boxes.map((box) => ({
      ...box,
      left: box.left * 3,
      top: box.top * 3,
      width: box.width * 3,
      height: box.height * 3,
    })),
  );
  const a = clusterOCRPage(original);
  const b = clusterOCRPage(enlarged);
  expect(b.distance).toBe(a.distance * 3);
  expect(a.clusters.map((c) => c.content)).toEqual(
    b.clusters.map((c) => c.content),
  );
  expect(a.clusters).toHaveLength(2);
});

test("slider threshold merges connected neighbors and can split them again", () => {
  const input = page([word("A", 0, 0), word("B", 45, 0), word("C", 90, 0)]);
  expect(clusterOCRPage(input, 0.5).clusters).toHaveLength(3);
  expect(clusterOCRPage(input, 1).clusters.map((c) => c.content)).toEqual([
    "A B C",
  ]);
  expect(clusterOCRPage(input, 0).clusters).toHaveLength(3);
});

test("uses Euclidean edge distance for diagonal neighbors", () => {
  const input = page([word("A", 0, 0), word("B", 45, 35)]);
  expect(clusterOCRPage(input, 1).clusters).toHaveLength(2);
  expect(clusterOCRPage(input, 1.1).clusters).toHaveLength(1);
});

test("exports geometry and exact content, preserving page boundaries", () => {
  const first = clusterOCRPage(page([word('Grüße "A"', 0, 0)]));
  const second = clusterOCRPage(page([word("第二頁", 0, 0)], 2));
  expect(JSON.parse(clusterJSON(clusterBlocks([second, first])))).toEqual([
    {
      id: 1,
      page: 1,
      type: "",
      representation: "",
      position: { x: 0, y: 0 },
      size: { width: 30, height: 20 },
      content: 'Grüße "A"',
    },
    {
      id: 2,
      page: 2,
      type: "",
      representation: "",
      position: { x: 0, y: 0 },
      size: { width: 30, height: 20 },
      content: "第二頁",
    },
  ]);
});

test("empty or unusable geometry has no clusters or distance", () => {
  const result = clusterOCRPage(
    page([word("", 0, 0), word("bad", 0, 0, 0), word("bad", NaN, 0)]),
  );
  expect(result).toEqual({
    page: 1,
    baseDistance: 0,
    distance: 0,
    clusters: [],
  });
  expect(clusterJSON(clusterBlocks([result]))).toBe("[]");
});

test("large heading does not dominate the automatic distance", () => {
  const result = clusterOCRPage(
    page([
      word("Heading", 0, 0, 100, 80),
      word("Body", 0, 200),
      word("text", 40, 200),
    ]),
  );
  expect(result.baseDistance).toBe(20);
});

test("reading order tolerates small baseline differences", () => {
  const result = clusterOCRPage(
    page([word("right", 40, 10), word("left", 0, 13)]),
  );
  expect(result.clusters[0].content).toBe("left right");
});

test("does not merge a word just because it falls inside another group's bounds", () => {
  const result = clusterOCRPage(
    page([
      word("top", 0, 0),
      word("edge", 35, 0),
      word("right", 70, 0),
      word("left", 0, 35, 10),
      word("bottom", 0, 70, 10),
      word("separate", 60, 60, 10, 10),
    ]),
  );
  expect(result.clusters).toHaveLength(2);
  expect(result.clusters[1].content).toBe("separate");
});

test("only repeated aligned cells hint at a table", () => {
  const table = clusterOCRPage(
    page([
      word("Name", 0, 0),
      word("Value", 70, 0),
      word("Alpha", 0, 30),
      word("42", 70, 30),
    ]),
    3,
  );
  expect(table.clusters[0].tableCandidate).toBe(true);
  const paragraph = clusterOCRPage(
    page([word("Hello", 0, 0), word("world", 40, 0), word("Again", 0, 30)]),
  );
  expect(paragraph.clusters[0].tableCandidate).toBe(false);
});

test("labels merge by ID without changing original geometry or text", () => {
  const blocks = clusterBlocks([
    clusterOCRPage(page([word("Alex", 10, 20), word("Hello", 200, 200)])),
  ]);
  const result = applyBlockLabels(blocks, [
    { id: 2, type: "text", representation: "paragraph" },
    { id: 1, type: "recipient", representation: "paragraph" },
  ]);
  expect(result[0]).toEqual({
    ...blocks[0],
    type: "recipient",
    representation: "paragraph",
  });
  expect(result[1].content).toBe("Hello");
  expect(blocks[0].type).toBe("");
  expect(() =>
    applyBlockLabels(blocks, [
      { id: 1, type: "text", representation: "paragraph" },
    ]),
  ).toThrow();
  expect(() =>
    applyBlockLabels(blocks, [
      { id: 1, type: "text", representation: "paragraph" },
      { id: 1, type: "sender", representation: "paragraph" },
    ]),
  ).toThrow();
});
