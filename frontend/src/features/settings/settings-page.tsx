import { useCallback, useEffect, useMemo, useState } from "react";
import X from "lucide-react/dist/esm/icons/x.mjs";
import { Button, Card, CardHeader, ConfirmDialog, Field, Input, PageHeader, Spinner, Switch, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
type Value = boolean | number | string;
interface Setting extends Record<string, unknown> {
  key: string;
  group: "token" | "dreamina" | "network";
  label: string;
  type: "boolean" | "integer" | "string";
  value: Value;
  default_value: Value;
  min?: number;
  max?: number;
}
interface SettingsData extends Record<string, unknown> {
  settings: Setting[];
}
interface CleanupResult {
  audit_logs: number;
  token_attempts: number;
  dreamina_attempts: number;
  jobs: number;
}
interface CleanupData extends Record<string, unknown> {
  result: CleanupResult;
}
const groupMeta = { token: ["Token 自动刷新", "控制 Session Token 刷新节奏"], dreamina: ["即梦自动刷新", "控制账号重新登录和 Session 更新节奏"], network: ["网络代理", "OpenAI 与即梦共同使用的出站代理"] } as const;
export function SettingsPage() {
  const [items, setItems] = useState<Setting[]>([]), [values, setValues] = useState<Record<string, Value>>({}), [dirty, setDirty] = useState(new Set<string>()), [loading, setLoading] = useState(true), [saving, setSaving] = useState(false);
  const initialCleanupTime = useMemo(() => inputParts(new Date(Date.now() - 30 * dayMs)), []);
  const [cleanupDate, setCleanupDate] = useState(initialCleanupTime.date);
  const [cleanupTime, setCleanupTime] = useState(initialCleanupTime.time);
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const { notify } = useToast();
  const load = useCallback(async () => {
    setLoading(true); try {
      const data = await api<SettingsData>("/api/v1/settings");
      setItems(data.settings);
      setValues(Object.fromEntries(data.settings.map((item) => [item.key, item.value])));
      setDirty(new Set());
    }
      catch (error) {
        notify("设置加载失败", errorMessage(error), "danger");
      }
      finally {
      setLoading(false);
    }
  }, [notify]);
  useEffect(() => { void load(); }, [load]);
  const dirtyCount = dirty.size;
  useEffect(() => {
    if (dirtyCount === 0)
      return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirtyCount]);
  const grouped = useMemo(() => items.reduce<Record<Setting["group"], Setting[]>>((result, item) => {
    result[item.group].push(item);
    return result;
  }, { token: [], dreamina: [], network: [] }), [items]);
  const change = (key: string, value: Value) => { setValues((current) => ({ ...current, [key]: value })); setDirty((current) => new Set(current).add(key)); };
  const save = async () => {
    if (!dirty.size)
      return; setSaving(true); try {
        const updates = Object.fromEntries([...dirty].map((key) => [key, values[key]]));
        const data = await api<SettingsData>("/api/v1/settings", { method: "PATCH", ...jsonBody({ settings: updates }) });
        setItems(data.settings);
        setValues(Object.fromEntries(data.settings.map((item) => [item.key, item.value])));
        setDirty(new Set());
        notify("设置已保存");
      }
    catch (error) {
      notify("保存失败", errorMessage(error), "danger");
    }
    finally {
      setSaving(false);
    }
  };
  const setCleanupPreset = (duration: number) => {
    const parts = inputParts(new Date(Date.now() - duration));
    setCleanupDate(parts.date);
    setCleanupTime(parts.time);
  };
  const cleanupBefore = () => {
    if (!cleanupDate || !cleanupTime) {
      return null;
    }
    const value = new Date(`${cleanupDate}T${cleanupTime}`);
    return Number.isNaN(value.getTime()) ? null : value;
  };
  const cleanupHistory = async () => {
    const before = cleanupBefore();
    if (!before) {
      notify("请选择清理时间", undefined, "danger");
      return;
    }
    setCleaning(true);
    try {
      const data = await api<CleanupData>("/api/v1/history-log-cleanups", { method: "POST", ...jsonBody({ before: before.getTime() }) });
      const attempts = data.result.token_attempts + data.result.dreamina_attempts;
      notify("清理完成", `审计日志 ${data.result.audit_logs} 条，执行记录 ${attempts} 条，批量任务 ${data.result.jobs} 条`, "info");
      setCleanupOpen(false);
    }
    catch (error) {
      notify("清理失败", errorMessage(error), "danger");
    }
    finally {
      setCleaning(false);
    }
  };
  if (loading)
    return <div className="grid min-h-48 place-items-center">
      <Spinner />
    </div>;
  return <>
    <PageHeader title="系统设置" description="仅保留账号刷新与统一出站代理配置" actions={<Button variant="primary" disabled={!dirty.size || saving} onClick={() => void save()}>
      {saving ? "保存中…" : `保存设置${dirty.size ? ` (${dirty.size})` : ""}`}</Button>} />
    <div className="grid gap-3">
      <Card>
        <CardHeader title="清理历史日志" description="移除所选时间戳之前创建的所有日志条目。" />
        <div className="grid gap-3 p-4">
          <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_8.5rem_auto]">
            <Input aria-label="清理日期" type="date" value={cleanupDate} onChange={(event) => setCleanupDate(event.target.value)} />
            <Input aria-label="清理时间" type="time" value={cleanupTime} onChange={(event) => setCleanupTime(event.target.value)} />
            <Button aria-label="清空时间" variant="secondary" onClick={() => {
              setCleanupDate("");
              setCleanupTime("");
            }}>
              <X aria-hidden="true" className="size-4" /></Button>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setCleanupPreset(dayMs)}>24 小时前</Button>
            <Button onClick={() => setCleanupPreset(7 * dayMs)}>7 天前</Button>
            <Button onClick={() => setCleanupPreset(30 * dayMs)}>30 天前</Button>
            <Button variant="danger" disabled={!cleanupDate || !cleanupTime || cleaning} onClick={() => setCleanupOpen(true)}>
              清理日志</Button>
          </div>
        </div>
      </Card>
      {(["token", "dreamina", "network"] as const).map((group) => <Card key={group}>
        <CardHeader title={groupMeta[group][0]} description={groupMeta[group][1]} />
        <div className="grid gap-3 p-4 md:grid-cols-2">
          {grouped[group]?.map((item) => <SettingControl key={item.key} item={item} value={values[item.key]} onChange={(value) => change(item.key, value)} />)}</div>
      </Card>)}</div>
    <ConfirmDialog open={cleanupOpen} onOpenChange={setCleanupOpen} title="清理历史日志" description="确定清理所选时间之前的审计日志、执行记录和已结束批量任务吗？此操作无法撤销，执行中和排队中的任务不会删除。" confirmLabel={cleaning ? "清理中…" : "清理日志"} danger onConfirm={() => void cleanupHistory()} />
  </>;
}
function SettingControl({ item, value, onChange }: {
  item: Setting;
  value: Value;
  onChange: (value: Value) => void;
}) {
  if (item.type === "boolean")
    return <div className="flex min-h-16 items-center justify-between gap-3 rounded-md border border-app-border bg-app-muted p-3">
      <div>
        <p className="font-medium">
          {item.label}</p>
        <p className="mt-1 font-mono text-[11px] text-app-subtle">
          {item.key}</p>
      </div>
      <Switch checked={Boolean(value)} onCheckedChange={onChange} />
    </div>;
  const isProxy = item.key === "proxy_url";
  return <Field label={item.label} hint={`${item.key}${item.min !== undefined ? ` · ${item.min}–${item.max}` : ""}`}>
    <Input name={item.key} autoComplete="off" type={item.type === "integer" ? "number" : "text"} min={item.min} max={item.max} value={String(value ?? "")} placeholder={isProxy && value === "••••••••" ? "已配置，输入新值以替换" : undefined} onFocus={() => {
      if (isProxy && value === "••••••••")
        onChange("");
    }} onChange={(event) => onChange(item.type === "integer" ? Number(event.target.value) : event.target.value)} />
  </Field>;
}

const dayMs = 24 * 60 * 60 * 1000;

function inputParts(value: Date) {
  const pad = (item: number) => String(item).padStart(2, "0");
  return {
    date: `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}`,
    time: `${pad(value.getHours())}:${pad(value.getMinutes())}`,
  };
}
