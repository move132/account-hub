export interface RuntimeLogEntry {
  id: string;
  time: number;
  level: string;
  message: string;
  fields: string;
}

export interface RuntimeLogBatch {
  items: RuntimeLogEntry[];
  reset: boolean;
  truncated: boolean;
}

export interface RuntimeLogView {
  items: RuntimeLogEntry[];
  truncated: boolean;
}

export const maxLogEntries = 1000;
const maxViewBytes = 4 << 20;

export function parseLogBatch(data: string): RuntimeLogBatch {
  const batch = JSON.parse(data) as RuntimeLogBatch;
  if (!batch || !Array.isArray(batch.items) || typeof batch.reset !== "boolean" || typeof batch.truncated !== "boolean"
    || batch.items.some((entry) => !entry || typeof entry.id !== "string" || !Number.isFinite(entry.time)
      || typeof entry.level !== "string" || typeof entry.message !== "string" || typeof entry.fields !== "string")) {
    throw new Error("无效的日志数据");
  }
  return batch;
}

export function mergeLogBatch(current: RuntimeLogView, batch: RuntimeLogBatch): RuntimeLogView {
  const previous = batch.reset ? [] : current.items;
  const seen = new Set(previous.map((entry) => entry.id));
  const incoming = batch.items.filter((entry) => {
    if (seen.has(entry.id)) return false;
    seen.add(entry.id);
    return true;
  });
  const items = [...previous, ...incoming];
  let start = items.length;
  let bytes = 0;
  while (start > 0 && items.length - start < maxLogEntries) {
    const entry = items[start - 1];
    const size = (entry.id.length + entry.message.length + entry.fields.length) * 2 + 128;
    if (bytes + size > maxViewBytes) break;
    bytes += size;
    start--;
  }
  return {
    items: items.slice(start),
    truncated: batch.truncated || start > 0 || (!batch.reset && current.truncated),
  };
}

export function matchesLog(entry: RuntimeLogEntry, level: string, query: string) {
  if (level !== "all" && entry.level !== level) return false;
  const needle = query.trim().toLowerCase();
  return !needle || (entry.message + " " + entry.fields).toLowerCase().includes(needle);
}
