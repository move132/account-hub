import { useEffect, useMemo, useRef, useState } from "react";
import ImageIcon from "lucide-react/dist/esm/icons/image.mjs";
import RefreshCw from "lucide-react/dist/esm/icons/refresh-cw.mjs";
import Trash2 from "lucide-react/dist/esm/icons/trash-2.mjs";
import { Badge, Button, Card, ConfirmDialog, DataTable, Dialog, Progress, Spinner, useToast, type Column } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { canDeleteLibraryFile, contentPath, formatBytes, mergeContentItems, usagePercentage, type LibraryDeleteResult, type LibraryFile, type LibraryPage, type StorageUsage } from "./content-types";
import { TokenContentImage, TokenImagePreview } from "./token-content-image";
import { formatTime } from "./types";

export function TokenStoragePanel({ tokenId }: { tokenId: string }) {
  const [usage, setUsage] = useState<StorageUsage | null>(null);
  const [usageError, setUsageError] = useState("");
  const [usageLoading, setUsageLoading] = useState(true);
  const [files, setFiles] = useState<LibraryFile[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState(new Set<string>());
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [failures, setFailures] = useState<LibraryDeleteResult["failures"]>([]);
  const [previewFile, setPreviewFile] = useState<LibraryFile | null>(null);
  const [preview, setPreview] = useState<{ url: string; label: string } | null>(null);
  const mutation = useRef<AbortController | null>(null);
  const { notify } = useToast();

  useEffect(() => {
    const controller = new AbortController();
    setUsageLoading(true);
    setUsageError("");
    void api<{ usage: StorageUsage }>(`${contentPath(tokenId)}/storage`, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setUsage(data.usage); })
      .catch((failure: unknown) => { if (!controller.signal.aborted) setUsageError(errorMessage(failure)); })
      .finally(() => { if (!controller.signal.aborted) setUsageLoading(false); });
    return () => controller.abort();
  }, [tokenId, reload]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    if (cursor === null) { setFiles([]); setSelected(new Set()); }
    const query = new URLSearchParams();
    if (cursor) query.set("cursor", cursor);
    void api<LibraryPage>(`${contentPath(tokenId)}/library/files?${query}`, { signal: controller.signal })
      .then((page) => {
        if (controller.signal.aborted) return;
        setFiles((current) => mergeContentItems(cursor === null ? [] : current, page.items));
        setNextCursor(page.cursor && page.cursor !== cursor ? page.cursor : null);
      })
      .catch((failure: unknown) => { if (!controller.signal.aborted) setError(errorMessage(failure)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [tokenId, cursor, reload]);

  useEffect(() => () => mutation.current?.abort(), []);
  const refresh = () => { setCursor(null); setReload((value) => value + 1); };
  const deleteFiles = async () => {
    if (deleting || loading || selected.size === 0 || selected.size > 100) return;
    const controller = new AbortController();
    mutation.current = controller;
    setDeleting(true);
    setFailures([]);
    try {
      const chosen = files.filter((file) => selected.has(file.id) && canDeleteLibraryFile(file));
      const result = await api<LibraryDeleteResult>(`${contentPath(tokenId)}/library/deletions`, {
        method: "POST", signal: controller.signal,
        headers: { "Idempotency-Key": crypto.randomUUID() },
        ...jsonBody({ files: chosen.map((file) => ({ library_file_id: file.id, file_id: file.file_id, parent_directory_id: file.parent_directory_id, file_name: file.name })) }),
      });
      if (controller.signal.aborted) return;
      setFailures(result.failures);
      notify(`已删除 ${result.deleted_count} 张图片`, result.failure_count ? `${result.failure_count} 张未删除或待确认，请查看结果` : undefined, result.failure_count ? "warning" : "success");
      refresh();
    } catch (failure) {
      if (!controller.signal.aborted) notify("删除图片失败", errorMessage(failure), "danger");
    } finally {
      if (!controller.signal.aborted) setDeleting(false);
    }
  };
  const columns = useMemo<Array<Column<LibraryFile>>>(() => [
    { key: "name", header: "名称", render: (file) => <Button variant="ghost" size="sm" className="max-w-sm !justify-start px-0" disabled={!file.file_id} onClick={() => setPreviewFile(file)}>
      <ImageIcon aria-hidden="true" className="size-4 shrink-0 text-app-subtle" /><span className="truncate" title={file.name}>{file.name || "未命名图片"}</span>
    </Button> },
    { key: "updated", header: "修改时间", render: (file) => <span className="text-xs text-app-subtle">{formatTime(file.updated_at)}</span> },
    { key: "size", header: "大小", render: (file) => <span className="text-xs">{formatBytes(file.file_size_bytes)}</span> },
  ], []);
  const imageCount = usage?.breakdown_by_file_type.find((item) => item.file_type === "image")?.count;

  return <section aria-label="账号存储空间" className="grid min-w-0 grid-cols-1 gap-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div><h3 className="font-medium">存储用量</h3><p className="mt-1 text-xs text-app-subtle">{imageCount !== undefined ? `${imageCount} 张图片 · ` : ""}已加载 {files.length} 个图片文件</p></div>
      <Button size="sm" disabled={loading || deleting || usageLoading} onClick={refresh}><RefreshCw aria-hidden="true" className="size-3.5" />刷新</Button>
    </div>
    {usage ? <Card className="grid gap-3 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm"><span>已使用 <strong>{formatBytes(usage.used_bytes)}</strong> / {formatBytes(usage.allowed_bytes)}</span><Badge tone={usage.is_over_limit ? "danger" : "neutral"}>{usage.is_over_limit ? "已超出容量" : `剩余 ${formatBytes(usage.remaining_bytes)}`}</Badge></div>
      <Progress value={usagePercentage(usage)} label="存储空间使用量" />
    </Card> : usageLoading ? <Spinner label="加载存储用量" /> : null}
    {usageError ? <p role="alert" className="break-words text-sm text-app-danger">用量加载失败：{usageError}</p> : null}
    <Card className="min-w-0 overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-app-border p-3">
        <span className="text-xs text-app-subtle">已选 {selected.size} 项{selected.size > 100 ? "，单次最多删除 100 张" : ""}</span>
        <Button size="sm" variant="danger" disabled={selected.size === 0 || selected.size > 100 || deleting || loading} onClick={() => setConfirmOpen(true)}><Trash2 aria-hidden="true" className="size-3.5" />{deleting ? "删除中…" : "删除选中"}</Button>
      </div>
      {loading && files.length === 0 ? <div className="grid min-h-48 place-items-center"><Spinner label="加载图片列表" /></div> : <DataTable columns={columns} rows={files} selected={selected} onSelectionChange={setSelected} isRowSelectable={(file) => !deleting && canDeleteLibraryFile(file)} emptyText={error ? "图片列表加载失败" : "暂无图片文件"} />}
      {error ? <div role="alert" className="flex flex-wrap items-center gap-2 p-3 text-sm text-app-danger"><span className="min-w-0 flex-1 break-words">{error}</span><Button size="sm" onClick={refresh}>重新加载</Button></div> : null}
      {nextCursor ? <div className="flex justify-center border-t border-app-border p-3"><Button size="sm" disabled={loading || deleting} onClick={() => setCursor(nextCursor)}>{loading ? "加载中…" : "加载更多图片"}</Button></div> : null}
    </Card>
    {failures.length > 0 ? <div role="alert" className="rounded-md border border-app-danger/30 p-3 text-sm"><p className="font-medium text-app-danger">以下图片未删除或结果待确认</p><ul className="mt-2 list-inside list-disc space-y-1 text-xs text-app-subtle">{failures.map((failure) => <li key={failure.library_file_id}>{failure.file_name}：{failure.message}</li>)}</ul></div> : null}
    <ConfirmDialog open={confirmOpen} onOpenChange={setConfirmOpen} title="删除图片" description={`确定删除选中的 ${selected.size} 张图片吗？它们将从该账号的 ChatGPT 资料库中移除。`} confirmLabel="确认删除" danger onConfirm={() => { setConfirmOpen(false); void deleteFiles(); }} />
    <Dialog open={Boolean(previewFile)} onOpenChange={(open) => { if (!open) setPreviewFile(null); }} title="图片文件" description={previewFile?.name}>
      {previewFile ? <div className="grid justify-center gap-3"><TokenContentImage key={previewFile.id} tokenId={tokenId} fileId={previewFile.file_id} label={previewFile.name} onPreview={setPreview} /><p className="text-center text-xs text-app-subtle">点击图片放大查看</p></div> : null}
    </Dialog>
    <TokenImagePreview image={preview} onClose={() => setPreview(null)} />
  </section>;
}
