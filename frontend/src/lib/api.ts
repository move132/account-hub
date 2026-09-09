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

export async function downloadApiFile(path: string, init: RequestInit = {}, fallbackFilename = "download.bin") {
  const headers = new Headers(init.headers)
  const method = (init.method ?? "GET").toUpperCase()
  if (!(init.body instanceof FormData) && init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json")
  }
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    headers.set("X-CSRF-Token", csrfToken())
  }
  const response = await fetch(path, { ...init, headers, credentials: "include" })
  if (!response.ok) {
    try {
      const envelope = (await response.json()) as Envelope
      throw new ApiError(envelope)
    } catch (error) {
      if (error instanceof ApiError) throw error
      throw new Error(`下载失败（HTTP ${response.status}）`)
    }
  }
  const disposition = response.headers.get("Content-Disposition") ?? ""
  const encoded = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1]
  const plain = disposition.match(/filename="?([^";]+)"?/i)?.[1]
  let filename = fallbackFilename
  try {
    filename = encoded ? decodeURIComponent(encoded) : plain || fallbackFilename
  } catch {
    filename = plain || fallbackFilename
  }
  filename = filename.split(/[\\/]/).pop()?.replace(/[\u0000-\u001f]/g, "") || fallbackFilename
  return { blob: await response.blob(), filename }
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
