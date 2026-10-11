import type { ToastTone } from "../../components/ui/toast"
import { ApiError, errorMessage } from "../../lib/api"

type ClaimStatus = "claimed" | "already_claimed" | "rejected" | "unconfirmed"

export interface ClaimOperation {
  status: ClaimStatus
  balance?: string
  provider_code?: string
  reason?: string
  balance_refreshed?: boolean
  warning?: { stage?: string; code?: string; message: string }
}

interface ClaimFeedback {
  status: ClaimStatus
  title: string
  description?: string
  tone: ToastTone
}

function object(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

// A successful HTTP exchange alone does not prove that credits were claimed.
export function readClaimOperation(value: unknown): ClaimOperation | undefined {
  const result = object(value)
  if (!result) return undefined
  const status = result.status
  if (status !== "claimed" && status !== "already_claimed" && status !== "rejected" && status !== "unconfirmed") return undefined
  const warning = object(result.warning)
  return {
    status,
    balance: typeof result.balance === "string" ? result.balance : undefined,
    provider_code: typeof result.provider_code === "string" ? result.provider_code : undefined,
    reason: typeof result.reason === "string" ? result.reason : undefined,
    balance_refreshed: typeof result.balance_refreshed === "boolean" ? result.balance_refreshed : undefined,
    warning: warning && typeof warning.message === "string" ? {
      message: warning.message,
      stage: typeof warning.stage === "string" ? warning.stage : undefined,
      code: typeof warning.code === "string" ? warning.code : undefined,
    } : undefined,
  }
}

export function claimOperationFromError(error: unknown): ClaimOperation | undefined {
  return error instanceof ApiError ? readClaimOperation(object(error.details)?.operation) : undefined
}


const claimReasons: Record<string, string> = {
  confirmed: "上游已确认本次领取",
  already_claimed: "当前领取周期内已领取",
  explicit_rejection: "上游拒绝了本次领取请求",
  conflicting_signals: "上游返回的信息不一致，无法确认领取结果",
  unrecognized_response: "上游返回的领取结果无法识别",
  invalid_shape: "上游响应格式无法识别",
  transport_error: "请求中断，无法确认上游是否完成领取",
}

function reasonText(reason?: string): string | undefined {
  return reason ? claimReasons[reason] ?? reason : undefined
}

export function claimFeedback(operation?: ClaimOperation): ClaimFeedback {
  const status = operation?.status ?? "unconfirmed"
  const warning = operation?.warning?.message.trim()
  const balance = operation?.balance?.trim()
  const balanceText = balance && operation?.balance_refreshed !== false ? `当前余额：${balance}` : undefined
  const description = (...parts: Array<string | undefined>) => parts.filter(Boolean).join("；") || undefined
  switch (status) {
    case "claimed":
      return {
        status,
        title: warning ? "积分领取已完成" : "积分领取成功",
        description: description(warning, balanceText),
        tone: warning ? "warning" : "success",
      }
    case "already_claimed":
      return {
        status,
        title: "积分已领取，无需重复领取",
        description: description(warning, balanceText),
        tone: warning ? "warning" : "info",
      }
    case "rejected":
      return {
        status,
        title: "本次领取被拒绝",
        description: description(reasonText(operation?.reason) || "上游拒绝了本次领取", operation?.provider_code ? `上游代码：${operation.provider_code}` : undefined, warning, balanceText),
        tone: "danger",
      }
    default:
      return {
        status,
        title: "领取结果待确认",
        description: description(warning || reasonText(operation?.reason) || "暂时无法确认领取结果", operation?.provider_code ? `上游代码：${operation.provider_code}` : undefined, operation?.balance_refreshed ? "已查询余额，请核对到账，勿重复领取" : "请先查询余额，勿重复领取", balanceText),
        tone: "warning",
      }
  }
}

export function claimErrorFeedback(error: unknown): ClaimFeedback {
  const operation = claimOperationFromError(error)
  if (operation) return claimFeedback(operation)
  if (error instanceof ApiError && error.errorCode === "CLAIM_REJECTED") {
    return claimFeedback({ status: "rejected", reason: error.message })
  }
  return claimFeedback({ status: "unconfirmed", reason: errorMessage(error) })
}
