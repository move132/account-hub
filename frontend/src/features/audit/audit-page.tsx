import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge, Card, DataTable, PageHeader, Pagination, Spinner, type Column, useToast } from "../../components/ui";
import { api, errorMessage } from "../../lib/api";
import { parsePageSize } from "../../lib/pagination";
import { auditActionLabel, targetKindLabel } from "../../lib/status-labels";
interface AuditEntry extends Record<string, unknown> {
  id: string;
  action: string;
  target_kind: string;
  target_id?: string;
  summary: string;
  request_id: string;
  remote_addr: string;
  created_at: number;
}
interface PageData extends Record<string, unknown> {
  items: AuditEntry[];
  total_pages: number;
}
export function AuditPage() {
  const [items, setItems] = useState<AuditEntry[]>([]), [page, setPage] = useState(1), [pageSize, setPageSize] = useState(20), [totalPages, setTotalPages] = useState(0), [loading, setLoading] = useState(true);
  const { notify } = useToast();
  const load = useCallback(async () => {
    setLoading(true); try {
      const data = await api<PageData>(`/api/v1/audit-logs?page=${page}&page_size=${pageSize}`);
      setItems(data.items);
      setTotalPages(data.total_pages);
    }
      catch (error) {
        notify("审计日志加载失败", errorMessage(error), "danger");
      }
      finally {
      setLoading(false);
    }
  }, [page, pageSize, notify]);
  const changePageSize = (value: number) => {
    setPageSize(parsePageSize(value));
    setPage(1);
  };
  useEffect(() => { void load(); }, [load]);
  const columns = useMemo<Array<Column<AuditEntry>>>(() => [
    {
      key: "action", header: "操作", render: (entry) => <div>
        <p className="font-medium">
          {entry.summary}</p>
        <p className="mt-1 font-mono text-xs text-app-subtle">
          {auditActionLabel(entry.action)}</p>
      </div>
    },
    {
      key: "target", header: "目标", render: (entry) => <div className="gap-1">
        <Badge>{targetKindLabel(entry.target_kind)}</Badge>
        <span className="mt-1 max-w-60 truncate font-mono text-xs text-app-subtle">
          {entry.target_id || ""}
        </span>
      </div>
    },
    {
      key: "request", header: "请求", render: (entry) => <div className="font-mono text-xs text-app-subtle">
        <p>
          {entry.request_id}</p>
        <p className="mt-1">
          {entry.remote_addr}</p>
      </div>
    },
    {
      key: "time", header: "时间", render: (entry) => <span className="text-xs text-app-subtle">
        {new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium", timeZone: "Asia/Shanghai" }).format(new Date(entry.created_at))}</span>
    },
  ], []);
  return <>
    <PageHeader title="审计日志" description="记录管理员登录、凭据变更、批量操作和设置修改" />
    <Card>
      {loading ? <div className="grid min-h-48 place-items-center">
        <Spinner />
      </div> : <DataTable columns={columns} rows={items} emptyText="暂无审计记录" />}<Pagination page={page} totalPages={totalPages} pageSize={pageSize} onPageSizeChange={changePageSize} onPageChange={setPage} />
    </Card>
  </>;
}
