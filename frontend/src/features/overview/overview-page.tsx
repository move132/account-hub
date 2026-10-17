import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import ArrowUpRight from "lucide-react/dist/esm/icons/arrow-up-right.mjs";
import KeyRound from "lucide-react/dist/esm/icons/key-round.mjs";
import RefreshCw from "lucide-react/dist/esm/icons/refresh-cw.mjs";
import UsersRound from "lucide-react/dist/esm/icons/users-round.mjs";
import Activity from "lucide-react/dist/esm/icons/activity.mjs";
import { Button, Card, PageHeader, Spinner, cn } from "../../components/ui";
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
  return <Card className="relative overflow-hidden !rounded-xl shadow-sm shadow-black/[0.025]">
    <div aria-hidden="true" className={cn("pointer-events-none absolute -right-12 -top-16 size-44 rounded-full blur-3xl", provider === "tokens" ? "bg-app-primary/[0.06]" : "bg-app-info/[0.06]")} />
    <div className="relative flex items-center justify-between gap-3 px-5 pb-4 pt-5 sm:px-6">
      <div className="flex items-center gap-3">
        <span className={cn("grid size-10 place-items-center rounded-xl", provider === "tokens" ? "bg-app-primary/10 text-app-primary" : "bg-app-info/10 text-app-info")}><Icon aria-hidden="true" className="size-5" strokeWidth={1.7} /></span>
        <h2 className="text-base font-semibold">{title}</h2>
      </div>
      <Button asChild size="sm" variant="ghost" className="text-app-subtle"><Link to={`/${provider}`}>管理账号<ArrowUpRight aria-hidden="true" className="size-3.5" /></Link></Button>
    </div>
    <div className="relative mx-3 mb-3 grid grid-cols-3 rounded-lg bg-app-muted/50 px-2 py-4 sm:mx-4 sm:mb-4">
      {metrics.map((metric) => <Link key={metric.key} to={metric.to} aria-label={`${title} · ${metric.label} ${summary[metric.key]}`} className="group rounded-lg px-2 py-2 transition-colors hover:bg-app-surface focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-ring sm:px-3">
        <span className="block text-xs text-app-subtle">{metric.label}</span>
        <strong className={cn("mt-2.5 block text-[30px] font-semibold leading-none tracking-tight tabular-nums sm:text-[34px]", metric.key === "enabled" && "text-app-success", metric.key === "disabled" && "text-app-subtle")}>{summary[metric.key].toLocaleString("zh-CN")}</strong>
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
    <PageHeader title="账号总览" description="查看账号数量与变化趋势。" actions={<Button size="sm" disabled={refreshing} onClick={() => void load()}><RefreshCw aria-hidden="true" className={cn("size-3.5", refreshing && "animate-spin motion-reduce:animate-none")} />{refreshing ? "更新中…" : "刷新数据"}</Button>} />
    <div className="mb-4 text-[11px] text-app-subtle">
      <div aria-live="polite">
        {error && <p role="alert" className="text-app-danger">更新失败：{error}{data ? "，当前展示上次读取的数据。" : "，请重试。"}</p>}
        <p>{data ? `更新于 ${timeFormatter.format(data.as_of)}` : "正在获取数据…"}</p>
      </div>
    </div>
    {data ? <div className="grid items-start gap-4 lg:grid-cols-2">
      <ProviderSummary provider="tokens" summary={data.tokens} />
      <ProviderSummary provider="dreamina" summary={data.dreamina} />
    </div> : refreshing ? <Card className="grid min-h-48 place-items-center"><Spinner label="加载账号总览" /></Card> : <Card className="p-8 text-center text-sm text-app-subtle">暂时无法读取总览，请点击刷新重试。</Card>}
    <Card className="mt-5 !rounded-xl p-4 shadow-sm shadow-black/[0.025] sm:p-6">
      <div className="mb-6 flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 className="inline-flex items-center gap-2 font-semibold"><Activity aria-hidden="true" className="size-4 text-app-primary" />实时数量变化</h2>
          <p className="mt-1 text-[11px] text-app-subtle">仅本次查看期间，刷新或离开页面后重新记录</p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <div role="group" aria-label="趋势账号类型" className="inline-flex rounded-lg bg-app-muted p-1">
            {([{ value: "tokens", label: "GPT" }, { value: "dreamina", label: "即梦" }] as const).map((option) => <Button key={option.value} size="sm" variant="ghost" aria-pressed={provider === option.value} onClick={() => setProvider(option.value)} className={cn("min-w-14", provider === option.value ? "bg-app-surface text-app-primary shadow-sm" : "text-app-subtle")}>{option.label}</Button>)}
          </div>
        </div>
      </div>
      {samples.length > 0 ? <TrendChart samples={samples} provider={provider} /> : refreshing ? <div className="grid h-72 place-items-center"><Spinner label="加载数量变化" /></div> : <div className="grid h-72 place-items-center text-sm text-app-subtle">暂无记录，请刷新重试。</div>}
    </Card>
  </>;
}
