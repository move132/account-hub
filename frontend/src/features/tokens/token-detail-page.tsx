import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import HardDrive from "lucide-react/dist/esm/icons/hard-drive.mjs";
import { Badge, Button, Card, CardHeader, PageHeader, Spinner, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { accountStatusLabel, checkStateLabel, operationLabel, resultStatusLabel } from "../../lib/status-labels";
import { formatTime, type Attempt, type TokenView } from "./types";
const TokenContentDialog = lazy(() => import("./token-content-dialog"));
export function TokenDetailPage() {
  const { id = "" } = useParams();
  const [item, setItem] = useState<TokenView | null>(null);
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const [loading, setLoading] = useState(true);
  const [contentOpen, setContentOpen] = useState(false);
  const { notify } = useToast();
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [tokenData, attemptData] = await Promise.all([
        api<{
          token: TokenView;
        } & Record<string, unknown>>(`/api/v1/tokens/${id}`),
        api<{
          items: Attempt[];
        } & Record<string, unknown>>(`/api/v1/tokens/${id}/attempts`),
      ]);
      setItem(tokenData.token);
      setAttempts(attemptData.items);
    }
    catch (error) {
      notify("加载失败", errorMessage(error), "danger");
    }
    finally {
      setLoading(false);
    }
  }, [id, notify]);
  useEffect(() => { void load(); }, [load]);
  const run = async (action: "checks" | "refreshes") => {
    try {
      await api(`/api/v1/tokens/${id}/${action}`, {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID() },
        ...jsonBody({}),
      });
      notify("操作完成");
      await load();
    }
    catch (error) {
      notify("操作失败", errorMessage(error), "danger");
    }
  };
  if (loading)
    return <div className="grid min-h-48 place-items-center">
      <Spinner />
    </div>;
  if (!item)
    return <Card className="p-8">Token 不存在</Card>;
  return <>
    {contentOpen ? <Suspense fallback={<Spinner label="加载账号内容" />}><TokenContentDialog key={id} tokenId={id} label={item.email || item.name || "未命名账号"} onClose={() => setContentOpen(false)} /></Suspense> : null}
    <PageHeader title={item.email || item.name || "Token 详情"} description={item.id} actions={<>
      <Button asChild>
        <Link to="/tokens">返回列表</Link>
      </Button>
      <Button onClick={() => void run("checks")}>检查</Button>
      <Button variant="primary" disabled={!item.has_session_token} onClick={() => void run("refreshes")}>刷新</Button>
    </>} />
    <Card>
      <CardHeader title="账号状态" action={<Button size="sm" onClick={() => setContentOpen(true)}><HardDrive aria-hidden="true" className="size-3.5" />存储与聊天</Button>} />
      <dl className="grid gap-x-5 gap-y-4 p-4 text-sm sm:grid-cols-2 lg:grid-cols-3">
        <Info label="Access Token" value={item.access_token_masked} mono />
        <Info label="Session Token" value={item.session_token_masked || "—"} mono />
        <Info label="状态" value={<Badge tone={item.status === "enabled" ? "success" : "neutral"}>
          {accountStatusLabel(item.status)}</Badge>} />
        <Info label="检查结果" value={<Badge tone={item.check_state === "valid" ? "success" : item.check_state === "invalid" ? "danger" : "warning"}>
          {checkStateLabel(item.check_state)}</Badge>} />
        <Info label="Access Token 过期" value={formatTime(item.access_expires_at)} />
        <Info label="最后检查" value={formatTime(item.last_checked_at)} />
        <Info label="最后刷新" value={formatTime(item.last_refreshed_at)} />
        <Info label="下次刷新" value={formatTime(item.next_refresh_at)} />
        <Info label="标注" value={item.note || "—"} />
      </dl>
    </Card>
    <Card className="mt-4">
      <CardHeader title="执行记录" />
      <div className="divide-y divide-app-border">
        {attempts.length ? attempts.map((attempt) => <div key={attempt.id} className="grid gap-2 px-4 py-3 sm:grid-cols-[140px_120px_1fr_180px]">
          <span>
            {operationLabel(attempt.operation)}</span>
          <Badge tone={attempt.status === "succeeded" ? "success" : "danger"}>
            {resultStatusLabel(attempt.status)}</Badge>
          <span className="text-sm text-app-subtle">
            {attempt.error_message || "—"}</span>
          <span className="text-xs text-app-subtle">
            {formatTime(attempt.started_at)}</span>
        </div>) : <p className="p-4 text-app-subtle">暂无执行记录</p>}</div>
    </Card>
  </>;
}
function Info({ label, value, mono }: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
}) {
  return <div>
    <dt className="text-xs text-app-subtle">
      {label}</dt>
    <dd className={`mt-1 ${mono ? "font-mono text-xs" : ""}`}>
      {value}</dd>
  </div>;
}
