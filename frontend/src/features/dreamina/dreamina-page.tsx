import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import BadgeDollarSign from "lucide-react/dist/esm/icons/badge-dollar-sign.mjs";
import CheckCircle from "lucide-react/dist/esm/icons/circle-check.mjs";
import Images from "lucide-react/dist/esm/icons/images.mjs";
import Pencil from "lucide-react/dist/esm/icons/pencil.mjs";
import RefreshCw from "lucide-react/dist/esm/icons/refresh-cw.mjs";
import Trash2 from "lucide-react/dist/esm/icons/trash-2.mjs";
import { ActionMenu, Badge, Button, Card, ConfirmDialog, DataTable, Dialog, Field, Input, PageHeader, Pagination, Select, Spinner, Textarea, TooltipText, cn, type Column, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { parsePageSize } from "../../lib/pagination";
import { accountStatusLabel, checkStateLabel } from "../../lib/status-labels";
import { formatTime, type DreaminaEditView, type DreaminaView } from "./types";
import { DreaminaAssetsDialog } from "./dreamina-assets-dialog";
import { DreaminaImportDialog } from "./dreamina-import-dialog";
interface PageData extends Record<string, unknown> {
  items: DreaminaView[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
}
type BatchConfirmAction = "check" | "refresh";
const emptyForm = { email: "", password: "", session_id: "", status: "enabled", note: "" };
export function DreaminaPage() {
  const [params, setParams] = useSearchParams();
  const [items, setItems] = useState<DreaminaView[]>([]);
  const [total, setTotal] = useState(0);
  const [totalPages, setTotalPages] = useState(0);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState(new Set<string>());
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<DreaminaView | null>(null);
  const [form, setForm] = useState({ ...emptyForm });
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<DreaminaView | null>(null);
  const [assetsAccount, setAssetsAccount] = useState<DreaminaView | null>(null);
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false);
  const [batchConfirmAction, setBatchConfirmAction] = useState<BatchConfirmAction | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [importJob, setImportJob] = useState<{ id: string; skipped: number } | null>(null);
  const { notify } = useToast();
  const page = Math.max(1, Number(params.get("page") || 1)), pageSize = parsePageSize(params.get("page_size")), search = params.get("search") || "", status = params.get("status") || "all";
  const load = useCallback(async () => {
    setLoading(true); try {
      const query = new URLSearchParams({ page: String(page), page_size: String(pageSize), search });
      if (status !== "all")
        query.set("status", status);
      const data = await api<PageData>(`/api/v1/dreamina/accounts?${query}`);
      setItems(data.items);
      setTotal(data.total);
      setTotalPages(data.total_pages);
      setSelected(new Set());
    }
      catch (error) {
        notify("即梦账号加载失败", errorMessage(error), "danger");
      }
      finally {
      setLoading(false);
    }
  }, [page, pageSize, search, status, notify]);
  useEffect(() => { const timer = setTimeout(() => void load(), 350); return () => clearTimeout(timer); }, [load]);
  useEffect(() => {
    if (!importJob) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const { job } = await api<{ job: { state: string; succeeded: number; failed: number } } & Record<string, unknown>>(
          `/api/v1/jobs/${importJob.id}`, { signal: controller.signal });
        if (controller.signal.aborted) return;
        if (job.state === "queued" || job.state === "running") {
          timer = setTimeout(() => void poll(), 1000);
          return;
        }
        setImportJob(null);
        notify("Session 导入完成", `成功 ${job.succeeded} 条，失败 ${job.failed} 条，跳过 ${importJob.skipped} 条`, job.failed > 0 ? "danger" : "success");
        await load();
      } catch (error) {
        if (controller.signal.aborted) return;
        setImportJob(null);
        notify("暂时无法获取导入结果", `可在批量任务中查看。${errorMessage(error)}`, "danger");
      }
    };
    void poll();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [importJob, load, notify]);
  const updateParam = (key: string, value: string) => {
    const next = new URLSearchParams(params); if (!value || value === "all")
      next.delete(key);
    else
      next.set(key, value); if (key !== "page")
      next.set("page", "1"); setParams(next);
  };
  const openCreate = () => { setEditing(null); setForm({ ...emptyForm }); setFormOpen(true); };
  const openEdit = async (item: DreaminaView) => {
    try {
      const data = await api<{
        account: DreaminaEditView;
      } & Record<string, unknown>>(`/api/v1/dreamina/accounts/${item.id}/edit`);
      const edit = data.account;
      setEditing(edit);
      setForm({ email: edit.email, password: edit.password, session_id: edit.session_id, status: edit.status, note: edit.note });
      setFormOpen(true);
    }
    catch (error) {
      notify("加载编辑信息失败", errorMessage(error), "danger");
    }
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setSaving(true); try {
      if (editing) {
        const payload: Record<string, unknown> = { email: form.email, status: form.status, note: form.note, version: editing.version };
        if (form.password)
          payload.password = form.password;
        if (form.session_id)
          payload.session_id = form.session_id;
        await api(`/api/v1/dreamina/accounts/${editing.id}`, { method: "PATCH", ...jsonBody(payload) });
      }
      else
        await api("/api/v1/dreamina/accounts", { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, ...jsonBody(form) });
      setFormOpen(false);
      notify(editing ? "即梦账号已更新" : "即梦账号已创建");
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
        await api(`/api/v1/dreamina/accounts/${deleting.id}`, { method: "DELETE", ...jsonBody({}) });
        notify("即梦账号已删除");
        setDeleting(null);
        await load();
      }
    catch (error) {
      notify("删除失败", errorMessage(error), "danger");
    }
  };
  const action = async (item: DreaminaView, name: "checks" | "refreshes") => {
    try {
      await api(`/api/v1/dreamina/accounts/${item.id}/${name}`, { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, ...jsonBody({}) });
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
        } & Record<string, unknown>>("/api/v1/dreamina-batch-jobs", { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, ...jsonBody({ action: actionName, ids: [...selected] }) });
        notify("批量任务已创建", data.job.id, "info");
        setSelected(new Set());
      }
    catch (error) {
      notify("创建任务失败", errorMessage(error), "danger");
    }
  };
  const batchDelete = async () => { await batch("delete"); setBatchDeleteOpen(false); };
  const confirmBatchAction = async () => {
    if (!batchConfirmAction)
      return;
    const actionName = batchConfirmAction;
    setBatchConfirmAction(null);
    await batch(actionName);
  };
  const exportData = async () => {
    try {
      const data = await api<{
        filename: string;
        content: string;
      } & Record<string, unknown>>("/api/v1/dreamina-exports", { method: "POST", ...jsonBody({}) });
      download(data.filename, data.content);
      notify("导出成功");
    }
    catch (error) {
      notify("导出失败", errorMessage(error), "danger");
    }
  };
  const columns = useMemo<Array<Column<DreaminaView>>>(() => [
    {
      key: "account", header: "账号", render: (item) => <div>
        <Link className="font-medium hover:text-app-primary" to={`/dreamina/${item.id}`}>
          {item.email}</Link>
        <p className="mt-1 font-mono text-xs text-app-subtle">
          {item.session_id_masked || "无 Session"}</p>
      </div>
    },
    {
      key: "status", header: "状态", className: "whitespace-nowrap", render: (item) => <div className="flex gap-1">
        <Badge tone={item.status === "enabled" ? "success" : "neutral"}>
          {accountStatusLabel(item.status)}</Badge>
        <Badge tone={item.check_state === "valid" ? "success" : item.check_state === "invalid" ? "danger" : item.check_state === "error" ? "warning" : "neutral"}>
          {checkStateLabel(item.check_state)}</Badge>
      </div>
    },
    {
      key: "credit", header: "积分", className: "text-center", render: (item) => <TooltipText label={`积分：${item.credit_balance || ""}`}>
        <span className="inline-flex size-8 items-center justify-center rounded-md text-app-subtle hover:bg-app-hover hover:text-app-foreground">
          <BadgeDollarSign aria-hidden="true" className="size-4" />
          <span className="sr-only">积分：{item.credit_balance || ""}</span>
        </span>
      </TooltipText>
    },
    {
      key: "sessionTime", header: "时间", className: "whitespace-nowrap", render: (item) => <div className="grid gap-0.5 text-[11px] leading-4 text-app-subtle">
        <span>刷新：{formatTime(item.last_refreshed_at)}</span>
        <span>过期：{formatTime(item.session_expires_at)}</span>
      </div>
    },
    {
      key: "description", header: "描述", className: "min-w-[100px]", render: (item) => <span className="text-sm text-app-subtle">
        {item.note || ""}</span>
    },
    {
      key: "actions", header: "操作", className: "w-24 text-right", render: (item) => <div className="flex justify-end gap-1">
        <TooltipText label="历史作品">
          <Button aria-label="历史作品" title="历史作品" size="sm" variant="ghost" className="size-8 px-0" disabled={!item.has_session_id} onClick={() => setAssetsAccount(item)}>
            <Images aria-hidden="true" className="size-4" />
          </Button>
        </TooltipText>
        <ActionMenu label="操作" items={[
          { label: "检查", icon: <CheckCircle aria-hidden="true" className="size-4" />, onSelect: () => action(item, "checks"), disabled: !item.has_session_id },
          { label: "刷新", icon: <RefreshCw aria-hidden="true" className="size-4" />, onSelect: () => action(item, "refreshes") },
          { label: "编辑", icon: <Pencil aria-hidden="true" className="size-4" />, onSelect: () => openEdit(item) },
          { label: "删除", icon: <Trash2 aria-hidden="true" className="size-4" />, onSelect: () => setDeleting(item), danger: true },
        ]} />
      </div>
    },
  ], [load]);
  return <>
    <PageHeader title="即梦账号" description={`共 ${total} 个账号`} actions={<>
      <Button onClick={() => setImportOpen(true)}>导入</Button>
      <Button onClick={() => void exportData()}>导出数据</Button>
      <Button variant="primary" onClick={openCreate}>新增账号</Button>
    </>} />
    <Card>
      <div className="grid min-h-14 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-app-border p-3">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <Input aria-label="搜索即梦账号" name="dreamina-search" autoComplete="off" spellCheck={false} className="max-w-sm" placeholder="搜索邮箱或备注…" value={search} onChange={(event) => updateParam("search", event.target.value)} />
          <Select ariaLabel="即梦账号状态筛选" value={status} onValueChange={(value) => updateParam("status", value)} options={[{ value: "all", label: "全部状态" }, { value: "enabled", label: "已启用" }, { value: "disabled", label: "已禁用" }]} />
        </div>
        <div className={cn("flex h-8 max-w-[min(58vw,34rem)] items-center gap-1.5 overflow-x-auto whitespace-nowrap", selected.size === 0 && "invisible pointer-events-none")}>
          <span className="text-xs text-app-subtle">已选 {selected.size} 项</span>
          <Button size="sm" onClick={() => setBatchConfirmAction("check")}>批量检查</Button>
          <Button size="sm" onClick={() => setBatchConfirmAction("refresh")}>批量刷新</Button>
          <Button size="sm" onClick={() => void batch("enable")}>启用</Button>
          <Button size="sm" onClick={() => void batch("disable")}>禁用</Button>
          <Button size="sm" variant="danger" onClick={() => setBatchDeleteOpen(true)}>删除</Button>
        </div>
      </div>
      {loading ? <div className="grid min-h-48 place-items-center">
        <Spinner />
      </div> : <DataTable columns={columns} rows={items} selected={selected} onSelectionChange={setSelected} emptyText="还没有即梦账号" />}<Pagination page={page} totalPages={totalPages} pageSize={pageSize} onPageSizeChange={(value) => updateParam("page_size", String(value))} onPageChange={(value) => updateParam("page", String(value))} />
    </Card>
    <DreaminaAssetsDialog open={Boolean(assetsAccount)} onOpenChange={(open) => !open && setAssetsAccount(null)} accountId={assetsAccount?.id || ""} email={assetsAccount?.email || ""} hasSessionId={Boolean(assetsAccount?.has_session_id)} />
    <Dialog open={formOpen} onOpenChange={setFormOpen} title={editing ? "编辑即梦账号" : "新增即梦账号"} description="密码和 Session ID 在列表中仍以遮罩显示">
      <form className="grid gap-3" onSubmit={submit}>
        <Field label="邮箱">
          <Input name="email" autoComplete="off" spellCheck={false} type="email" required value={form.email} onChange={(event) => setForm({ ...form, email: event.target.value })} />
        </Field>
        <Field label="密码">
          <Input name="account-password" autoComplete="off" type={editing ? "text" : "password"} required={!editing} value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} />
        </Field>
        <Field label="Session ID">
          <Textarea name="session-id" autoComplete="off" value={form.session_id} onChange={(event) => setForm({ ...form, session_id: event.target.value })} />
        </Field>
        <Field label="状态">
          <Select ariaLabel="即梦账号状态" value={form.status} onValueChange={(value) => setForm({ ...form, status: value })} options={[{ value: "enabled", label: "启用" }, { value: "disabled", label: "禁用" }]} />
        </Field>
        <Field label="备注">
          <Textarea name="note" autoComplete="off" value={form.note} onChange={(event) => setForm({ ...form, note: event.target.value })} />
        </Field>
        <div className="flex justify-end gap-2">
          <Button type="button" onClick={() => setFormOpen(false)}>取消</Button>
          <Button type="submit" variant="primary" disabled={saving}>
            {saving ? "保存中…" : "保存"}</Button>
        </div>
      </form>
    </Dialog>
    {importOpen ? <DreaminaImportDialog onClose={() => setImportOpen(false)} onImported={(id, skipped) => {
      if (id) setImportJob({ id, skipped });
      else void load();
    }} /> : null}
    <ConfirmDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(null)} title="删除即梦账号" description={`确定删除 ${deleting?.email || "该账号"}？凭据将立即删除。`} confirmLabel="删除" danger onConfirm={() => void remove()} />
    <ConfirmDialog open={Boolean(batchConfirmAction)} onOpenChange={(open) => !open && setBatchConfirmAction(null)} title={batchConfirmAction === "check" ? "批量检查即梦账号" : "批量刷新即梦账号"} description={`确定对选中的 ${selected.size} 项执行${batchConfirmAction === "check" ? "批量检查" : "批量刷新"}吗？该操作由后台任务执行。`} confirmLabel={batchConfirmAction === "check" ? "创建检查任务" : "创建刷新任务"} onConfirm={() => void confirmBatchAction()} />
    <ConfirmDialog open={batchDeleteOpen} onOpenChange={setBatchDeleteOpen} title="批量删除即梦账号" description={`确定删除选中的 ${selected.size} 项吗？该操作由后台任务执行。`} confirmLabel="创建删除任务" danger onConfirm={() => void batchDelete()} />
  </>;
}
function download(filename: string, content: string) { const blob = new Blob([content], { type: "text/csv;charset=utf-8" }); const url = URL.createObjectURL(blob); const anchor = document.createElement("a"); anchor.href = url; anchor.download = filename; anchor.click(); URL.revokeObjectURL(url); }
