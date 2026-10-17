import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { useTheme } from "../../app/theme-provider";
import { UPDATE_INTERVAL, chartDomain, continuousSegments, countTicks, nearestPoint, type Provider, type Snapshot } from "./trend";

const series = [
  { key: "total", label: "账号总数", color: "--info", className: "bg-app-info", width: 3.5 },
  { key: "enabled", label: "启用", color: "--success", className: "bg-app-success", width: 2 },
  { key: "disabled", label: "禁用", color: "--muted-foreground", className: "bg-app-subtle", width: 1.8 },
] as const;
const timestampFormatter = new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", timeZone: "Asia/Shanghai" });
const hourFormatter = new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "Asia/Shanghai" });
const dayFormatter = new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", timeZone: "Asia/Shanghai" });
const margin = { left: 44, right: 18, top: 22, bottom: 34 };

export function TrendChart({ samples, provider }: { samples: Snapshot[]; provider: Provider }) {
  const { mode } = useTheme();
  const canvas = useRef<HTMLCanvasElement>(null);
  const descriptionId = useId();
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [active, setActive] = useState<number | null>(null);
  const points = useMemo(() => samples.map((point) => ({ at: point.at, ...point[provider] })), [samples, provider]);
  const domain = useMemo(() => chartDomain(points), [points]);
  const ticks = useMemo(() => countTicks(Math.max(0, ...points.map((point) => point.total))), [points]);
  const segments = useMemo(() => continuousSegments(points, UPDATE_INTERVAL), [points]);
  const activeIndex = active === null || points.length === 0 ? null : Math.min(active, points.length - 1);
  const selected = activeIndex === null ? null : points[activeIndex];
  const plotWidth = Math.max(1, size.width - margin.left - margin.right);
  const plotHeight = Math.max(1, size.height - margin.top - margin.bottom);
  const x = (at: number) => margin.left + ((at - domain.from) / Math.max(1, domain.to - domain.from)) * plotWidth;

  useEffect(() => {
    const element = canvas.current;
    if (!element) return;
    const observer = new ResizeObserver(() => setSize({ width: element.clientWidth, height: element.clientHeight }));
    observer.observe(element);
    setSize({ width: element.clientWidth, height: element.clientHeight });
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const element = canvas.current;
      const ctx = element?.getContext("2d");
      if (!element || !ctx || size.width === 0 || size.height === 0) return;
      const scale = window.devicePixelRatio || 1;
      element.width = Math.round(size.width * scale);
      element.height = Math.round(size.height * scale);
      ctx.setTransform(scale, 0, 0, scale, 0, 0);
      const styles = getComputedStyle(document.documentElement);
      const color = (name: string) => styles.getPropertyValue(name).trim();
      const xAt = (at: number) => margin.left + ((at - domain.from) / Math.max(1, domain.to - domain.from)) * plotWidth;
      const yAt = (value: number) => margin.top + plotHeight * (1 - value / ticks[ticks.length - 1]);
      ctx.font = "11px system-ui, sans-serif";
      ctx.textBaseline = "middle";
      for (const tick of ticks) {
        const y = yAt(tick);
        ctx.strokeStyle = color("--border");
        ctx.globalAlpha = 0.6;
        ctx.lineWidth = 1;
        ctx.beginPath(); ctx.moveTo(margin.left, y); ctx.lineTo(size.width - margin.right, y); ctx.stroke();
        ctx.globalAlpha = 1;
        ctx.fillStyle = color("--muted-foreground");
        ctx.textAlign = "right";
        ctx.fillText(tick.toLocaleString("zh-CN"), margin.left - 10, y);
      }
      const divisions = size.width < 500 ? 3 : 5;
      for (let index = 0; index <= divisions; index++) {
        const at = domain.from + (domain.to - domain.from) * index / divisions;
        ctx.fillStyle = color("--muted-foreground");
        ctx.textAlign = index === 0 ? "left" : index === divisions ? "right" : "center";
        ctx.fillText((domain.to - domain.from <= 36 * 3_600_000 ? hourFormatter : dayFormatter).format(at), xAt(at), size.height - 12);
      }

      ctx.save();
      ctx.beginPath(); ctx.rect(margin.left - 5, margin.top - 6, plotWidth + 10, plotHeight + 12); ctx.clip();
      for (const segment of segments) {
        if (segment.length < 2) continue;
        const gradient = ctx.createLinearGradient(0, margin.top, 0, size.height - margin.bottom);
        gradient.addColorStop(0, color("--info")); gradient.addColorStop(1, color("--surface"));
        ctx.fillStyle = gradient;
        ctx.globalAlpha = mode === "dark" ? 0.12 : 0.07;
        ctx.beginPath(); ctx.moveTo(xAt(segment[0].at), yAt(0));
        for (const point of segment) ctx.lineTo(xAt(point.at), yAt(point.total));
        ctx.lineTo(xAt(segment[segment.length - 1].at), yAt(0)); ctx.closePath(); ctx.fill();
        ctx.globalAlpha = 1;
      }
      for (const item of series) {
        ctx.strokeStyle = color(item.color);
        ctx.fillStyle = color(item.color);
        ctx.lineWidth = item.width;
        ctx.lineJoin = "round"; ctx.lineCap = "round";
        ctx.setLineDash(item.key === "disabled" ? [4, 4] : []);
        for (const segment of segments) {
          ctx.beginPath();
          segment.forEach((point, index) => index === 0 ? ctx.moveTo(xAt(point.at), yAt(point[item.key])) : ctx.lineTo(xAt(point.at), yAt(point[item.key])));
          ctx.stroke();
          if (segment.length === 1) { ctx.beginPath(); ctx.arc(xAt(segment[0].at), yAt(segment[0][item.key]), 3, 0, Math.PI * 2); ctx.fill(); }
        }
      }
      ctx.setLineDash([]);
      if (selected) {
        ctx.strokeStyle = color("--muted-foreground"); ctx.globalAlpha = 0.5; ctx.lineWidth = 1; ctx.setLineDash([3, 4]);
        ctx.beginPath(); ctx.moveTo(xAt(selected.at), margin.top); ctx.lineTo(xAt(selected.at), size.height - margin.bottom); ctx.stroke();
        ctx.globalAlpha = 1; ctx.setLineDash([]);
      }
      const highlight = selected || points[points.length - 1];
      if (highlight) {
        for (const item of series) {
          ctx.fillStyle = color(item.color); ctx.strokeStyle = color("--surface"); ctx.lineWidth = 2;
          ctx.beginPath(); ctx.arc(xAt(highlight.at), yAt(highlight[item.key]), selected ? 4.5 : 3, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        }
      }
      ctx.restore();
    });
    return () => cancelAnimationFrame(frame);
  }, [domain, mode, plotHeight, plotWidth, points, segments, selected, size, ticks]);

  const move = (event: PointerEvent<HTMLCanvasElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    const position = Math.max(0, Math.min(1, (event.clientX - rect.left - margin.left) / plotWidth));
    const index = nearestPoint(points, domain.from + position * (domain.to - domain.from));
    setActive(index < 0 ? null : index);
  };
  const keyDown = (event: KeyboardEvent<HTMLCanvasElement>) => {
    if (points.length === 0) return;
    const current = activeIndex ?? points.length - 1;
    if (event.key === "ArrowLeft") setActive(Math.max(0, current - 1));
    else if (event.key === "ArrowRight") setActive(Math.min(points.length - 1, current + 1));
    else if (event.key === "Home") setActive(0);
    else if (event.key === "End") setActive(points.length - 1);
    else if (event.key === "Escape") setActive(null);
    else return;
    event.preventDefault();
  };
  const latest = points[points.length - 1];
  return <div>
    <div className="mb-2 flex flex-wrap items-center gap-5 text-xs text-app-subtle">
      {series.map((item) => <span key={item.key} className="inline-flex items-center gap-2"><span aria-hidden="true" className={`h-0.5 w-4 rounded-full ${item.className}`} />{item.label}</span>)}
    </div>
    <div className="relative">
      <canvas ref={canvas} role="img" aria-label={`${provider === "tokens" ? "GPT" : "即梦"}账号实时数量折线图`} aria-describedby={descriptionId} tabIndex={0} className="block h-60 w-full rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-app-ring/40 sm:h-72" onPointerMove={move} onPointerLeave={() => { if (document.activeElement !== canvas.current) setActive(null); }} onFocus={() => setActive(points.length ? points.length - 1 : null)} onBlur={() => setActive(null)} onKeyDown={keyDown} />
      {selected && <div role="status" className="pointer-events-none absolute top-2 z-10 w-44 rounded-lg border border-app-border bg-app-surface/95 p-3 text-xs shadow-lg backdrop-blur" style={{ left: Math.max(0, Math.min(size.width - 176, x(selected.at) + (x(selected.at) > size.width / 2 ? -190 : 14))) }}>
        <p className="mb-2 text-[11px] text-app-subtle">{timestampFormatter.format(selected.at)}</p>
        {series.map((item) => <div key={item.key} className="mt-1.5 flex items-center justify-between gap-3"><span className="inline-flex items-center gap-1.5 text-app-subtle"><span aria-hidden="true" className={`size-1.5 rounded-full ${item.className}`} />{item.label}</span><strong className="font-semibold tabular-nums">{selected[item.key].toLocaleString("zh-CN")}</strong></div>)}
      </div>}
    </div>
    <p id={descriptionId} className="sr-only">{latest ? `最近记录：${timestampFormatter.format(latest.at)}，总数 ${latest.total}，启用 ${latest.enabled}，禁用 ${latest.disabled}。` : "暂无记录。"}可使用左右方向键查看记录，Home 和 End 跳到首尾记录。</p>
    <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[11px] text-app-subtle">
      <span>北京时间</span><span>{points.length > 1 ? `当前显示自 ${timestampFormatter.format(points[0].at)}` : "首个记录点，下一次更新后形成折线"}</span>
    </div>
  </div>;
}
