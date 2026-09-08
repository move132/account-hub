import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import Eye from "lucide-react/dist/esm/icons/eye.mjs";
import Trash2 from "lucide-react/dist/esm/icons/trash-2.mjs";
import { ActionMenu, Badge, Button, Card, ConfirmDialog, DataTable, Dialog, PageHeader, Pagination, Progress, Spinner, type Column, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { parsePageSize } from "../../lib/pagination";
import { operationLabel, resultStatusLabel, targetKindLabel } from "../../lib/status-labels";
interface JobView extends Record<string, unknown> {
  id: string;
  target_kind: string;
  action: string;
  state: string;
  total: number;
  pending: number;
  running: number;
  succeeded: number;
  failed: number;
  cancelled: number;
  created_at: number;
  completed_at?: number;
  items?: JobItem[];
}
interface JobItem {
  id: string;
  target_id?: string;
  target_label: string;
  state: string;
  attempts: number;
  error_message?: string;
}
interface PageData extends Record<string, unknown> {
  items: JobView[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
}
export function JobsPage() {
  const [jobs, setJobs] = useState<JobView[]>([]), [page, setPage] = useState(1), [pageSize, setPageSize] = useState(20), [totalPages, setTotalPages] = useState(0);
  const [loading, setLoading] = useState(true), [detail, setDetail] = useState<JobView | null>(null);
  const [deleting, setDeleting] = useState<JobView | null>(null);
  const [selected, setSelected] = useState(new Set<string>()), [batchDeleteOpen, setBatchDeleteOpen] = useState(false);
  const { notify } = useToast();
  const detailId = detail?.id;
  const detailState = detail?.state;
  const load = useCallback(async (quiet = false) => {
    if (!quiet)
      setLoading(true); try {
        const data = await api<PageData>(`/api/v1/jobs?page=${page}&page_size=${pageSize}`);
        setJobs(data.items);
        setTotalPages(data.total_pages);
        const deletable = new Set(data.items.filter(canDelete).map((job) => job.id));
        setSelected((current) => {
          if (current.size === 0)
            return current;
          const next = new Set([...current].filter((id) => deletable.has(id)));
          return next.size === current.size ? current : next;
        });
        if (detailId && detailState && ["queued", "running"].includes(detailState)) {
          const current = await api<{
            job: JobView;
          } & Record<string, unknown>>(`/api/v1/jobs/${detailId}`);
          setDetail(current.job);
        }
      }
    catch (error) {
      if (!quiet)
        notify("任务加载失败", errorMessage(error), "danger");
    }
    finally {
      if (!quiet)
        setLoading(false);
    }
  }, [page, pageSize, detailId, detailState, notify]);
  const changePageSize = (value: number) => {
    setPageSize(parsePageSize(value));
    setPage(1);
  };
  useEffect(() => { void load(); const timer = setInterval(() => void load(true), 3000); return () => clearInterval(timer); }, [load]);
  const open = async (job: JobView) => {
    try {
      const data = await api<{
        job: JobView;
      } & Record<string, unknown>>(`/api/v1/jobs/${job.id}`);
      setDetail(data.job);
    }
    catch (error) {
      notify("任务加载失败", errorMessage(error), "danger");
    }
  };
  const cancel = async (job: JobView) => {
    try {
      const data = await api<{
        job: JobView;
      } & Record<string, unknown>>(`/api/v1/jobs/${job.id}/cancellations`, { method: "POST", ...jsonBody({}) });
      setDetail(data.job);
      notify("任务已取消", undefined, "warning");
      await load(true);
    }
    catch (error) {
      notify("取消失败", errorMessage(error), "danger");
    }
  };
  const remove = async () => {
    if (!deleting)
      return;
    const id = deleting.id;
    try {
      await api(`/api/v1/jobs/${id}`, { method: "DELETE" });
      setDeleting(null);
      setSelected((current) => {
        if (!current.has(id))
          return current;
        const next = new Set(current);
        next.delete(id);
        return next;
      });
      notify("任务已删除");
      if (jobs.length === 1 && page > 1)
        setPage((current) => current - 1);
      else
        await load(true);
    }
    catch (error) {
      notify("删除失败", errorMessage(error), "danger");
    }
  };
  const removeSelected = async () => {
    const ids = [...selected];
    if (ids.length === 0)
      return;
    try {
      const data = await api<{
        deleted: number;
      } & Record<string, unknown>>("/api/v1/jobs", { method: "DELETE", ...jsonBody({ ids }) });
      setBatchDeleteOpen(false);
      setSelected(new Set());
      notify(`已删除 ${data.deleted} 个任务`);
      const remainingOnPage = jobs.filter((job) => !selected.has(job.id)).length;
      if (remainingOnPage === 0 && page > 1)
        setPage((current) => current - 1);
      else
        await load(true);
    }
    catch (error) {
      notify("批量删除失败", errorMessage(error), "danger");
    }
  };
  const columns = useMemo<Array<Column<JobView>>>(() => [
    {
      key: "job", header: "任务", render: (job) => <div>
        <div className="h-auto p-0 font-mono text-xs cursor-pointer" onClick={() => void open(job)}>
          {job.id}</div>
        <p className="mt-1 text-xs text-app-subtle">
          {targetKindLabel(job.target_kind)} · {operationLabel(job.action)}</p>
      </div>
    },
    {
      key: "state", header: "状态", render: (job) => <Badge tone={job.state === "succeeded" ? "success" : job.state === "failed" ? "danger" : job.state === "partially_succeeded" ? "warning" : "neutral"}>
        {resultStatusLabel(job.state)}</Badge>
    },
    {
      key: "progress", header: "进度", render: (job) => <div className="min-w-44">
        <p className="text-xs">
          {job.succeeded + job.failed + job.cancelled} / {job.total}</p>
        <div className="mt-2">
          <Progress value={job.total ? ((job.succeeded + job.failed + job.cancelled) / job.total) * 100 : 0} />
        </div>
      </div>
    },
    {
      key: "counts", header: "结果", render: (job) => <div className="flex gap-3 text-xs">
        <span className="text-app-success">成功 {job.succeeded}</span>
        <span className="text-app-danger">失败 {job.failed}</span>
        <span className="text-app-subtle">等待 {job.pending + job.running}</span>
      </div>
    },
    {
      key: "time", header: "创建时间", render: (job) => <span className="text-xs text-app-subtle">
        {formatTime(job.created_at)}</span>
    },
    {
      key: "actions", header: "操作", className: "w-12 text-right", render: (job) => <div className="flex justify-end">
        <ActionMenu label="任务操作" items={[
          { label: "查看详情", icon: <Eye aria-hidden="true" className="size-4" />, onSelect: () => open(job) },
          { label: canDelete(job) ? "删除" : "删除（任务未结束）", icon: <Trash2 aria-hidden="true" className="size-4" />, onSelect: () => setDeleting(job), danger: true, disabled: !canDelete(job) },
        ]} />
      </div>
    },
  ], []);
  return <>
    <PageHeader title="批量任务" description="后台持久化执行，页面关闭后仍会继续" />
    <Card>
      {loading ? <div className="grid min-h-48 place-items-center">
        <Spinner />
      </div> : <DataTable columns={columns} rows={jobs} selected={selected} onSelectionChange={setSelected} isRowSelectable={canDelete} emptyText="暂无批量任务" />}<Pagination page={page} totalPages={totalPages} pageSize={pageSize} onPageSizeChange={changePageSize} onPageChange={setPage} />
    </Card>
    {selected.size > 0 && <div className="fixed bottom-4 left-1/2 z-30 flex -translate-x-1/2 items-center gap-2 rounded-lg border border-app-border bg-app-surface px-3 py-2 shadow-xl">
      <span className="whitespace-nowrap text-xs text-app-subtle">已选 {selected.size} 项</span>
      <Button size="sm" variant="danger" onClick={() => setBatchDeleteOpen(true)}>
        <Trash2 aria-hidden="true" className="size-3.5" />批量删除</Button>
    </div>}
    <Dialog open={Boolean(detail)} onOpenChange={(open) => !open && setDetail(null)} title="任务详情" description={detail?.id}>
      {detail && <div className="grid gap-3">
        <div className="grid grid-cols-3 gap-3">
          <Metric label="总数" value={detail.total} />
          <Metric label="成功" value={detail.succeeded} />
          <Metric label="失败" value={detail.failed} />
        </div>
        {["queued", "running"].includes(detail.state) && <Button variant="danger" onClick={() => void cancel(detail)}>取消未执行项目</Button>}<div className="max-h-96 divide-y divide-app-border overflow-auto rounded-lg border border-app-border">
          {detail.items?.map((item) => <div key={item.id} className="grid gap-2 p-3 text-xs sm:grid-cols-[minmax(0,1.25fr)_6.5rem_minmax(0,1fr)] sm:items-start">
            <div className="min-w-0 leading-5">
              <AccountLink targetKind={detail.target_kind} item={item} />
            </div>
            <Badge tone={item.state === "succeeded" ? "success" : item.state === "failed" ? "danger" : "neutral"}>
              {resultStatusLabel(item.state)}</Badge>
            <span className="min-w-0 break-words leading-5 text-app-subtle">
              {item.error_message || ""}</span>
          </div>)}</div>
      </div>}</Dialog>
    <ConfirmDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(null)} title="删除任务记录" description={`确定删除这条${deleting ? `${targetKindLabel(deleting.target_kind)}${operationLabel(deleting.action)}` : "批量"}任务记录？任务明细也会一并删除，此操作无法撤销。`} confirmLabel="删除" danger onConfirm={() => void remove()} />
    <ConfirmDialog open={batchDeleteOpen} onOpenChange={setBatchDeleteOpen} title="批量删除任务" description={`确定删除选中的 ${selected.size} 条任务记录吗？任务明细也会一并删除，此操作无法撤销。`} confirmLabel="批量删除" danger onConfirm={() => void removeSelected()} />
  </>;
}
function canDelete(job: JobView) {
  return !["queued", "running"].includes(job.state) && job.running === 0;
}
function Metric({ label, value }: {
  label: string;
  value: number;
}) {
  return <div className="rounded-lg bg-app-muted p-3">
    <p className="text-xs text-app-subtle">
      {label}</p>
    <p className="mt-1 text-lg font-medium">
      {value}</p>
  </div>;
}
function AccountLink({ targetKind, item }: {
  targetKind: string;
  item: JobItem;
}) {
  const path = accountPath(targetKind, item.target_id);
  if (!path) {
    return <span className="break-all font-medium">
      {item.target_label}</span>;
  }
  return <Link className="break-all font-medium hover:text-app-primary" to={path}>
    {item.target_label}</Link>;
}
function accountPath(targetKind: string, targetId?: string) {
  if (!targetId) {
    return "";
  }
  if (targetKind === "dreamina") {
    return `/dreamina/${targetId}`;
  }
  if (targetKind === "token") {
    return `/tokens/${targetId}`;
  }
  return "";
}
function formatTime(value: number) { return new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium", timeZone: "Asia/Shanghai" }).format(new Date(value)); }
