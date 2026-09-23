import type { OCRPage } from "./types";

type Word = OCRPage["boxes"][number];
export const blockTypes = [
  "text",
  "sender",
  "recipient",
  "date",
  "subject",
  "reference",
  "payment",
] as const;
export type BlockType = (typeof blockTypes)[number];
export type BlockLabel = {
  id: number;
  type: BlockType;
  representation: "paragraph" | "table";
};
export type ClusterBlock = {
  id: number;
  type: BlockType | "";
  representation: "paragraph" | "table" | "";
  page: number;
  position: { x: number; y: number } | null;
  size: { width: number; height: number } | null;
  content: string;
  table_candidate?: boolean;
};
export type Party = {
  names: string[];
  addresses: string[];
  phones: string[];
  faxes: string[];
  emails: string[];
  websites: string[];
  source_block_ids: number[];
};
export type PostalAddress = {
  lines: string[];
  street_name?: string;
  house_number?: string;
  postal_code?: string;
  city?: string;
};
export type Identity = Omit<Party, "addresses"> & {
  addresses: PostalAddress[];
  primary_address?: number;
  address_selection?: string;
  origin: string;
  profile_id?: number;
};
export type DocumentMetadata = {
  fields?: UnifiedDocument["groups"];
  version: number;
  date: string;
  subject: string;
  sender: Identity;
  recipient: Identity;
};
export type UnifiedDocument = {
  normalized?: DocumentMetadata;
  date: string;
  date_source_ids: number[];
  version: number;
  extraction_status: string;
  subject: string;
  subject_source_ids: number[];
  sender: Party;
  recipient: Party;
  groups: {
    type: string;
    entries: { content: string; source_block_ids: number[] }[];
  }[];
};
export type BlockDocument = {
  unified?: UnifiedDocument;
  version: number;
  source_hash: string;
  distance_multiplier: number;
  blocks: ClusterBlock[];
};
export type OCRCluster = {
  left: number;
  top: number;
  width: number;
  height: number;
  content: string;
  tableCandidate: boolean;
};
export type ClusterPage = {
  page: number;
  baseDistance: number;
  distance: number;
  clusters: OCRCluster[];
};

function readingOrder(a: Word, b: Word) {
  return a.top - b.top || a.left - b.left || a.text.localeCompare(b.text);
}

function orderedLines(words: Word[]) {
  const lines: { center: number; height: number; words: Word[] }[] = [];
  for (const word of [...words].sort(readingOrder)) {
    const center = word.top + word.height / 2;
    // Match by vertical centers, allowing small OCR baseline/height variation.
    const line = lines.find(
      (line) =>
        Math.abs(line.center - center) <=
        Math.min(line.height, word.height) / 2,
    );
    if (line) line.words.push(word);
    else lines.push({ center, height: word.height, words: [word] });
  }
  return lines.map((line) =>
    line.words.sort((a, b) => a.left - b.left || readingOrder(a, b)),
  );
}

function contentInReadingOrder(words: Word[]) {
  return orderedLines(words)
    .map((line) => line.map((word) => word.text).join(" "))
    .join("\n");
}

function hasTableAlignment(words: Word[], height: number) {
  const rows = orderedLines(words)
    .map((line) => {
      const starts = [line[0].left];
      line.slice(1).forEach((word, i) => {
        if (word.left - (line[i].left + line[i].width) > height * 1.5)
          starts.push(word.left);
      });
      return starts;
    })
    .filter((starts) => starts.length > 1);
  // At least two rows with aligned cells; multiline prose/addresses alone aren't tables.
  return rows.some((row, i) =>
    rows
      .slice(i + 1)
      .some(
        (other) =>
          row.length === other.length &&
          row.every((x, column) => Math.abs(x - other[column]) <= height / 2),
      ),
  );
}

// Connected components of word rectangles, using their Euclidean edge gap.
// Median word height gives a robust, resolution-dependent distance per page.
// Compare original rectangles, never growing cluster bounds (which fill gaps).
export function clusterOCRPage(page: OCRPage, multiplier = 1): ClusterPage {
  const words = page.boxes
    .filter(
      (word) =>
        word.text.trim() &&
        [word.left, word.top, word.width, word.height].every(Number.isFinite) &&
        word.width > 0 &&
        word.height > 0,
    )
    .sort(readingOrder);
  const heights = words.map((word) => word.height).sort((a, b) => a - b);
  const middle = Math.floor(heights.length / 2);
  const baseDistance = heights.length
    ? (heights[middle] + heights[Math.floor((heights.length - 1) / 2)]) / 2
    : 0;
  const distance =
    baseDistance * (Number.isFinite(multiplier) ? Math.max(0, multiplier) : 1);
  const parents = words.map((_, i) => i);
  const root = (index: number): number => {
    while (parents[index] !== index) {
      parents[index] = parents[parents[index]];
      index = parents[index];
    }
    return index;
  };
  for (let i = 0; i < words.length; i++) {
    const a = words[i];
    for (let j = i + 1; j < words.length; j++) {
      const b = words[j];
      // Sorted tops bound comparisons to a nearby vertical band.
      if (b.top > a.top + a.height + distance) break;
      const dx = Math.max(
        0,
        a.left - b.left - b.width,
        b.left - a.left - a.width,
      );
      const dy = Math.max(
        0,
        a.top - b.top - b.height,
        b.top - a.top - a.height,
      );
      if (dx * dx + dy * dy <= distance * distance) parents[root(j)] = root(i);
    }
  }
  const groups = new Map<number, Word[]>();
  words.forEach((word, index) => {
    const key = root(index);
    const group = groups.get(key) ?? [];
    group.push(word);
    groups.set(key, group);
  });
  const clusters = [...groups.values()].map((group) => {
    const left = Math.min(...group.map((word) => word.left));
    const top = Math.min(...group.map((word) => word.top));
    return {
      left,
      top,
      width: Math.max(...group.map((word) => word.left + word.width)) - left,
      height: Math.max(...group.map((word) => word.top + word.height)) - top,
      content: contentInReadingOrder(group),
      tableCandidate: hasTableAlignment(group, baseDistance),
    };
  });
  clusters.sort((a, b) => a.top - b.top || a.left - b.left);
  return { page: page.page, baseDistance, distance, clusters };
}

export function clusterBlocks(pages: ClusterPage[]): ClusterBlock[] {
  return [...pages]
    .sort((a, b) => a.page - b.page)
    .flatMap((page) =>
      page.clusters.map((cluster) => ({
        id: 0,
        type: "" as const,
        representation: "" as const,
        page: page.page,
        position: { x: cluster.left, y: cluster.top },
        size: { width: cluster.width, height: cluster.height },
        content: cluster.content,
        table_candidate: cluster.tableCandidate,
      })),
    )
    .map((block, index) => ({ ...block, id: index + 1 }));
}

export function applyBlockLabels(
  blocks: ClusterBlock[],
  labels: BlockLabel[],
): ClusterBlock[] {
  const byID = new Map(labels.map((label) => [label.id, label]));
  if (
    labels.length !== blocks.length ||
    byID.size !== blocks.length ||
    blocks.some((block) => {
      const label = byID.get(block.id);
      return (
        !label ||
        !blockTypes.includes(label.type) ||
        !["paragraph", "table"].includes(label.representation)
      );
    })
  )
    throw new Error(
      "The model returned incomplete or invalid block labels. Try again.",
    );
  return blocks.map((block) => {
    const label = byID.get(block.id);
    if (!label) throw new Error("Missing block label.");
    return { ...block, type: label.type, representation: label.representation };
  });
}

export function clusterJSON(blocks: ClusterBlock[]) {
  return JSON.stringify(
    blocks.map(
      ({ id, type, representation, page, position, size, content }) => ({
        id,
        type,
        representation,
        page,
        position,
        size,
        content,
      }),
    ),
    null,
    2,
  );
}
