export interface DreaminaView extends Record<string, unknown> {
  id: string
  email: string
  password_masked: string
  session_id_masked: string
  has_session_id: boolean
  session_expires_at?: string
  status: "enabled" | "disabled"
  check_state: "unchecked" | "valid" | "invalid" | "error"
  credit_balance?: string
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

export interface DreaminaEditView extends DreaminaView {
  password: string
  session_id: string
}

export interface Attempt extends Record<string, unknown> {
  id: string
  operation: string
  status: string
  error_code?: string
  error_message?: string
  started_at: number
}

export const formatTime = (value?: number | string) => {
  if (!value) return ""
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? String(value) : new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium", timeZone: "Asia/Shanghai" }).format(date)
}
