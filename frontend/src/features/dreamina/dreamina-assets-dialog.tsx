import { useEffect, useMemo, useState } from "react";
import Copy from "lucide-react/dist/esm/icons/copy.mjs";
import ExternalLink from "lucide-react/dist/esm/icons/external-link.mjs";
import Images from "lucide-react/dist/esm/icons/images.mjs";
import List from "lucide-react/dist/esm/icons/list.mjs";
import ZoomIn from "lucide-react/dist/esm/icons/zoom-in.mjs";
import { Badge, Button, Dialog, EmptyState, Spinner, useToast } from "../../components/ui";
import { api, errorMessage } from "../../lib/api";
import { asRecord, flattenHistoryMedia, mergeHistoryItems, normalizeHistoryItems, type HistoryItem, type MediaItem } from "./media";

interface DreaminaAssetsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  accountId: string;
  email: string;
  hasSessionId: boolean;
}
type HistoryViewMode = "detail" | "media";

export function DreaminaAssetsDialog({ open, onOpenChange, accountId, email, hasSessionId }: DreaminaAssetsDialogProps) {
  const [history, setHistory] = useState<HistoryItem[]>([]);
  const [assetOffset, setAssetOffset] = useState(0);
  const [hasMoreAssets, setHasMoreAssets] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [viewMode, setViewMode] = useState<HistoryViewMode>("detail");
  const [previewImage, setPreviewImage] = useState<MediaItem | null>(null);
  const { notify } = useToast();
  const media = useMemo(() => flattenHistoryMedia(history), [history]);

  useEffect(() => {
    if (!open) {
      setPreviewImage(null);
      setLoading(false);
      return;
    }
    setHistory([]);
    setAssetOffset(0);
    setHasMoreAssets(false);
    setLoaded(false);
    setPreviewImage(null);
    if (!accountId || !hasSessionId) {
      return;
    }

    let ignore = false;
    const loadInitial = async () => {
      setLoading(true);
      try {
        const page = await fetchAssets(accountId, 0);
        if (ignore) {
          return;
        }
        setHistory(normalizeHistoryItems(page));
        const listCount = Array.isArray(page.asset_list) ? page.asset_list.length : 0;
        setAssetOffset(typeof page.next_offset === "number" ? page.next_offset : listCount);
        setHasMoreAssets(Boolean(page.has_more));
        setLoaded(true);
      }
      catch (error) {
        if (!ignore) {
          notify("历史作品加载失败", errorMessage(error), "danger");
        }
      }
      finally {
        if (!ignore) {
          setLoading(false);
        }
      }
    };
    void loadInitial();
    return () => {
      ignore = true;
    };
  }, [accountId, hasSessionId, notify, open]);

  const loadMore = async () => {
    if (loading || !accountId || !hasSessionId) {
      return;
    }
    setLoading(true);
    try {
      const page = await fetchAssets(accountId, assetOffset);
      const next = normalizeHistoryItems(page);
      setHistory((current) => mergeHistoryItems(current, next));
      const listCount = Array.isArray(page.asset_list) ? page.asset_list.length : 0;
      setAssetOffset(typeof page.next_offset === "number" ? page.next_offset : assetOffset + listCount);
      setHasMoreAssets(Boolean(page.has_more));
      setLoaded(true);
    }
    catch (error) {
      notify("历史作品加载失败", errorMessage(error), "danger");
    }
    finally {
      setLoading(false);
    }
  };
  const copyPrompt = async (prompt: string) => {
    try {
      await navigator.clipboard.writeText(prompt);
      notify("提示词已复制");
    }
    catch {
      notify("复制失败", "请检查浏览器剪贴板权限", "warning");
    }
  };

  return <>
    <Dialog open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen)
        setPreviewImage(null);
      onOpenChange(nextOpen);
    }} title="历史作品" description={email} contentClassName="w-[min(96vw,1200px)]">
      {!hasSessionId ? <EmptyState title="无 Session ID" description="该账号无法加载历史作品" /> : <>
        {history.length > 0 && <HistoryModeControl value={viewMode} onChange={setViewMode} />}
        {loading && !loaded ? <div className="grid min-h-48 place-items-center">
          <Spinner label="加载历史作品" />
        </div> : history.length === 0 ? <EmptyState title={loaded ? "暂无历史作品" : "尚未加载历史作品"} /> : viewMode === "media" ? <MediaOnlyGrid media={media} onPreview={setPreviewImage} /> : <HistoryDetailGrid items={history} onPreview={setPreviewImage} onCopyPrompt={copyPrompt} />}
        {hasMoreAssets && <div className="mt-4 flex justify-center border-t border-app-border pt-4">
          <Button disabled={loading} onClick={() => void loadMore()}>
            {loading ? "加载中..." : "加载更多"}</Button>
        </div>}
      </>}
    </Dialog>
    <Dialog open={Boolean(previewImage)} onOpenChange={(previewOpen) => !previewOpen && setPreviewImage(null)} title="图片预览" contentClassName="w-[min(96vw,1280px)]">
      {previewImage && <>
        <div className="grid min-h-48 place-items-center overflow-hidden rounded-md bg-black">
          <img src={previewImage.url} alt="即梦历史作品大图" width={1920} height={1080} className="max-h-[calc(88vh-8rem)] max-w-full object-contain" />
        </div>
        <div className="mt-3 flex justify-end">
          <Button size="sm" asChild>
            <a href={previewImage.url} target="_blank" rel="noreferrer">
              <ExternalLink aria-hidden="true" className="size-3.5" />打开原文件</a>
          </Button>
        </div>
      </>}
    </Dialog>
  </>;
}

function HistoryModeControl({ value, onChange }: {
  value: HistoryViewMode;
  onChange: (value: HistoryViewMode) => void;
}) {
  return <div className="mb-3 flex justify-end">
    <div role="group" aria-label="历史记录显示方式" className="inline-flex gap-0.5 rounded-md border border-app-border bg-app-muted p-0.5">
      <Button type="button" size="sm" variant={value === "detail" ? "secondary" : "ghost"} aria-pressed={value === "detail"} className="border-0 px-2.5" onClick={() => onChange("detail")}>
        <List aria-hidden="true" className="size-3.5" />完整信息</Button>
      <Button type="button" size="sm" variant={value === "media" ? "secondary" : "ghost"} aria-pressed={value === "media"} className="border-0 px-2.5" onClick={() => onChange("media")}>
        <Images aria-hidden="true" className="size-3.5" />仅媒体</Button>
    </div>
  </div>;
}

function MediaOnlyGrid({ media, onPreview }: {
  media: MediaItem[];
  onPreview: (media: MediaItem) => void;
}) {
  return <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
    {media.map((asset) => <article key={asset.id} className="overflow-hidden rounded-lg border border-app-border bg-app-muted">
      <MediaElement media={asset} onPreview={onPreview} />
      <div className="flex items-center justify-between gap-2 p-2">
        <Badge>
          {asset.type === "image" ? "图片" : "视频"}</Badge>
        <Button size="sm" asChild>
          <a href={asset.url} target="_blank" rel="noreferrer">打开原文件</a>
        </Button>
      </div>
    </article>)}
  </div>;
}

function HistoryDetailGrid({ items, onPreview, onCopyPrompt }: {
  items: HistoryItem[];
  onPreview: (media: MediaItem) => void;
  onCopyPrompt: (prompt: string) => void | Promise<void>;
}) {
  return <div className="grid items-start gap-3 lg:grid-cols-2">
    {items.map((item) => <article key={`${item.type}-${item.id}`} className="grid gap-3 rounded-lg border border-app-border bg-app-muted p-3">
      {(item.modelName || item.resources.length > 0) && <div className="flex flex-wrap gap-1">
        {item.modelName && <Badge>
          {item.modelName}</Badge>}
        {item.resources.length > 0 && <Badge tone="success">
          上传 {item.resources.length} 张图片</Badge>}
      </div>}
      <div className="flex items-start gap-2">
        <p title={item.prompt || undefined} className={`line-clamp-2 min-w-0 flex-1 whitespace-pre-wrap break-words text-sm leading-5 ${item.prompt ? "" : "text-app-subtle"}`}>
          {item.prompt || "无提示词"}</p>
        {item.prompt && <Button type="button" size="sm" variant="ghost" aria-label="复制提示词" title="复制提示词" className="size-7 shrink-0 px-0" onClick={() => void onCopyPrompt(item.prompt)}>
          <Copy aria-hidden="true" className="size-3.5" />
        </Button>}
      </div>
      <div className={item.type === "image" ? "grid grid-cols-2 gap-2 sm:grid-cols-4" : "grid gap-2"}>
        {item.media.map((media) => <MediaElement key={media.id} media={media} onPreview={onPreview} />)}
      </div>
    </article>)}
  </div>;
}

function MediaElement({ media, onPreview }: {
  media: MediaItem;
  onPreview: (media: MediaItem) => void;
}) {
  if (media.type === "video")
    return <video aria-label="即梦历史视频" src={media.url} controls preload="metadata" className="aspect-video w-full rounded-md bg-black object-contain" />;
  return <Button type="button" variant="ghost" aria-label="放大查看图片" title="放大查看图片" className="group relative !h-auto aspect-video w-full overflow-hidden rounded-md border-0 !p-0 focus-visible:ring-inset" onClick={() => onPreview(media)}>
    <img src={media.url} alt="即梦历史作品" width={640} height={360} loading="lazy" className="size-full object-cover transition-transform duration-200 group-hover:scale-[1.03]" />
    <span className="pointer-events-none absolute bottom-2 right-2 grid size-7 place-items-center rounded-md bg-black/65 text-white shadow-sm">
      <ZoomIn aria-hidden="true" className="size-4" />
    </span>
  </Button>;
}

async function fetchAssets(accountId: string, offset: number) {
  const data = await api<{
    assets: unknown;
  } & Record<string, unknown>>(`/api/v1/dreamina/accounts/${accountId}/assets?offset=${offset}&count=50`);
  return asRecord(data.assets);
}
