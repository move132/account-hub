import { useCallback, useEffect, useMemo, useState } from "react";
import X from "lucide-react/dist/esm/icons/x.mjs";
import { Button, Card, CardHeader, ConfirmDialog, DatePicker, Field, Input, PageHeader, Spinner, Switch, TimePicker, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { ApiKeySettings } from "../api-keys/api-key-settings";
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
const controlMeta: Record<string, { label: string; unit: string }> = {
  token_refresh_interval_hours: { label: "刷新周期", unit: "小时" },
  token_refresh_batch_size: { label: "单次数量", unit: "个" },
  token_refresh_account_interval_seconds: { label: "账号间隔", unit: "秒" },
  dreamina_refresh_interval_days: { label: "刷新周期", unit: "天" },
  dreamina_refresh_batch_size: { label: "单次数量", unit: "个" },
  dreamina_refresh_account_interval_minutes: { label: "账号间隔", unit: "分钟" },
};
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
    <PageHeader title="系统设置" description="管理 API Key、账号刷新、网络代理与历史日志" actions={<Button variant="primary" disabled={!dirty.size || saving} onClick={() => void save()}>
      {saving ? "保存中…" : `保存设置${dirty.size ? ` (${dirty.size})` : ""}`}</Button>} />
    <div className="grid gap-3 lg:grid-cols-2">
      <div className="min-w-0 lg:col-span-2"><ApiKeySettings /></div>
      {(["token", "dreamina", "network"] as const).map((group) => <SettingGroup key={group} group={group} items={grouped[group]} values={values} onChange={change} />)}
      <Card className="min-w-0">
        <CardHeader title="清理历史日志" description="移除所选时间之前的日志和已结束任务。" />
        <div className="grid gap-3 p-3 sm:p-4">
          <div className="grid gap-1.5">
            <div className="flex items-center justify-between gap-2">
              <p className="text-sm font-medium">截止时间</p>
              <Button size="sm" aria-label="清空时间" title="清空时间" variant="ghost" className="h-6! w-6 px-0! text-app-subtle" onClick={() => {
                setCleanupDate("");
                setCleanupTime("");
              }}>
                <X aria-hidden="true" className="size-3.5" /></Button>
            </div>
            <div className="grid grid-cols-[minmax(0,1fr)_5.5rem] items-center gap-1.5 sm:gap-2">
              <DatePicker aria-label="清理日期" value={cleanupDate} onValueChange={setCleanupDate} />
              <TimePicker aria-label="清理时间" value={cleanupTime} onValueChange={setCleanupTime} />
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button size="sm" onClick={() => setCleanupPreset(dayMs)}>24 小时前</Button>
            <Button size="sm" onClick={() => setCleanupPreset(7 * dayMs)}>7 天前</Button>
            <Button size="sm" onClick={() => setCleanupPreset(30 * dayMs)}>30 天前</Button>
            <Button size="sm" variant="danger" className="ml-auto" disabled={!cleanupDate || !cleanupTime || cleaning} onClick={() => setCleanupOpen(true)}>
              清理日志</Button>
          </div>
        </div>
      </Card>
    </div>
    <ConfirmDialog open={cleanupOpen} onOpenChange={setCleanupOpen} title="清理历史日志" description="确定清理所选时间之前的审计日志、执行记录和已结束批量任务吗？此操作无法撤销，执行中和排队中的任务不会删除。" confirmLabel={cleaning ? "清理中…" : "清理日志"} danger onConfirm={() => void cleanupHistory()} />
  </>;
}
function SettingGroup({ group, items, values, onChange }: {
  group: Setting["group"];
  items: Setting[];
  values: Record<string, Value>;
  onChange: (key: string, value: Value) => void;
}) {
  return <Card className="min-w-0">
    <CardHeader title={groupMeta[group][0]} description={groupMeta[group][1]} action={
      <div className="flex shrink-0 items-center gap-2 pt-0.5">
        {items.filter((item) => item.type === "boolean").map((item) => <div key={item.key} className="flex items-center gap-2">
          <span className="hidden whitespace-nowrap text-xs text-app-subtle sm:inline">{values[item.key] ? "已开启" : "已关闭"}</span>
          <Switch aria-label={item.label} checked={Boolean(values[item.key])} onCheckedChange={(value) => onChange(item.key, value)} />
        </div>)}
      </div>
    } />
    <div className={group === "network" ? "grid gap-3 p-3 sm:p-4" : "grid grid-cols-3 gap-2 p-3 sm:gap-3 sm:p-4"}>
      {items.filter((item) => item.type !== "boolean").map((item) => <SettingControl key={item.key} item={item} value={values[item.key]} onChange={(value) => onChange(item.key, value)} />)}
    </div>
  </Card>;
}

function SettingControl({ item, value, onChange }: {
  item: Setting;
  value: Value;
  onChange: (value: Value) => void;
}) {
  const presentation = controlMeta[item.key];
  const range = item.type === "integer" && item.max !== undefined ? `${item.min ?? 0}–${item.max}` : undefined;
  const label = presentation?.label ?? item.label;
  return <Field label={range ? <span className="flex min-h-9 flex-wrap content-start items-baseline justify-between gap-x-1.5 gap-y-0.5 sm:min-h-0">
    <span>{label}</span>
    <span className="ml-auto shrink-0 whitespace-nowrap text-xs font-normal text-app-subtle">{range}</span>
  </span> : label} hint={item.key === "proxy_url" ? "支持 HTTP、HTTPS 和 SOCKS5" : undefined}>
    <div className="relative min-w-0">
      <Input name={item.key} aria-label={item.label} autoComplete="off" type={item.type === "integer" ? "number" : "text"}
        min={item.min} max={item.max} value={String(value ?? "")} placeholder={item.key === "proxy_url" ? "http://127.0.0.1:7890" : undefined}
        className={presentation ? "pr-9 tabular-nums [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none" : undefined}
        onChange={(event) => onChange(item.type === "integer" ? Number(event.target.value) : event.target.value)} />
      {presentation && <span aria-hidden="true" className="pointer-events-none absolute inset-y-0 right-2 flex items-center text-xs font-normal text-app-subtle">{presentation.unit}</span>}
    </div>
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
