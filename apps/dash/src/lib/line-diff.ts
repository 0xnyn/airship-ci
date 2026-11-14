/** One line of a diff: kept, added or removed, with its line numbers. */
export interface DiffLine {
  after?: number;
  before?: number;
  kind: "add" | "keep" | "remove";
  text: string;
}

/** A run of diff lines to show, or a count of unchanged lines folded away. */
export type DiffHunk =
  | { kind: "lines"; lines: DiffLine[] }
  | { count: number; kind: "fold" };

// Past this many cells the LCS table costs more than a preview is worth;
// such files show as replaced wholesale.
const MAX_CELLS = 4_000_000;

function splitLines(text: string): string[] {
  if (text === "") {
    return [];
  }
  const lines = text.split("\n");
  if (lines.at(-1) === "") {
    lines.pop();
  }
  return lines;
}

function lcsTable(a: string[], b: string[]): Uint32Array[] {
  const table = Array.from(
    { length: a.length + 1 },
    () => new Uint32Array(b.length + 1)
  );
  for (let i = a.length - 1; i >= 0; i -= 1) {
    const row = table[i] ?? new Uint32Array(0);
    const below = table[i + 1] ?? new Uint32Array(0);
    for (let j = b.length - 1; j >= 0; j -= 1) {
      row[j] =
        a[i] === b[j]
          ? (below[j + 1] ?? 0) + 1
          : Math.max(below[j] ?? 0, row[j + 1] ?? 0);
    }
  }
  return table;
}

function cell(table: Uint32Array[], i: number, j: number): number {
  return table[i]?.[j] ?? 0;
}

/** A line diff of two texts, by longest common subsequence. */
export function diffLines(before: string, after: string): DiffLine[] {
  const a = splitLines(before);
  const b = splitLines(after);
  const out: DiffLine[] = [];
  if (a.length * b.length > MAX_CELLS) {
    for (const [index, text] of a.entries()) {
      out.push({ before: index + 1, kind: "remove", text });
    }
    for (const [index, text] of b.entries()) {
      out.push({ after: index + 1, kind: "add", text });
    }
    return out;
  }
  const table = lcsTable(a, b);
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      out.push({ after: j + 1, before: i + 1, kind: "keep", text: a[i] ?? "" });
      i += 1;
      j += 1;
    } else if (
      i < a.length &&
      (j >= b.length || cell(table, i + 1, j) >= cell(table, i, j + 1))
    ) {
      out.push({ before: i + 1, kind: "remove", text: a[i] ?? "" });
      i += 1;
    } else {
      out.push({ after: j + 1, kind: "add", text: b[j] ?? "" });
      j += 1;
    }
  }
  return out;
}

/** Changes with `context` kept lines around each; longer runs fold. */
export function hunks(lines: DiffLine[], context = 3): DiffHunk[] {
  const near = new Uint8Array(lines.length);
  lines.forEach((line, index) => {
    if (line.kind === "keep") {
      return;
    }
    const from = Math.max(0, index - context);
    const to = Math.min(lines.length - 1, index + context);
    for (let k = from; k <= to; k += 1) {
      near[k] = 1;
    }
  });
  const out: DiffHunk[] = [];
  let folded = 0;
  for (const [index, line] of lines.entries()) {
    if (near[index]) {
      if (folded > 0) {
        out.push({ count: folded, kind: "fold" });
        folded = 0;
      }
      const last = out.at(-1);
      if (last?.kind === "lines") {
        last.lines.push(line);
      } else {
        out.push({ kind: "lines", lines: [line] });
      }
    } else {
      folded += 1;
    }
  }
  if (folded > 0) {
    out.push({ count: folded, kind: "fold" });
  }
  return out;
}
