import { describe, expect, it } from "vitest"
import { ApiError } from "../../lib/api"
import { claimErrorFeedback, claimFeedback, claimOperationFromError, readClaimOperation } from "./claim-feedback"

function apiFailure(status: number, errorCode: string, operation?: unknown) {
  return new ApiError({
    code: status,
    success: false,
    data: { error_code: errorCode, details: operation === undefined ? {} : { operation } },
    msg: "领取接口返回业务结果",
    ts: 1,
  })
}

describe("credit claim feedback", () => {
  it("only reports success for a confirmed claim", () => {
    const feedback = claimFeedback(readClaimOperation({ status: "claimed", balance: "120", balance_refreshed: true }))
    expect(feedback).toMatchObject({ status: "claimed", title: "积分领取成功", tone: "success" })
    expect(feedback.description).toContain("当前余额：120")
  })

  it("does not present already received credits as a new successful claim", () => {
    const feedback = claimFeedback(readClaimOperation({ status: "already_claimed", provider_code: "3001" }))
    expect(feedback).toMatchObject({ status: "already_claimed", title: "积分已领取，无需重复领取", tone: "info" })
    expect(feedback.title).not.toContain("成功")
  })

  it.each(["balance_refresh", "persistence"])("keeps a completed claim explicit when %s has a warning", (stage) => {
    const feedback = claimFeedback(readClaimOperation({
      status: "claimed", balance: "0", balance_refreshed: true,
      warning: { stage, code: "INTERNAL_ERROR", message: "领取已完成，余额保存失败，请查询余额" },
    }))
    expect(feedback).toMatchObject({ status: "claimed", title: "积分领取已完成", tone: "warning" })
    expect(feedback.description).toContain("余额保存失败")
    expect(feedback.description).toContain("当前余额：0")
    expect(feedback.title).not.toContain("失败")
  })

  it("shows the provider rejection from a 422 error's operation details", () => {
    const feedback = claimErrorFeedback(apiFailure(422, "CLAIM_REJECTED", {
      status: "rejected", provider_code: "4007", reason: "账号未满足领取条件", balance: "20", balance_refreshed: true,
    }))
    expect(feedback).toMatchObject({ status: "rejected", title: "本次领取被拒绝", tone: "danger" })
    expect(feedback.description).toContain("账号未满足领取条件")
    expect(feedback.description).toContain("4007")
    expect(feedback.description).toContain("当前余额：20")
  })

  it("warns against resubmission when a 502 carries an unconfirmed result", () => {
    const error = apiFailure(502, "CLAIM_OUTCOME_UNCONFIRMED", {
      status: "unconfirmed", reason: "上游响应无法确认结果", balance: "30", balance_refreshed: true,
    })
    expect(claimOperationFromError(error)?.balance).toBe("30")
    const feedback = claimErrorFeedback(error)
    expect(feedback).toMatchObject({ status: "unconfirmed", title: "领取结果待确认", tone: "warning" })
    expect(feedback.description).toContain("勿重复领取")
    expect(feedback.description).toContain("当前余额：30")
    expect(feedback.description).toContain("已查询余额，请核对到账")
    expect(feedback.description).not.toContain("请先查询余额")
    expect(feedback.title).not.toContain("失败")
  })

  it.each([undefined, null, {}, { status: "future_status" }, { status: true }])("treats missing or malformed HTTP 200 operation data as unconfirmed: %j", (value) => {
    const feedback = claimFeedback(readClaimOperation(value))
    expect(feedback.status).toBe("unconfirmed")
    expect(feedback.tone).toBe("warning")
    expect(feedback.title).not.toContain("成功")
  })

  it.each(["explicit_rejection", "conflicting_signals", "unrecognized_response", "invalid_shape", "transport_error"])("translates the provider reason %s for display", (reason) => {
    const feedback = claimFeedback(readClaimOperation({ status: reason === "explicit_rejection" ? "rejected" : "unconfirmed", reason }))
    expect(feedback.description).not.toContain(reason)
    expect(feedback.description).toContain("上游")
  })

  it("keeps the safe provider code visible for an unconfirmed operation", () => {
    const feedback = claimErrorFeedback(apiFailure(502, "CLAIM_OUTCOME_UNCONFIRMED", {
      status: "unconfirmed", provider_code: "9999", reason: "unrecognized_response", balance_refreshed: true,
    }))
    expect(feedback.description).toContain("上游代码：9999")
    expect(feedback.description).toContain("已查询余额，请核对到账")
    expect(feedback.description).not.toContain("unrecognized_response")
    expect(feedback).toMatchObject({ status: "unconfirmed", tone: "warning" })
  })

  it("does not display an unrefreshed balance as the current balance", () => {
    const feedback = claimFeedback(readClaimOperation({ status: "unconfirmed", balance: "99", balance_refreshed: false }))
    expect(feedback.description).not.toContain("当前余额")
  })

  it("treats network loss and non-structured upstream errors as uncertain", () => {
    for (const error of [new TypeError("Failed to fetch"), apiFailure(502, "UPSTREAM_TIMEOUT")]) {
      const feedback = claimErrorFeedback(error)
      expect(feedback.status).toBe("unconfirmed")
      expect(feedback.description).toContain("勿重复领取")
    }
  })
})
