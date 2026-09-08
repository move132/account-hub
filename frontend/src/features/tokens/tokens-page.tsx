import { lazy, Suspense, useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import CheckCircle from "lucide-react/dist/esm/icons/circle-check.mjs";
import Pencil from "lucide-react/dist/esm/icons/pencil.mjs";
import RefreshCw from "lucide-react/dist/esm/icons/refresh-cw.mjs";
import Trash2 from "lucide-react/dist/esm/icons/trash-2.mjs";
import HardDrive from "lucide-react/dist/esm/icons/hard-drive.mjs";
import { ActionMenu, Badge, Button, Card, ConfirmDialog, DataTable, Dialog, Field, FileInput, Input, PageHeader, Pagination, Select, Spinner, Textarea, cn, type Column, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { parsePageSize } from "../../lib/pagination";
import { accountStatusLabel, checkStateLabel, localizeStatusPreview } from "../../lib/status-labels";
import { formatTime, type TokenEditView, type TokenView } from "./types";
const TokenContentDialog = lazy(() => import("./token-content-dialog"));
interface PageData extends Record<string, unknown> {
  items: TokenView[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
}
const emptyForm = { name: "", email: "", access_token: "", session_token: "", status: "enabled", note: "" };
export function TokensPage() {
  const [params, setParams] = useSearchParams();
  const [items, setItems] = useState<TokenView[]>([]);
  const [total, setTotal] = useState(0);
  const [totalPages, setTotalPages] = useState(0);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState(new Set<string>());
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<TokenView | null>(null);
  const [form, setForm] = useState({ ...emptyForm });
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<TokenView | null>(null);
  const [content, setContent] = useState<TokenView | null>(null);
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [importFile, setImportFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<Record<string, unknown> | null>(null);
  const { notify } = useToast();
  const page = Math.max(1, Number(params.get("page") || 1));
  const pageSize = parsePageSize(params.get("page_size"));
  const search = params.get("search") || "";
  const status = params.get("status") || "all";
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const query = new URLSearchParams({ page: String(page), page_size: String(pageSize), search });
      if (status !== "all")
        query.set("status", status);
      const data = await api<PageData>(`/api/v1/tokens?${query}`);
      setItems(data.items);
      setTotal(data.total);
      setTotalPages(data.total_pages);
      setSelected(new Set());
    }
    catch (error) {
      notify("Token 加载失败", errorMessage(error), "danger");
    }
    finally {
      setLoading(false);
    }
  }, [page, pageSize, search, status, notify]);
  useEffect(() => { const timer = setTimeout(() => void load(), 350); return () => clearTimeout(timer); }, [load]);
  const updateParam = (key: string, value: string) => {
    const next = new URLSearchParams(params); if (!value || value === "all")
      next.delete(key);
    else
      next.set(key, value); if (key !== "page")
      next.set("page", "1"); setParams(next);
  };
  const openCreate = () => { setEditing(null); setForm({ ...emptyForm }); setFormOpen(true); };
  const openEdit = async (item: TokenView) => {
    try {
      const data = await api<{
        token: TokenEditView;
      } & Record<string, unknown>>(`/api/v1/tokens/${item.id}/edit`);
      const edit = data.token;
      setEditing(edit);
      setForm({ name: edit.name, email: edit.email, access_token: edit.access_token, session_token: edit.session_token, status: edit.status, note: edit.note });
      setFormOpen(true);
    }
    catch (error) {
      notify("加载编辑信息失败", errorMessage(error), "danger");
    }
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try {
      if (editing) {
        const payload: Record<string, unknown> = { name: form.name, email: form.email, status: form.status, note: form.note, version: editing.version };
        if (form.access_token)
          payload.access_token = form.access_token;
        if (form.session_token)
          payload.session_token = form.session_token;
        await api(`/api/v1/tokens/${editing.id}`, { method: "PATCH", ...jsonBody(payload) });
      }
      else {
        await api("/api/v1/tokens", { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, ...jsonBody(form) });
      }
      setFormOpen(false);
      notify(editing ? "Token 已更新" : "Token 已创建");
      await load();
    }
    catch (error) {
      notify("保存失败", errorMessage(error), "danger");
    }
    finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!deleting)
      return; try {
        await api(`/api/v1/tokens/${deleting.id}`, { method: "DELETE", ...jsonBody({}) });
        notify("Token 已删除");
        setDeleting(null);
        await load();
      }
    catch (error) {
      notify("删除失败", errorMessage(error), "danger");
    }
  };
  const action = async (item: TokenView, name: "checks" | "refreshes") => {
    try {
      await api(`/api/v1/tokens/${item.id}/${name}`, { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, ...jsonBody({}) });
      notify(name === "checks" ? "检查完成" : "刷新完成");
      await load();
    }
    catch (error) {
      notify("操作失败", errorMessage(error), "danger");
    }
  };
  const batch = async (actionName: string) => {
    if (!selected.size)
      return; try {
        const data = await api<{
          job: {
            id: string;
          };
        } & Record<string, unknown>>("/api/v1/token-batch-jobs", { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, ...jsonBody({ action: actionName, ids: [...selected] }) });
        notify("批量任务已创建", data.job.id, "info");
        setSelected(new Set());
      }
    catch (error) {
      notify("创建任务失败", errorMessage(error), "danger");
    }
  };
  const batchDelete = async () => { await batch("delete"); setBatchDeleteOpen(false); };
  const doImport = async (dryRun: boolean) => {
    if (!importFile)
      return; const data = new FormData(); data.set("file", importFile); try {
        const result = await api<Record<string, unknown>>(`/api/v1/token-imports?dry_run=${dryRun}`, { method: "POST", headers: dryRun ? {} : { "Idempotency-Key": crypto.randomUUID() }, body: data });
        if (dryRun)
          setPreview(result);
        else {
          notify("导入任务已创建");
          setImportOpen(false);
          setPreview(null);
          await load();
        }
      }
    catch (error) {
      notify("导入失败", errorMessage(error), "danger");
    }
  };
  const exportData = async () => {
    try {
      const data = await api<{
        filename: string;
        content: string;
      } & Record<string, unknown>>("/api/v1/token-exports", { method: "POST", ...jsonBody({}) });
      download(data.filename, data.content);
      notify("导出成功");
    }
    catch (error) {
      notify("导出失败", errorMessage(error), "danger");
    }
  };
  const localizedPreview = useMemo(() => preview ? JSON.stringify(localizeStatusPreview(preview), null, 2) : "", [preview]);
  const columns = useMemo<Array<Column<TokenView>>>(() => [
    {
      key: "account", header: "账号", render: (item) => <div>
        <Link className="font-medium hover:text-app-primary" to={`/tokens/${item.id}`}>
          {item.email || item.name || "未命名"}</Link>
        <p className="mt-1 font-mono text-xs text-app-subtle">
          {item.access_token_masked}</p>
      </div>
    },
    {
      key: "session", header: "Session", render: (item) => <span className="font-mono text-xs text-app-subtle">
        {item.session_token_masked || "—"}</span>
    },
    {
      key: "status", header: "账号状态", render: (item) => <div className="flex flex-wrap gap-1">
        <Badge tone={item.status === "enabled" ? "success" : "neutral"}>
          {accountStatusLabel(item.status)}</Badge>
        <Badge tone={item.check_state === "valid" ? "success" : item.check_state === "invalid" ? "danger" : item.check_state === "error" ? "warning" : "neutral"}>
          {checkStateLabel(item.check_state)}</Badge>
      </div>
    },
    {
      key: "note", header: "标注", render: (item) => <span className="block max-w-64 truncate text-xs text-app-subtle" title={item.note || undefined}>
        {item.note || "—"}</span>
    },
    {
      key: "updated", header: "更新时间", render: (item) => <span className="text-xs text-app-subtle">
        {formatTime(item.updated_at)}</span>
    },
    {
      key: "actions", header: "操作", className: "w-24 text-right", render: (item) => <div className="flex items-center justify-end gap-1">
        <Button size="sm" variant="ghost" className="size-8 shrink-0 px-0" aria-label="存储与聊天" title="存储与聊天" onClick={() => setContent(item)}><HardDrive aria-hidden="true" className="size-4" /></Button>
        <ActionMenu label="操作" items={[
          { label: "检查", icon: <CheckCircle aria-hidden="true" className="size-4" />, onSelect: () => action(item, "checks") },
          { label: "刷新", icon: <RefreshCw aria-hidden="true" className="size-4" />, onSelect: () => action(item, "refreshes"), disabled: !item.has_session_token },
          { label: "编辑", icon: <Pencil aria-hidden="true" className="size-4" />, onSelect: () => openEdit(item) },
          { label: "删除", icon: <Trash2 aria-hidden="true" className="size-4" />, onSelect: () => setDeleting(item), danger: true },
        ]} />
      </div>
    },
  ], [load]);
  return <>
    {content ? <Suspense fallback={<Spinner label="加载账号内容" />}><TokenContentDialog key={content.id} tokenId={content.id} label={content.email || content.name || "未命名账号"} onClose={() => setContent(null)} /></Suspense> : null}
    <PageHeader title="GPT账号" description={`共 ${total} 个账号凭据`} actions={<>
      <Button onClick={() => setImportOpen(true)}>导入</Button>
      <Button onClick={() => void exportData()}>导出数据</Button>
      <Button variant="primary" onClick={openCreate}>新增账号</Button>
    </>} />
    <Card>
      <div className="grid min-h-14 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-app-border p-3">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <Input aria-label="搜索 Token" name="token-search" autoComplete="off" spellCheck={false} className="max-w-sm" placeholder="搜索名称、邮箱或标注…" value={search} onChange={(event) => updateParam("search", event.target.value)} />
          <Select ariaLabel="Token 状态筛选" value={status} onValueChange={(value) => updateParam("status", value)} options={[{ value: "all", label: "全部状态" }, { value: "enabled", label: "已启用" }, { value: "disabled", label: "已禁用" }]} />
        </div>
        <div className={cn("flex h-8 max-w-[min(58vw,34rem)] items-center gap-1.5 overflow-x-auto whitespace-nowrap", selected.size === 0 && "invisible pointer-events-none")}>
          <span className="text-xs text-app-subtle">已选 {selected.size} 项</span>
          <Button size="sm" onClick={() => void batch("check")}>批量检查</Button>
          <Button size="sm" onClick={() => void batch("refresh")}>批量刷新</Button>
          <Button size="sm" onClick={() => void batch("enable")}>启用</Button>
          <Button size="sm" onClick={() => void batch("disable")}>禁用</Button>
          <Button size="sm" variant="danger" onClick={() => setBatchDeleteOpen(true)}>删除</Button>
        </div>
      </div>
      {loading ? <div className="grid min-h-48 place-items-center">
        <Spinner />
      </div> : <DataTable columns={columns} rows={items} selected={selected} onSelectionChange={setSelected} emptyText="还没有 Token" />}<Pagination page={page} totalPages={totalPages} pageSize={pageSize} onPageSizeChange={(value) => updateParam("page_size", String(value))} onPageChange={(value) => updateParam("page", String(value))} />
    </Card>
    <Dialog open={formOpen} onOpenChange={setFormOpen} title={editing ? "编辑 Token" : "新增 Token"} description="凭据在列表中仍以遮罩显示">
      <form className="grid gap-3" onSubmit={submit}>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="名称">
            <Input name="name" autoComplete="off" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
          </Field>
          <Field label="邮箱">
            <Input name="email" autoComplete="off" spellCheck={false} type="email" value={form.email} onChange={(event) => setForm({ ...form, email: event.target.value })} />
          </Field>
        </div>
        <Field label="Access Token">
          <Textarea name="access-token" autoComplete="off" required={!editing} value={form.access_token} onChange={(event) => setForm({ ...form, access_token: event.target.value })} />
        </Field>
        <Field label="Session Token">
          <Textarea name="session-token" autoComplete="off" value={form.session_token} onChange={(event) => setForm({ ...form, session_token: event.target.value })} />
        </Field>
        <Field label="状态">
          <Select ariaLabel="Token 状态" value={form.status} onValueChange={(value) => setForm({ ...form, status: value })} options={[{ value: "enabled", label: "启用" }, { value: "disabled", label: "禁用" }]} />
        </Field>
        <Field label="标注">
          <Textarea name="note" autoComplete="off" value={form.note} onChange={(event) => setForm({ ...form, note: event.target.value })} />
        </Field>
        <div className="flex justify-end gap-2">
          <Button type="button" onClick={() => setFormOpen(false)}>取消</Button>
          <Button type="submit" variant="primary" disabled={saving}>
            {saving ? "保存中…" : "保存"}</Button>
        </div>
      </form>
    </Dialog>
    <Dialog open={importOpen} onOpenChange={setImportOpen} title="导入 Token" description="CSV 列：name,email,access_token,session_token,access_expires_at,status,note">
      <div className="grid gap-3">
        <FileInput aria-label="选择 Token CSV 文件" name="token-import" accept=".csv,text/csv" onChange={(event) => { setImportFile(event.target.files?.[0] || null); setPreview(null); }} />
        {localizedPreview && <pre className="max-h-64 overflow-auto rounded-lg bg-app-muted p-3 text-xs text-app-subtle">
          {localizedPreview}</pre>}<div className="flex justify-end gap-2">
          <Button disabled={!importFile} onClick={() => void doImport(true)}>预检</Button>
          <Button variant="primary" disabled={!importFile || !preview} onClick={() => void doImport(false)}>确认导入</Button>
        </div>
      </div>
    </Dialog>
    <ConfirmDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(null)} title="删除 Token" description={`确定删除 ${deleting?.email || deleting?.name || "该 Token"}？凭据将立即删除。`} confirmLabel="删除" danger onConfirm={() => void remove()} />
    <ConfirmDialog open={batchDeleteOpen} onOpenChange={setBatchDeleteOpen} title="批量删除 Token" description={`确定删除选中的 ${selected.size} 项吗？该操作由后台任务执行。`} confirmLabel="创建删除任务" danger onConfirm={() => void batchDelete()} />
  </>;
}
function download(filename: string, content: string) { const blob = new Blob([content], { type: "text/csv;charset=utf-8" }); const url = URL.createObjectURL(blob); const anchor = document.createElement("a"); anchor.href = url; anchor.download = filename; anchor.click(); URL.revokeObjectURL(url); }
