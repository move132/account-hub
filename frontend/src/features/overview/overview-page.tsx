import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import ArrowUpRight from "lucide-react/dist/esm/icons/arrow-up-right.mjs";
import KeyRound from "lucide-react/dist/esm/icons/key-round.mjs";
import RefreshCw from "lucide-react/dist/esm/icons/refresh-cw.mjs";
import UsersRound from "lucide-react/dist/esm/icons/users-round.mjs";
import Activity from "lucide-react/dist/esm/icons/activity.mjs";
import { Button, Card, Spinner, cn } from "../../components/ui";
import { api, errorMessage } from "../../lib/api";
import { UPDATE_INTERVAL, appendSample, type Counts, type OverviewData, type Provider, type Snapshot } from "./trend";
import { TrendChart } from "./trend-chart";

const timeFormatter = new Intl.DateTimeFormat("zh-CN", {
  hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, timeZone: "Asia/Shanghai",
});

function ProviderSummary({ provider, summary }: { provider: Provider; summary: Counts }) {
  const title = provider === "tokens" ? "GPT 账号" : "即梦账号";
  const Icon = provider === "tokens" ? KeyRound : UsersRound;
  const metrics = [
    { key: "total", label: "账号总数", to: `/${provider}` },
    { key: "enabled", label: "启用", to: `/${provider}?status=enabled` },
    { key: "disabled", label: "禁用", to: `/${provider}?status=disabled` },
  ] as const;
  return <Card className="overflow-hidden">
    <div className="flex items-center justify-between gap-2 px-3 py-2.5">
      <div className="flex min-w-0 items-center gap-2">
        <span className={cn("grid size-7 shrink-0 place-items-center rounded-md", provider === "tokens" ? "bg-app-primary/10 text-app-primary" : "bg-app-info/10 text-app-info")}><Icon aria-hidden="true" className="size-3.5" strokeWidth={1.7} /></span>
        <h2 className="text-sm font-semibold">{title}</h2>
      </div>
      <Button asChild size="sm" variant="ghost" className="!h-6 gap-1 !px-2 !text-xs text-app-subtle"><Link to={`/${provider}`}>管理账号<ArrowUpRight aria-hidden="true" className="size-[13px]" /></Link></Button>
    </div>
    <div className="grid grid-cols-3 divide-x divide-app-border/60 border-t border-app-border/60">
      {metrics.map((metric) => <Link key={metric.key} to={metric.to} aria-label={`${title} · ${metric.label} ${summary[metric.key]}`} className="min-w-0 px-3 py-2.5 transition-colors hover:bg-app-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-app-ring">
        <span className="block text-xs text-app-subtle">{metric.label}</span>
        <strong className={cn("mt-0.5 block break-all text-[22px] font-semibold leading-7 tracking-tight tabular-nums", metric.key === "enabled" && "text-app-success", metric.key === "disabled" && "text-app-subtle")}>{summary[metric.key].toLocaleString("zh-CN")}</strong>
      </Link>)}
    </div>
  </Card>;
}

export function OverviewPage() {
  const [provider, setProvider] = useState<Provider>("tokens");
  const [samples, setSamples] = useState<Snapshot[]>([]);
  const [data, setData] = useState<OverviewData | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  const load = useCallback(async () => {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setRefreshing(true);
    try {
      const result = await api<OverviewData>("/api/v1/overview", { signal: request.signal });
      if (!request.signal.aborted) {
        setData(result);
        setSamples((previous) => appendSample(previous, result));
        setError("");
      }
    } catch (reason) {
      if (!request.signal.aborted) setError(errorMessage(reason));
    } finally {
      if (!request.signal.aborted) { setRefreshing(false); controller.current = null; }
    }
  }, []);
  useEffect(() => {
    void load();
    const refreshVisible = () => { if (document.visibilityState === "visible" && !controller.current) void load(); };
    const timer = setInterval(refreshVisible, UPDATE_INTERVAL);
    document.addEventListener("visibilitychange", refreshVisible);
    return () => { controller.current?.abort(); clearInterval(timer); document.removeEventListener("visibilitychange", refreshVisible); };
  }, [load]);

  return <>
    <header className="mb-3 flex flex-wrap items-end justify-between gap-x-3 gap-y-2">
      <div className="min-w-0">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h1 className="text-xl font-semibold tracking-tight">账号总览</h1>
          <p aria-live="polite" className="text-[11px] text-app-subtle">{data ? `更新于 ${timeFormatter.format(data.as_of)}` : error && !refreshing ? "尚未获取数据" : "正在获取数据…"}</p>
        </div>
        <p className="mt-1 text-sm text-app-subtle">查看 GPT 与即梦账号统计及变化趋势</p>
      </div>
      <Button disabled={refreshing} onClick={() => void load()}><RefreshCw aria-hidden="true" className={cn("size-[13px]", refreshing && "animate-spin motion-reduce:animate-none")} />{refreshing ? "更新中…" : "刷新数据"}</Button>
    </header>
    {error && <p role="alert" className="mb-3 rounded-md border border-app-danger/20 bg-app-danger/5 px-3 py-2 text-xs text-app-danger">更新失败：{error}{data ? "，当前展示上次读取的数据。" : "，请重试。"}</p>}
    {data ? <div className="grid items-start gap-3 lg:grid-cols-2">
      <ProviderSummary provider="tokens" summary={data.tokens} />
      <ProviderSummary provider="dreamina" summary={data.dreamina} />
    </div> : refreshing || !error ? <Card className="grid min-h-32 place-items-center"><Spinner label="加载账号总览" /></Card> : <Card className="grid min-h-32 place-items-center p-4 text-center text-sm text-app-subtle">暂时无法读取总览，请点击刷新重试。</Card>}
    <Card className="mt-3 p-3 sm:p-4">
      <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="inline-flex items-center gap-1.5 text-sm font-semibold"><Activity aria-hidden="true" className="size-[13px] text-app-primary" />实时数量变化</h2>
          <p className="mt-0.5 text-[10px] text-app-subtle">本次查看记录，重载后重置</p>
        </div>
        <div role="group" aria-label="趋势账号类型" className="inline-flex gap-0.5 rounded-md bg-app-muted p-0.5">
          {([{ value: "tokens", label: "GPT" }, { value: "dreamina", label: "即梦" }] as const).map((option) => <Button key={option.value} size="sm" variant="ghost" aria-pressed={provider === option.value} onClick={() => setProvider(option.value)} className={cn("!h-6 min-w-11 !rounded !border-0 !px-2 !text-xs", provider === option.value ? "!bg-app-surface !text-app-primary shadow-sm" : "text-app-subtle")}>{option.label}</Button>)}
        </div>
      </div>
      {samples.length > 0 ? <TrendChart samples={samples} provider={provider} /> : refreshing || !error ? <div className="grid h-44 place-items-center sm:h-52"><Spinner label="加载数量变化" /></div> : <div className="grid h-44 place-items-center text-sm text-app-subtle sm:h-52">暂无记录，请刷新重试。</div>}
    </Card>
  </>;
}
