import { useRef, useState } from "react";
import * as Tabs from "radix-ui/tabs";
import { Badge, Button, DataTable, Dialog, Field, FileInput, Pagination, Textarea, type Column, useToast } from "../../components/ui";
import { api, errorMessage } from "../../lib/api";

interface PreviewRow {
  line: number;
  name: string;
  note: string;
  access_token_masked: string;
  session_token_masked: string;
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
  { key: "name", header: "名称", className: "min-w-40", render: (row) => row.name },
  {
    key: "access", header: "Access Token", className: "min-w-52", render: (row) => <span className="font-mono text-xs text-app-subtle">
      {row.access_token_masked || "—"}
    </span>,
  },
  {
    key: "session", header: "Session Token", className: "min-w-52", render: (row) => <span className="font-mono text-xs text-app-subtle">
      {row.session_token_masked || "—"}
    </span>,
  },
  { key: "note", header: "描述", className: "max-w-48 break-words", render: (row) => row.note || "—" },
  {
    key: "result", header: "状态", className: "min-w-44 max-w-64", render: (row) => <div className="grid gap-1">
      <Badge tone={row.result_status === "invalid" ? "danger" : row.result_status === "duplicate" ? "warning" : "success"}>
        {row.result_status === "invalid" ? "格式错误" : row.result_status === "duplicate" ? "重复，跳过" : "新增"}
      </Badge>
      {row.duplicate_reason ? <p className="text-xs text-app-subtle">{row.duplicate_reason}</p> : null}
      {row.errors.map((message, index) => <p key={index} className="text-xs text-app-danger">{message}</p>)}
    </div>,
  },
];

export function isSupportedTokenImportFilename(filename: string) {
  return /\.(txt|csv)$/i.test(filename);
}

export function TokenImportDialog({ onClose, onImported }: {
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

  const requestBody = () => {
    const body = new FormData();
    if (method === "upload" && file) body.set("file", file);
    else body.set("content", content);
    return body;
  };

  const run = async (action: "preview" | "import") => {
    if (!hasContent || pendingRef.current) return;
    pendingRef.current = true;
    setPending(action);
    try {
      const checked = await api<ImportPreview>("/api/v1/token-imports?dry_run=true", { method: "POST", body: requestBody() });
      if (action === "preview" || checked.invalid > 0) {
        setPreview(checked);
        setPage(1);
        if (action === "import" && checked.invalid > 0) {
          notify("请修正格式错误后再导入", `有 ${checked.invalid} 行内容需要修改`, "danger");
        }
        return;
      }
      const result = await api<ImportResult>("/api/v1/token-imports?dry_run=false", {
        method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, body: requestBody(),
      });
      notify(result.job ? "Token 导入任务已创建" : "重复 Token 已跳过",
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
    title={preview ? `导入预览（${preview.total} 条）` : "批量导入 Token"}
    description={preview ? "重复的 Access Token 会自动跳过，格式错误的行需返回修改。" : "可粘贴 Token，也可上传 TXT/CSV 文件。"}
    contentClassName={preview ? "w-[min(96vw,1120px)]" : "w-[min(92vw,760px)]"}>
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
      <div className="flex justify-end gap-2 border-t border-app-border pt-4">
        <Button disabled={Boolean(pending)} onClick={() => setPreview(null)}>返回编辑</Button>
        <Button variant="primary" disabled={Boolean(pending) || preview.invalid > 0 || preview.valid === 0} onClick={() => void run("import")}>
          {pending === "import" ? "导入中…" : "确认导入"}
        </Button>
      </div>
    </div> : <div className="grid gap-5">
      <Tabs.Root value={method} onValueChange={setMethod}>
        <Tabs.List aria-label="Token 导入方式" className="mb-4 flex gap-1 border-b border-app-border">
          {[{ value: "paste", label: "粘贴内容" }, { value: "upload", label: "上传文件" }].map((tab) => <Tabs.Trigger key={tab.value} value={tab.value} asChild>
            <Button variant="ghost" disabled={Boolean(pending)} className="rounded-none border-0 border-b-2 border-transparent px-3 pb-2 pt-1 data-[state=active]:border-app-primary data-[state=active]:text-app-primary">
              {tab.label}
            </Button>
          </Tabs.Trigger>)}
        </Tabs.List>
        <div className="mb-3 grid gap-2">
          <p className="text-sm font-semibold">Token 列表</p>
          <div className="rounded-lg border border-app-border bg-app-muted px-3 py-2.5 text-sm leading-6">
            <p>1. 每行一个 Access Token；或</p>
            <p className="break-words font-mono">2. 每行格式：[名称],[Access Token],[Session Token],[描述(可选)]</p>
          </div>
        </div>
        <Tabs.Content value="paste" className="outline-none">
          <Textarea aria-label="Token 导入内容" name="token-import-content" autoComplete="off" spellCheck={false}
            rows={8} className="min-h-48 text-sm" placeholder="请粘贴 Token 内容..." value={content}
            disabled={Boolean(pending)} onChange={(event) => setContent(event.target.value)} />
        </Tabs.Content>
        <Tabs.Content value="upload" forceMount hidden={method !== "upload"} className="min-h-48">
          <Field label="选择文件" hint="支持 .txt 和 .csv 格式，每行格式同上，无需表头。">
            <FileInput aria-label="选择 Token 文件" name="token-import-file" accept=".txt,.csv,text/plain,text/csv"
              disabled={Boolean(pending)} onChange={(event) => {
                const selected = event.target.files?.[0] ?? null;
                if (selected && !isSupportedTokenImportFilename(selected.name)) {
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
      <p className="rounded-lg border border-app-primary/25 bg-app-primary/5 px-3 py-2.5 text-sm leading-6 text-app-subtle">
        批量导入按 Access Token 去重并跳过已有记录；名称相同但 Token 不同的记录仍会新增。
      </p>
      <div className="flex justify-end gap-2 border-t border-app-border pt-4">
        <Button disabled={Boolean(pending)} onClick={onClose}>取消</Button>
        <Button disabled={!hasContent || Boolean(pending)} onClick={() => void run("preview")}>
          {pending === "preview" ? "预览中…" : "预览"}
        </Button>
        <Button variant="primary" disabled={!hasContent || Boolean(pending)} onClick={() => void run("import")}>
          {pending === "import" ? "导入中…" : "导入"}
        </Button>
      </div>
    </div>}
  </Dialog>;
}
