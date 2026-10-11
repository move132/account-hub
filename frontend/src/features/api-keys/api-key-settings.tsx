import { useCallback, useEffect, useState, type FormEvent } from "react";
import Copy from "lucide-react/dist/esm/icons/copy.mjs";
import Plus from "lucide-react/dist/esm/icons/plus.mjs";
import Trash2 from "lucide-react/dist/esm/icons/trash-2.mjs";
import { Button, Card, CardHeader, DataTable, Dialog, Field, Input, Spinner, type Column, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
import { copyText } from "../../lib/clipboard";

interface APIKeyView {
  id: string;
  name: string;
  key_masked: string;
  created_at: number;
  last_used_at: number | null;
}

interface KeySecret extends Record<string, unknown> {
  api_key: APIKeyView;
  secret: string;
}

const timeFormatter = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium", timeZone: "Asia/Shanghai" });

export function ApiKeySettings() {
  const [keys, setKeys] = useState<APIKeyView[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [saving, setSaving] = useState(false);
  const [created, setCreated] = useState<KeySecret | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<APIKeyView | null>(null);
  const [deleting, setDeleting] = useState(false);
  const { notify } = useToast();

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setLoadError("");
    try {
      const data = await api<{ api_keys: APIKeyView[] }>("/api/v1/settings", { signal });
      if (!signal?.aborted) setKeys(data.api_keys);
    } catch (error) {
      if (!signal?.aborted) setLoadError(errorMessage(error));
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const changeCreateOpen = (open: boolean) => {
    if (saving) return;
    setCreateOpen(open);
    if (!open) {
      setCreated(null);
      setName("");
    }
  };

  const createKey = async (event: FormEvent) => {
    event.preventDefault();
    if (saving || !name.trim()) return;
    setSaving(true);
    try {
      const result = await api<KeySecret>("/api/v1/settings", {
        method: "PATCH",
        ...jsonBody({ api_key_action: { action: "create", name: name.trim() } }),
      });
      setCreated(result);
      setKeys((current) => [result.api_key, ...current]);
      notify("API Key 已生成");
    } catch (error) {
      notify("未能取得创建结果", errorMessage(error), "danger");
    } finally {
      setSaving(false);
    }
  };

  const copySecret = async (secret: string) => {
    try {
      await copyText(secret);
      notify("Key 已复制");
    } catch {
      notify("复制失败", "请选中密钥手动复制", "danger");
    }
  };

  const deleteKey = async () => {
    if (!deleteTarget || deleting) return;
    const id = deleteTarget.id;
    setDeleting(true);
    try {
      await api("/api/v1/settings", { method: "PATCH", ...jsonBody({ api_key_action: { action: "delete", id } }) });
      setKeys((current) => current.filter((item) => item.id !== id));
      setDeleteTarget(null);
      notify("API Key 已删除", "该 Key 已失效");
    } catch (error) {
      notify("删除失败", errorMessage(error), "danger");
    } finally {
      setDeleting(false);
    }
  };

  const deleteButton = (item: APIKeyView) => <Button size="sm" variant="ghost" className="shrink-0 text-app-danger" aria-label={`删除“${item.name}”的 Key`} title="删除 Key" onClick={() => setDeleteTarget(item)}>
    <Trash2 aria-hidden="true" className="size-3.5" /></Button>;

  const columns: Array<Column<APIKeyView>> = [
    { key: "name", header: "名称", className: "max-w-[300px]", render: (item) => <span className="line-clamp-2 max-w-[300px] break-words font-medium" title={item.name}>{item.name}</span> },
    { key: "key", header: "Key", render: (item) => <code className="whitespace-nowrap text-xs text-app-subtle">{item.key_masked}</code> },
    { key: "created_at", header: "创建时间", render: (item) => <span className="whitespace-nowrap text-xs text-app-subtle">{timeFormatter.format(item.created_at)}</span> },
    { key: "last_used_at", header: "最近使用时间", render: (item) => <span className="whitespace-nowrap text-xs text-app-subtle">{item.last_used_at == null ? "未使用" : timeFormatter.format(item.last_used_at)}</span> },
    { key: "actions", header: "操作", className: "w-16", render: deleteButton },
  ];

  return <>
    <Card className="min-w-0">
      <CardHeader title="API Key 管理" description="用于调用 Token 管理接口，完整 Key 仅在创建时显示。" action={
        <Button size="sm" className="shrink-0" onClick={() => changeCreateOpen(true)} disabled={loading || Boolean(loadError)}>
          <Plus aria-hidden="true" className="size-3.5" />新增 Key</Button>
      } />
      {loading ? <div className="grid min-h-20 place-items-center"><Spinner /></div> : loadError ? <div className="flex flex-wrap items-center justify-between gap-3 p-4">
        <p role="alert" className="text-sm text-app-danger">Key 列表加载失败：{loadError}</p>
        <Button onClick={() => void load()}>重试</Button>
      </div> : keys.length === 0 ? <p className="px-4 py-6 text-center text-xs text-app-subtle">暂无 API Key，点击“新增 Key”创建</p> : <>
        <div className="hidden lg:block [&_table]:min-w-[640px] [&_td]:py-1.5 [&_th]:py-2">
          <DataTable columns={columns} rows={keys} />
        </div>
        <div className="divide-y divide-app-border lg:hidden">
          {keys.map((item) => <div key={item.id} className="p-3 sm:px-4">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="break-words font-medium">{item.name}</p>
                <code className="text-xs text-app-subtle">{item.key_masked}</code>
              </div>
              {deleteButton(item)}
            </div>
            <dl className="mt-2 grid grid-cols-2 gap-2 text-xs">
              <div><dt className="text-app-subtle">创建时间</dt><dd className="mt-0.5 tabular-nums">{timeFormatter.format(item.created_at)}</dd></div>
              <div><dt className="text-app-subtle">最近使用时间</dt><dd className="mt-0.5 tabular-nums">{item.last_used_at == null ? "未使用" : timeFormatter.format(item.last_used_at)}</dd></div>
            </dl>
          </div>)}
        </div>
      </>}
      <p className="border-t border-app-border px-3 py-2 text-xs text-app-subtle sm:px-4">
        请求头使用 <code>Authorization: Bearer &lt;API Key&gt;</code>，删除后立即失效。
      </p>
    </Card>
    <Dialog open={createOpen} onOpenChange={changeCreateOpen} title={created ? "API Key 已生成" : "新增 API Key"}
      description={created ? "完整 Key 仅显示一次，请立即复制保存。关闭后将无法再次查看。" : "输入名称，便于识别这个 Key 的用途。"}>
      {created ? <div className="grid gap-4">
        <Field label={created.api_key.name}>
          <Input aria-label="完整 API Key" readOnly value={created.secret} className="font-mono" onFocus={(event) => event.currentTarget.select()} />
        </Field>
        <div className="flex justify-end gap-2">
          <Button onClick={() => changeCreateOpen(false)}>完成</Button>
          <Button variant="primary" onClick={() => void copySecret(created.secret)}><Copy aria-hidden="true" className="size-3.5" />复制 Key</Button>
        </div>
      </div> : <form className="grid gap-4" onSubmit={(event) => void createKey(event)}>
        <Field label="名称" hint="最多 15 个字符">
          <Input autoFocus required maxLength={15} autoComplete="off" placeholder="例如：自动化脚本" value={name} disabled={saving} onChange={(event) => setName(event.target.value)} />
        </Field>
        <div className="flex justify-end gap-2">
          <Button type="button" disabled={saving} onClick={() => changeCreateOpen(false)}>取消</Button>
          <Button type="submit" variant="primary" disabled={saving || !name.trim()}>{saving ? "保存中…" : "保存"}</Button>
        </div>
      </form>}
    </Dialog>
    <Dialog open={Boolean(deleteTarget)} onOpenChange={(open) => { if (!open && !deleting) setDeleteTarget(null); }} title="删除 API Key"
      description={`删除“${deleteTarget?.name ?? ""}”后，使用该 Key 的后续请求将立即失效。此操作无法撤销。`}>
      <div className="flex justify-end gap-2">
        <Button disabled={deleting} onClick={() => setDeleteTarget(null)}>取消</Button>
        <Button variant="danger" disabled={deleting} onClick={() => void deleteKey()}>{deleting ? "删除中…" : "删除"}</Button>
      </div>
    </Dialog>
  </>;
}
