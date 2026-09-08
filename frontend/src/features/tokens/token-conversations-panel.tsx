import { useEffect, useState } from "react";
import RefreshCw from "lucide-react/dist/esm/icons/refresh-cw.mjs";
import { Badge, Button, Card, EmptyState, Spinner, cn } from "../../components/ui";
import { api, errorMessage } from "../../lib/api";
import { contentPath, mergeContentItems, type ConversationDetail, type ConversationPage, type ConversationSummary } from "./content-types";
import { TokenContentImage, TokenImagePreview } from "./token-content-image";
import { formatTime } from "./types";

export function TokenConversationsPanel({ tokenId }: { tokenId: string }) {
  const [items, setItems] = useState<ConversationSummary[]>([]);
  const [offset, setOffset] = useState(0);
  const [nextOffset, setNextOffset] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [total, setTotal] = useState<number | null>(null);
  const [selected, setSelected] = useState<ConversationSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    if (offset === 0) { setItems([]); setSelected(null); }
    void api<ConversationPage>(`${contentPath(tokenId)}/conversations?offset=${offset}&limit=30`, { signal: controller.signal })
      .then((page) => {
        if (controller.signal.aborted) return;
        setItems((current) => mergeContentItems(offset === 0 ? [] : current, page.items));
        setSelected((current) => current ?? page.items[0] ?? null);
        setTotal(page.total);
        setNextOffset(page.next_offset);
        setHasMore(page.has_more && page.next_offset > offset);
      })
      .catch((failure: unknown) => { if (!controller.signal.aborted) setError(errorMessage(failure)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [tokenId, offset, reload]);
  const refresh = () => { setOffset(0); setReload((value) => value + 1); };

  return <section aria-label="账号聊天记录" className="grid min-w-0 grid-cols-1 gap-3">
    <div className="flex items-center justify-between gap-2"><p className="text-sm text-app-subtle">{total !== null ? `共 ${total} 条会话` : `已加载 ${items.length} 条会话`}</p><Button size="sm" disabled={loading} onClick={refresh}><RefreshCw aria-hidden="true" className="size-3.5" />刷新记录</Button></div>
    <div className="grid min-w-0 grid-cols-1 items-start gap-3 md:grid-cols-[280px_minmax(0,1fr)]">
      <Card className="min-w-0 overflow-hidden">
        <h3 className="border-b border-app-border p-3 text-xs font-medium text-app-subtle">历史会话</h3>
        <div className="max-h-[48vh] overflow-y-auto md:max-h-[56vh]">
          {loading && items.length === 0 ? <div className="grid min-h-40 place-items-center"><Spinner label="加载聊天记录" /></div> : items.length === 0 ? <EmptyState title={error ? "聊天记录加载失败" : "暂无聊天记录"} /> : items.map((item) => <Button key={item.id} variant="ghost" aria-label={`查看会话 ${item.title || "未命名会话"}`} aria-pressed={selected?.id === item.id} className={cn("!h-auto w-full flex-col !items-start rounded-none border-b border-app-border px-3 py-3 text-left", selected?.id === item.id && "bg-app-primary/10 text-app-primary")} onClick={() => setSelected(item)}>
            <span className="w-full truncate font-medium" title={item.title}>{item.title || "未命名会话"}</span><span className="mt-1 text-xs font-normal text-app-subtle">{formatTime(item.update_time || item.create_time)}</span>
          </Button>)}
        </div>
        {error ? <div role="alert" className="grid gap-2 p-3 text-xs text-app-danger"><p className="break-words">{error}</p><Button size="sm" onClick={refresh}>重新加载</Button></div> : null}
        {hasMore ? <div className="flex justify-center border-t border-app-border p-3"><Button size="sm" disabled={loading} onClick={() => setOffset(nextOffset)}>{loading ? "加载中…" : "加载更多会话"}</Button></div> : null}
      </Card>
      {selected ? <ConversationMessages key={`${selected.id}-${reload}`} tokenId={tokenId} summary={selected} /> : <Card className="grid min-h-72 place-items-center"><EmptyState title="选择一条会话查看内容" /></Card>}
    </div>
  </section>;
}

function ConversationMessages({ tokenId, summary }: { tokenId: string; summary: ConversationSummary }) {
  const [detail, setDetail] = useState<ConversationDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [preview, setPreview] = useState<{ url: string; label: string } | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setDetail(null);
    void api<{ conversation: ConversationDetail }>(`${contentPath(tokenId)}/conversations/${encodeURIComponent(summary.id)}`, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setDetail(data.conversation); })
      .catch((failure: unknown) => { if (!controller.signal.aborted) setError(errorMessage(failure)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [tokenId, summary.id, retry]);
  const roles: Record<string, string> = { user: "用户", assistant: "助手", system: "系统", tool: "工具", unknown: "其他" };
  return <Card className="min-w-0 overflow-hidden">
    <div className="border-b border-app-border p-3"><h3 className="break-words text-sm font-medium">{detail?.title || summary.title || "未命名会话"}</h3><p className="mt-1 text-xs text-app-subtle">{formatTime(detail?.update_time || summary.update_time || summary.create_time)}</p></div>
    <div aria-label="聊天内容" className="max-h-[56vh] min-h-72 space-y-4 overflow-y-auto p-4">
      {loading ? <div className="grid min-h-48 place-items-center"><Spinner label="加载聊天内容" /></div> : error ? <div role="alert" className="grid gap-3 text-sm text-app-danger"><p className="break-words">{error}</p><Button onClick={() => setRetry((value) => value + 1)}>重试聊天内容</Button></div> : detail?.messages.length ? detail.messages.map((message) => <article key={message.id} className={cn("flex", message.role === "user" ? "justify-end" : "justify-start")}>
        <div className={cn("max-w-[95%] rounded-lg border border-app-border p-3 sm:max-w-[90%]", message.role === "user" ? "bg-app-primary/10" : "bg-app-muted")}>
          <div className="mb-2 flex flex-wrap items-center gap-2"><Badge>{roles[message.role] || message.author || message.role}</Badge><span className="text-xs text-app-subtle">{formatTime(message.create_time)}</span></div>
          {message.content && !(message.content === "[图片]" && message.assets.length > 0) ? <p className="whitespace-pre-wrap break-words text-sm leading-6 [overflow-wrap:anywhere]">{message.content}</p> : null}
          {message.assets.length > 0 ? <div className="mt-3 flex flex-wrap gap-2">{message.assets.map((asset) => <TokenContentImage key={asset.file_id} tokenId={tokenId} fileId={asset.file_id} onPreview={setPreview} />)}</div> : null}
        </div>
      </article>) : <EmptyState title="暂无可展示的聊天内容" />}
    </div>
    <TokenImagePreview image={preview} onClose={() => setPreview(null)} />
  </Card>;
}
