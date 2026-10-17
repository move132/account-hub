export type Provider = "tokens" | "dreamina";
export const UPDATE_INTERVAL = 30_000;
export const MAX_SAMPLES = 240;

export interface Counts { total: number; enabled: number; disabled: number }
export interface Snapshot { at: number; tokens: Counts; dreamina: Counts }
export interface ChartPoint extends Counts { at: number }
export interface OverviewData extends Record<string, unknown> {
  tokens: Counts;
  dreamina: Counts;
  as_of: number;
}

// Keep only observations received while this page is mounted. No browser or
// server storage is used, and equal timestamps are never duplicated.
export function appendSample(samples: Snapshot[], data: OverviewData): Snapshot[] {
  const point = { at: data.as_of, tokens: data.tokens, dreamina: data.dreamina };
  const last = samples[samples.length - 1];
  if (last && point.at < last.at) return [point];
  const previous = last?.at === point.at ? samples.slice(0, -1) : samples;
  return [...previous, point].slice(-MAX_SAMPLES);
}

export function countTicks(maximum: number) {
  if (maximum <= 1) return [0, 1];
  const raw = maximum / 4;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  const fraction = raw / magnitude;
  const step = Math.max(1, (fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 5 ? 5 : 10) * magnitude);
  const top = Math.ceil(maximum / step) * step;
  return Array.from({ length: Math.round(top / step) + 1 }, (_, index) => index * step);
}

export function chartDomain(points: { at: number }[]) {
  const to = points[points.length - 1]?.at ?? 0;
  return { from: Math.min(points[0]?.at ?? to, to - 300_000), to };
}

export function nearestPoint(points: ChartPoint[], at: number) {
  if (points.length === 0) return -1;
  let low = 0, high = points.length - 1;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    if (points[middle].at < at) low = middle + 1;
    else high = middle;
  }
  return low > 0 && at - points[low - 1].at < points[low].at - at ? low - 1 : low;
}

export function continuousSegments(points: ChartPoint[], interval: number) {
  const segments: ChartPoint[][] = [];
  for (const point of points) {
    const current = segments[segments.length - 1];
    if (!current || point.at - current[current.length - 1].at > interval * 2) segments.push([point]);
    else current.push(point);
  }
  return segments;
}
