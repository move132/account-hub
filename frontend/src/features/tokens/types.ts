export interface TokenView extends Record<string, unknown> {
  id: string
  name: string
  email: string
  access_token_masked: string
  session_token_masked: string
  has_session_token: boolean
  access_expires_at?: string
  status: "enabled" | "disabled"
  check_state: "unchecked" | "valid" | "invalid" | "error"
  note: string
  next_refresh_at?: number
  last_checked_at?: number
  last_refreshed_at?: number
  last_error_code?: string
  last_error_message?: string
  version: number
  created_at: number
  updated_at: number
}

export interface TokenEditView extends TokenView {
  access_token: string
  session_token: string
}

export interface Attempt extends Record<string, unknown> {
  id: string
  operation: string
  status: string
  error_code?: string
  error_message?: string
  started_at: number
  completed_at?: number
}

export function formatTime(value?: number | string) {
  if (!value) return "—"
  const date = typeof value === "number" ? new Date(value) : new Date(value)
  return Number.isNaN(date.getTime()) ? String(value) : new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium", timeZone: "Asia/Shanghai" }).format(date)
}
