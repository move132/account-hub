import type { ReactNode } from "react";
import { Button, Checkbox, Select, cn } from "./primitives";
export function Card({ children, className }: {
  children: ReactNode;
  className?: string;
}) {
  return <section className={cn("rounded-lg border border-app-border bg-app-surface", className)}>
    {children}</section>;
}
export function CardHeader({ title, description, action }: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return <header className="flex items-start justify-between gap-3 border-b border-app-border px-4 py-3">
    <div>
      <h2 className="font-semibold">
        {title}</h2>
      {description && <p className="mt-1 text-xs text-app-subtle">
        {description}</p>}</div>
    {action}</header>;
}
export function Badge({ children, tone = "neutral" }: {
  children: ReactNode;
  tone?: "neutral" | "success" | "warning" | "danger";
}) {
  return <span className={cn("inline-flex w-fit max-w-full shrink-0 items-center justify-self-start self-start whitespace-nowrap rounded-full border px-1.5 py-0.5 text-xs", tone === "neutral" && "border-app-border bg-app-muted text-app-subtle", tone === "success" && "border-app-success/30 bg-app-success/10 text-app-success", tone === "warning" && "border-app-warning/30 bg-app-warning/10 text-app-warning", tone === "danger" && "border-app-danger/30 bg-app-danger/10 text-app-danger")}>
    {children}</span>;
}
export function EmptyState({ title = "暂无数据", description }: {
  title?: string;
  description?: string;
}) {
  return <div className="grid min-h-32 place-items-center p-5 text-center">
    <div>
      <p className="font-medium">
        {title}</p>
      {description && <p className="mt-1 text-sm text-app-subtle">
        {description}</p>}</div>
  </div>;
}
export function PageHeader({ title, description, actions }: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
    <div className="min-w-0">
      <h1 className="text-balance text-xl font-semibold tracking-tight">
        {title}</h1>
      {description && <p className="mt-1 break-words text-sm text-app-subtle">
        {description}</p>}</div>
    <div className="flex flex-wrap gap-2">
      {actions}</div>
  </div>;
}
const pageSizeOptions = [10, 20, 50, 100, 200, 500].map((value) => ({ value: String(value), label: `${value} / 页` }));

export function Pagination({ page, totalPages, onPageChange, pageSize, onPageSizeChange }: {
  page: number;
  totalPages: number;
  onPageChange: (page: number) => void;
  pageSize?: number;
  onPageSizeChange?: (pageSize: number) => void;
}) {
  return <div className="flex flex-wrap items-center justify-between gap-2 border-t border-app-border px-4 py-2 text-xs text-app-subtle">
    <span>第 {page} / {Math.max(totalPages, 1)} 页</span>
    <div className="flex items-center gap-2">
      {pageSize && onPageSizeChange && <Select ariaLabel="每页条数" value={String(pageSize)} onValueChange={(value) => onPageSizeChange(Number(value))} options={pageSizeOptions} />}
      <Button size="sm" disabled={page <= 1} onClick={() => onPageChange(page - 1)}>上一页</Button>
      <Button size="sm" disabled={page >= totalPages} onClick={() => onPageChange(page + 1)}>下一页</Button>
    </div>
  </div>;
}
export interface Column<T> {
  key: string;
  header: string;
  className?: string;
  render: (row: T) => ReactNode;
}
export function DataTable<T extends {
  id: string;
}>({ columns, rows, selected, onSelectionChange, isRowSelectable, emptyText = "暂无数据" }: {
  columns: Array<Column<T>>;
  rows: T[];
  selected?: Set<string>;
  onSelectionChange?: (selected: Set<string>) => void;
  isRowSelectable?: (row: T) => boolean;
  emptyText?: string;
}) {
  const selectable = Boolean(selected && onSelectionChange);
  const selectableRows = isRowSelectable ? rows.filter(isRowSelectable) : rows;
  const allSelected = selectable && selectableRows.length > 0 && selectableRows.every((row) => selected!.has(row.id));
  const toggleAll = () => onSelectionChange?.(allSelected ? new Set() : new Set(selectableRows.map((row) => row.id)));
  return <div className="overflow-x-auto">
    <table className="w-full min-w-[880px] border-collapse text-left tabular-nums">
      <thead>
        <tr className="border-b border-app-border text-xs uppercase tracking-wide text-app-subtle">
          {selectable && <th className="w-10 px-4 py-2">
            <Checkbox label="选择全部" checked={allSelected} disabled={selectableRows.length === 0} onCheckedChange={toggleAll} />
          </th>}{columns.map((column) => <th key={column.key} className={cn("px-3 py-2 font-medium", column.className)}>
            {column.header}</th>)}</tr>
      </thead>
      <tbody>
        {rows.map((row) => {
          const rowSelectable = isRowSelectable?.(row) ?? true;
          return <tr key={row.id} className="border-b border-app-border/70 transition-colors last:border-0 hover:bg-app-hover/50">
            {selectable && <td className="px-4 py-3">
              <Checkbox label={`选择 ${row.id}`} checked={selected!.has(row.id)} disabled={!rowSelectable} onCheckedChange={(checked) => {
                const next = new Set(selected); if (checked)
                  next.add(row.id);
                else
                  next.delete(row.id); onSelectionChange?.(next);
              }} />
            </td>}{columns.map((column) => <td key={column.key} className={cn("px-3 py-3 align-middle", column.className)}>
              {column.render(row)}</td>)}</tr>;
        })}</tbody>
    </table>
    {rows.length === 0 && <EmptyState title={emptyText} />}</div>;
}
export function StatCard({ label, value, hint }: {
  label: string;
  value: string | number;
  hint?: string;
}) {
  return <Card className="p-4">
    <p className="text-xs text-app-subtle">
      {label}</p>
    <p className="mt-1.5 text-xl font-medium tracking-tight">
      {value}</p>
    {hint && <p className="mt-1 text-xs text-app-subtle">
      {hint}</p>}</Card>;
}
