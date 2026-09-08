export interface StorageUsage {
  used_bytes: number;
  allowed_bytes: number;
  remaining_bytes: number;
  is_over_limit: boolean;
  plan_type?: string;
  breakdown_by_file_type: Array<{ file_type: string; used_bytes: number; count: number }>;
}

export interface LibraryFile {
  id: string;
  name: string;
  file_id: string;
  parent_directory_id: string;
  file_size_bytes: number;
  updated_at?: string;
}

export interface LibraryPage extends Record<string, unknown> {
  items: LibraryFile[];
  cursor?: string;
}

export interface LibraryDeleteResult extends Record<string, unknown> {
  deleted_library_file_ids: string[];
  deleted_count: number;
  failure_count: number;
  failures: Array<{ library_file_id: string; file_name: string; message: string }>;
}

export interface ConversationSummary {
  id: string;
  title: string;
  create_time?: string;
  update_time?: string;
}

export interface ConversationPage extends Record<string, unknown> {
  items: ConversationSummary[];
  total: number | null;
  next_offset: number;
  has_more: boolean;
}

export interface ConversationMessage {
  id: string;
  role: string;
  author: string;
  content: string;
  create_time?: string;
  assets: Array<{ file_id: string }>;
}

export interface ConversationDetail extends ConversationSummary {
  messages: ConversationMessage[];
}

export function mergeContentItems<T extends { id: string }>(current: T[], incoming: T[]): T[] {
  const items = new Map(current.map((item) => [item.id, item]));
  for (const item of incoming) {
    if (item.id) items.set(item.id, item);
  }
  return [...items.values()];
}

export function formatBytes(value?: number): string {
  if (value === undefined || !Number.isFinite(value) || value < 0) return "—";
  if (value < 1024) return `${Math.round(value)} B`;
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${Number((value / 1024 ** index).toFixed(2))} ${units[index]}`;
}

export function usagePercentage(usage: StorageUsage): number {
  return usage.allowed_bytes > 0 ? Math.max(0, Math.min(100, usage.used_bytes / usage.allowed_bytes * 100)) : 0;
}

export function canDeleteLibraryFile(file: LibraryFile): boolean {
  return /^libfile_[a-zA-Z0-9_-]+$/.test(file.id) && /^file_[a-zA-Z0-9]+$/.test(file.file_id) && Boolean(file.parent_directory_id?.trim() && file.name?.trim());
}

export function contentPath(tokenId: string): string {
  return `/api/v1/tokens/${encodeURIComponent(tokenId)}`;
}
