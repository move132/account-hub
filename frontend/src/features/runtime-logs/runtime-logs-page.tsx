import { useDeferredValue, useEffect, useMemo, useRef, useState } from "react";
import Eraser from "lucide-react/dist/esm/icons/eraser.mjs";
import RotateCw from "lucide-react/dist/esm/icons/rotate-cw.mjs";
import Search from "lucide-react/dist/esm/icons/search.mjs";
import SquareTerminal from "lucide-react/dist/esm/icons/square-terminal.mjs";
import { Badge, Button, Card, Input, PageHeader, Select, Spinner, Switch, cn } from "../../components/ui";
import { matchesLog, maxLogEntries } from "./log-state";
import { useRuntimeLogs } from "./use-runtime-logs";

const levels = [
  { value: "all", label: "全部级别" },
  { value: "INFO", label: "INFO · 信息" },
  { value: "WARN", label: "WARN · 警告" },
  { value: "ERROR", label: "ERROR · 错误" },
  { value: "DEBUG", label: "DEBUG · 调试" },
];
const timeFormat = new Intl.DateTimeFormat("zh-CN", {
  month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit",
  hourCycle: "h23", timeZone: "Asia/Shanghai",
});
const connectionLabels = { connecting: "正在连接", connected: "实时接收中", reconnecting: "正在重连" };

export function RuntimeLogsPage() {
  const { items, truncated, connection, clear, reconnect } = useRuntimeLogs();
  const [level, setLevel] = useState("all");
  const [query, setQuery] = useState("");
  const [follow, setFollow] = useState(true);
  const deferredQuery = useDeferredValue(query);
  const viewport = useRef<HTMLDivElement>(null);
  const visible = useMemo(() => items.filter((entry) => matchesLog(entry, level, deferredQuery)), [items, level, deferredQuery]);

  useEffect(() => {
    if (follow && viewport.current) viewport.current.scrollTop = viewport.current.scrollHeight;
  }, [visible, follow]);

  return <>
    <PageHeader title="实时日志" description="查看服务运行情况、任务执行和请求异常" actions={<>
      <Button size="sm" onClick={reconnect}>
        <RotateCw aria-hidden="true" className="size-3.5" />重新连接
      </Button>
      <Button size="sm" onClick={clear} disabled={items.length === 0}>
        <Eraser aria-hidden="true" className="size-3.5" />清空当前视图
      </Button>
    </>} />
    <Card>
      <div className="flex flex-wrap items-center gap-3 border-b border-app-border px-4 py-3">
        <div className="relative min-w-44 flex-1 sm:max-w-sm">
          <Search aria-hidden="true" className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-app-subtle" />
          <Input aria-label="搜索运行日志" placeholder="搜索消息、请求 ID 或任务 ID…" value={query} onChange={(event) => setQuery(event.target.value)} className="pl-8" />
        </div>
        <Select ariaLabel="日志级别" value={level} onValueChange={setLevel} options={levels} />
        <div className="ml-auto flex items-center gap-3">
          <div className="flex items-center gap-2 text-xs text-app-subtle">
            <Switch aria-label="自动滚动" checked={follow} onCheckedChange={setFollow} />
            <span>自动滚动</span>
          </div>
          <span role="status">
            <Badge tone={connection === "connected" ? "success" : "warning"}>{connectionLabels[connection]}</Badge>
          </span>
        </div>
      </div>
      {truncated && <p role="status" className="border-b border-app-border bg-app-warning/5 px-4 py-2 text-xs text-app-warning">
        部分较早日志已被覆盖，当前显示最近记录。
      </p>}
      <div ref={viewport} role="log" aria-label="服务运行日志" aria-live="off" tabIndex={0}
        className="h-[65vh] min-h-72 max-h-[760px] overflow-auto overscroll-contain bg-app-muted/40"
        onScroll={(event) => {
          const element = event.currentTarget;
          setFollow(element.scrollHeight - element.scrollTop - element.clientHeight < 32);
        }}>
        {visible.length === 0 ? <div className="grid h-full min-h-72 place-items-center px-4 py-12 text-center">
          <div className="grid justify-items-center gap-3 text-app-subtle">
            {connection !== "connected" && items.length === 0 ? <Spinner /> : <SquareTerminal aria-hidden="true" className="size-7" />}
            <p className="text-sm">{query.trim() || level !== "all" ? "没有匹配的日志" : connection === "connected" ? "等待新的运行日志…" : "正在连接运行日志…"}</p>
          </div>
        </div> : <div className="min-w-0 divide-y divide-app-border/40 py-1 font-mono text-xs leading-5">
          {visible.map((entry) => <div key={entry.id} className="flex flex-wrap items-start gap-x-3 gap-y-0.5 px-4 py-2 hover:bg-app-hover/50 sm:flex-nowrap sm:py-1.5">
            <time dateTime={new Date(entry.time).toISOString()} className="shrink-0 whitespace-nowrap text-app-subtle">{timeFormat.format(entry.time)}</time>
            <span className={cn("w-12 shrink-0 font-semibold",
              entry.level === "INFO" && "text-app-success",
              entry.level === "WARN" && "text-app-warning",
              entry.level === "ERROR" && "text-app-danger",
              entry.level === "DEBUG" && "text-app-info")}>{entry.level}</span>
            <div className="min-w-0 basis-full whitespace-pre-wrap break-words [overflow-wrap:anywhere] sm:basis-auto sm:flex-1">
              <span className={cn(entry.level === "ERROR" && "text-app-danger", entry.level === "WARN" && "text-app-warning")}>{entry.message}</span>
              {entry.fields && <span className="text-app-subtle">{"  "}{entry.fields}</span>}
            </div>
          </div>)}
        </div>}
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-app-border px-4 py-2 text-xs text-app-subtle">
        <span>显示 {visible.length} / {items.length} 条 · 北京时间</span>
        <span>保留最近 {maxLogEntries.toLocaleString("zh-CN")} 条，服务重启后重新记录</span>
      </div>
    </Card>
  </>;
}
