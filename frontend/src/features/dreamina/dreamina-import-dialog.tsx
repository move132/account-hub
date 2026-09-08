import { useRef, useState } from "react";
import * as Tabs from "radix-ui/tabs";
import { Badge, Button, DataTable, Dialog, Field, FileInput, Pagination, Textarea, type Column, useToast } from "../../components/ui";
import { api, errorMessage } from "../../lib/api";

interface PreviewRow {
  line: number;
  email: string;
  password_masked: string;
  session_id_masked: string;
  note: string;
  result_status: "new" | "duplicate" | "invalid";
  duplicate_reason: string;
  errors: string[];
}

interface ImportPreview extends Record<string, unknown> {
  rows: PreviewRow[];
  total: number;
  valid: number;
  invalid: number;
  duplicates: number;
}

interface ImportResult extends Record<string, unknown> {
  job: { id: string } | null;
  accepted: number;
  skipped: number;
}

const previewColumns: Array<Column<PreviewRow & { id: string }>> = [
  { key: "line", header: "行号", render: (row) => row.line },
  { key: "email", header: "账户邮箱", render: (row) => <span className="break-all">{row.email || "—"}</span> },
  { key: "password", header: "账户密码", render: (row) => <span className="font-mono text-xs">{row.password_masked || "—"}</span> },
  { key: "session", header: "Session ID", render: (row) => <span className="font-mono text-xs">{row.session_id_masked || "—"}</span> },
  { key: "note", header: "描述", className: "max-w-48 break-words", render: (row) => row.note || "—" },
  {
    key: "result", header: "状态", className: "min-w-36 max-w-64", render: (row) => <div className="grid gap-1">
      <Badge tone={row.result_status === "invalid" ? "danger" : row.result_status === "duplicate" ? "warning" : "success"}>
        {row.result_status === "invalid" ? "格式错误" : row.result_status === "duplicate" ? "重复，跳过" : "新增"}
      </Badge>
      {row.errors.map((error, index) => <p key={index} className="text-xs text-app-danger">{error}</p>)}
      {row.duplicate_reason ? <p className="text-xs text-app-subtle">{row.duplicate_reason}</p> : null}
    </div>,
  },
];

export function DreaminaImportDialog({ onClose, onImported }: {
  onClose: () => void;
  onImported: (jobId: string | null, skipped: number) => void;
}) {
  const [method, setMethod] = useState("paste");
  const [content, setContent] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [page, setPage] = useState(1);
  const [pending, setPending] = useState<"preview" | "import" | null>(null);
  const pendingRef = useRef(false);
  const { notify } = useToast();
  const hasContent = method === "paste" ? Boolean(content.trim()) : Boolean(file);

  const run = async (action: "preview" | "import") => {
    if (pendingRef.current || !hasContent) return;
    pendingRef.current = true;
    setPending(action);
    try {
      const body = new FormData();
      if (method === "upload" && file) body.set("file", file);
      else body.set("content", content);
      const checked = await api<ImportPreview>("/api/v1/dreamina-imports?dry_run=true", { method: "POST", body });
      if (action === "preview" || checked.invalid > 0) {
        setPreview(checked);
        setPage(1);
        if (action === "import" && checked.invalid > 0) {
          notify("请修正格式错误后再导入", `有 ${checked.invalid} 行内容需要修改`, "danger");
        }
        return;
      }
      const result = await api<ImportResult>("/api/v1/dreamina-imports?dry_run=false", {
        method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, body,
      });
      notify(result.job ? "导入任务已创建" : "重复 Session 已跳过",
        result.job ? `导入 ${result.accepted} 条，跳过 ${result.skipped} 条重复记录` : `已跳过 ${result.skipped} 条重复记录`);
      onImported(result.job?.id ?? null, result.skipped);
      onClose();
    } catch (error) {
      notify(action === "preview" ? "预览失败" : "导入失败", errorMessage(error), "danger");
    } finally {
      pendingRef.current = false;
      setPending(null);
    }
  };

  const previewRows = preview?.rows.slice((page - 1) * 50, page * 50).map((row) => ({ ...row, id: String(row.line) })) ?? [];

  return <Dialog open onOpenChange={(open) => { if (!open && !pendingRef.current) onClose(); }}
    title={preview ? `导入预览（${preview.total} 条）` : "批量导入 Session"}
    description={preview ? "确认新增和重复的 Session，格式错误的行需返回修改。" : "每行一个 Session，可粘贴内容或上传文件。"}
    contentClassName={preview ? "w-[min(96vw,1080px)]" : undefined}>
    {preview ? <div className="grid gap-4">
      <div className="flex flex-wrap gap-2" role="status">
        <Badge tone="success">新增 {preview.valid} 条</Badge>
        <Badge tone="warning">重复 {preview.duplicates} 条</Badge>
        <Badge tone={preview.invalid > 0 ? "danger" : "neutral"}>格式错误 {preview.invalid} 条</Badge>
      </div>
      <div className="max-h-[48vh] overflow-auto rounded-lg border border-app-border">
        <DataTable columns={previewColumns} rows={previewRows} />
      </div>
      {preview.total > 50 ? <Pagination page={page} totalPages={Math.ceil(preview.total / 50)} onPageChange={setPage} /> : null}
      <div className="flex justify-end gap-2">
        <Button disabled={Boolean(pending)} onClick={() => setPreview(null)}>返回编辑</Button>
        <Button variant="primary" disabled={Boolean(pending) || preview.invalid > 0 || preview.valid === 0} onClick={() => void run("import")}>
          {pending === "import" ? "导入中…" : "确认导入"}
        </Button>
      </div>
    </div> : null}
    <div className={preview ? "hidden" : "grid gap-5"}>
      <Tabs.Root value={method} onValueChange={setMethod}>
        <Tabs.List aria-label="Session 导入方式" className="mb-5 flex gap-5 border-b border-app-border">
          {[{ value: "paste", label: "粘贴内容" }, { value: "upload", label: "上传文件" }].map((tab) => <Tabs.Trigger key={tab.value} value={tab.value} asChild>
            <Button variant="ghost" disabled={Boolean(pending)} className="rounded-none border-0 border-b-2 border-transparent px-0 pb-3 data-[state=active]:border-app-info data-[state=active]:text-app-info">
              {tab.label}
            </Button>
          </Tabs.Trigger>)}
        </Tabs.List>
        <div className="mb-3 grid gap-2">
          <p className="text-sm font-semibold">Session 列表</p>
          <div className="rounded-md bg-app-muted p-3 text-sm leading-6">
            <p>1. 每行一个 Session</p>
            <p className="break-words font-mono">2. 每行格式：[账户邮箱],[账户密码],[Session ID],[描述(可选)]</p>
          </div>
        </div>
        <Tabs.Content value="paste" className="outline-none">
          <Textarea aria-label="Session 内容" name="dreamina-import-content" autoComplete="off" spellCheck={false}
            rows={8} className="min-h-48 text-sm" placeholder="请粘贴 Session 内容..." value={content}
            disabled={Boolean(pending)} onChange={(event) => setContent(event.target.value)} />
        </Tabs.Content>
        <Tabs.Content value="upload" forceMount hidden={method !== "upload"} className="min-h-48">
          <Field label="选择文件" hint="支持 .txt 和 .csv 格式文件，每行格式同上，无需表头。">
            <FileInput aria-label="选择 Session 文件" name="dreamina-import-file" accept=".txt,.csv,text/plain,text/csv"
              disabled={Boolean(pending)} onChange={(event) => {
                const selected = event.target.files?.[0] ?? null;
                if (selected && !/\.(txt|csv)$/i.test(selected.name)) {
                  notify("文件格式不支持", "请选择 .txt 或 .csv 文件", "danger");
                  event.target.value = "";
                  setFile(null);
                  return;
                }
                setFile(selected);
              }} />
          </Field>
        </Tabs.Content>
      </Tabs.Root>
      <p className="rounded-md bg-app-info/10 px-4 py-3 text-sm leading-6 text-app-info">
        批量导入将跳过重复的 Session ID。如果账户邮箱重复但 Session ID 不同，将作为新 Session 添加。
      </p>
      <div className="flex justify-end gap-2">
        <Button disabled={Boolean(pending)} onClick={onClose}>取消</Button>
        <Button disabled={!hasContent || Boolean(pending)} className="border-app-info bg-app-info text-white hover:bg-app-info/90" onClick={() => void run("preview")}>
          {pending === "preview" ? "预览中…" : "预览"}
        </Button>
        <Button variant="primary" disabled={!hasContent || Boolean(pending)} onClick={() => void run("import")}>
          {pending === "import" ? "导入中…" : "导入"}
        </Button>
      </div>
    </div>
  </Dialog>;
}
