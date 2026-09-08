export interface Envelope<T extends Record<string, unknown> = Record<string, unknown>> {
  code: number
  success: boolean
  data: T
  msg: string
  ts: number
}

export class ApiError extends Error {
  code: number
  errorCode?: string
  requestId?: string

  constructor(response: Envelope) {
    super(response.msg)
    this.code = response.code
    this.errorCode = typeof response.data.error_code === "string" ? response.data.error_code : undefined
    this.requestId = typeof response.data.request_id === "string" ? response.data.request_id : undefined
  }
}

function csrfToken() {
  return document.cookie
    .split("; ")
    .find((entry) => entry.startsWith("account_csrf="))
    ?.split("=")
    .slice(1)
    .join("=") ?? ""
}

export async function api<T extends Record<string, unknown>>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const method = (init.method ?? "GET").toUpperCase()
  if (!(init.body instanceof FormData) && init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json")
  }
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    headers.set("X-CSRF-Token", csrfToken())
  }
  const response = await fetch(path, { ...init, headers, credentials: "include" })
  const envelope = (await response.json()) as Envelope<T>
  if (!response.ok || !envelope.success) throw new ApiError(envelope)
  return envelope.data
}

export function jsonBody(value: unknown): Pick<RequestInit, "body"> {
  return { body: JSON.stringify(value) }
}

export function errorMessage(error: unknown) {
  if (error instanceof ApiError) {
    return error.requestId ? `${error.message} · ${error.requestId}` : error.message
  }
  return error instanceof Error ? error.message : "操作失败"
}
