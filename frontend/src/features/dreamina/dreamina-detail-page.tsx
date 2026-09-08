import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Badge, Button, Card, CardHeader, ConfirmDialog, PageHeader, Spinner, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { accountStatusLabel, checkStateLabel, operationLabel, resultStatusLabel } from "../../lib/status-labels";
import { formatTime, type Attempt, type DreaminaView } from "./types";
import { DreaminaAssetsDialog } from "./dreamina-assets-dialog";
export function DreaminaDetailPage() {
  const { id = "" } = useParams();
  const [item, setItem] = useState<DreaminaView | null>(null);
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const [loading, setLoading] = useState(true);
  const [claimOpen, setClaimOpen] = useState(false);
  const [assetsOpen, setAssetsOpen] = useState(false);
  const [claimKey, setClaimKey] = useState("");
  const { notify } = useToast();
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [accountData, attemptData] = await Promise.all([
        api<{
          account: DreaminaView;
        } & Record<string, unknown>>(`/api/v1/dreamina/accounts/${id}`),
        api<{
          items: Attempt[];
        } & Record<string, unknown>>(`/api/v1/dreamina/accounts/${id}/attempts`),
      ]);
      setItem(accountData.account);
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
      await api(`/api/v1/dreamina/accounts/${id}/${action}`, { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, ...jsonBody({}) });
      notify("操作完成");
      await load();
    }
    catch (error) {
      notify("操作失败", errorMessage(error), "danger");
    }
  };
  const queryCredit = async () => {
    try {
      const data = await api<{
        balance: string;
      } & Record<string, unknown>>(`/api/v1/dreamina/accounts/${id}/credit`);
      notify("积分查询成功", `当前余额：${data.balance || "—"}`, "info");
      await load();
    }
    catch (error) {
      notify("积分查询失败", errorMessage(error), "danger");
    }
  };
  const claim = async () => {
    try {
      await api(`/api/v1/dreamina/accounts/${id}/credit-claims`, { method: "POST", headers: { "Idempotency-Key": claimKey || crypto.randomUUID() }, ...jsonBody({}) });
      notify("积分领取成功");
      setClaimOpen(false);
      await load();
    }
    catch (error) {
      notify("积分领取失败", errorMessage(error), "danger");
    }
  };
  if (loading)
    return <div className="grid min-h-48 place-items-center">
      <Spinner />
    </div>;
  if (!item)
    return <Card className="p-8">即梦账号不存在</Card>;
  return <>
    <PageHeader title={item.email} description={item.id} actions={<>
      <Button asChild>
        <Link to="/dreamina">返回列表</Link>
      </Button>
      <Button disabled={!item.has_session_id} onClick={() => void run("checks")}>检查</Button>
      <Button variant="primary" onClick={() => void run("refreshes")}>刷新 Session</Button>
    </>} />
    <Card>
      <CardHeader title="账号状态" />
      <div className="grid gap-4 p-4">
        <dl className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
          <Info label="密码" value={item.password_masked} mono />
          <Info label="Session ID" value={item.session_id_masked || "—"} mono />
          <Info label="状态" value={<Badge tone={item.status === "enabled" ? "success" : "neutral"}>
            {accountStatusLabel(item.status)}</Badge>} />
          <Info label="检查结果" value={<Badge tone={item.check_state === "valid" ? "success" : item.check_state === "invalid" ? "danger" : "warning"}>
            {checkStateLabel(item.check_state)}</Badge>} />
          <Info label="积分" value={item.credit_balance || "—"} />
          <Info label="Session 过期" value={formatTime(item.session_expires_at)} />
          <Info label="最后检查" value={formatTime(item.last_checked_at)} />
          <Info label="最后刷新" value={formatTime(item.last_refreshed_at)} />
        </dl>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" disabled={!item.has_session_id} onClick={() => void queryCredit()}>查询积分</Button>
          <Button size="sm" disabled={!item.has_session_id} onClick={() => { setClaimKey(crypto.randomUUID()); setClaimOpen(true); }}>领取积分</Button>
          <Button size="sm" disabled={!item.has_session_id} onClick={() => setAssetsOpen(true)}>查看历史作品</Button>
        </div>
      </div>
    </Card>
    <DreaminaAssetsDialog open={assetsOpen} onOpenChange={setAssetsOpen} accountId={item.id} email={item.email} hasSessionId={item.has_session_id} />
    <Card className="mt-4">
      <CardHeader title="执行记录" />
      <div className="divide-y divide-app-border">
        {attempts.length ? attempts.map((attempt) => <div key={attempt.id} className="grid gap-2 px-4 py-3 sm:grid-cols-[140px_120px_1fr_180px]">
          <span>
            {operationLabel(attempt.operation)}</span>
          <Badge tone={attempt.status === "succeeded" ? "success" : "danger"}>
            {resultStatusLabel(attempt.status)}</Badge>
          <span className="text-sm text-app-subtle">
            {attempt.error_message || ""}</span>
          <span className="text-xs text-app-subtle">
            {formatTime(attempt.started_at)}</span>
        </div>) : <p className="p-4 text-app-subtle">暂无执行记录</p>}</div>
    </Card>
    <ConfirmDialog open={claimOpen} onOpenChange={(open) => {
      setClaimOpen(open); if (!open)
        setClaimKey("");
    }} title="领取即梦积分" description="该操作会调用即梦领取接口并产生真实副作用，确认继续吗？" confirmLabel="确认领取" onConfirm={() => void claim()} />
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
