package dreamina

import (
	"context"
	"fmt"
	"strings"
	"time"

	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/upstream"
)

// ClaimCredit submits once. A read-only balance check is independent of whether
// the provider confirmed, rejected, or returned an unrecognized claim result.
// A balance change alone never upgrades an unconfirmed claim to success.
func (s *Service) ClaimCredit(ctx context.Context, id string) (ClaimResult, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return ClaimResult{}, err
	}
	if record.SessionID == "" {
		return ClaimResult{}, apperror.New(apperror.Validation, "该账号没有 session_id")
	}
	attempt, err := s.newAttempt(ctx, record, "credit_claim")
	if err != nil {
		return ClaimResult{}, err
	}

	raw, claimErr := s.client.ClaimCredit(ctx, record.SessionID)
	decision := normalizeClaimDecision(raw)
	result := ClaimResult{Upstream: raw, RemoteCompleted: claimErr == nil,
		Status: decision.Status, ProviderCode: decision.ProviderCode, Reason: decision.Reason}
	if claimErr != nil {
		result.Status, result.Reason = ClaimUnconfirmed, "transport_error"
	}
	result.Claimed = result.Status == ClaimConfirmed

	var credit CreditResult
	var balanceErr error
	if result.RemoteCompleted {
		// Keep this auxiliary read bounded; never call ClaimCredit a second time.
		balanceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		credit, balanceErr = s.client.Check(balanceCtx, record.SessionID)
		cancel()
		if balanceErr == nil && strings.TrimSpace(credit.Balance) == "" {
			balanceErr = &upstream.Error{Code: upstream.InvalidResponse, Message: "余额查询未返回有效余额"}
		}
		if balanceErr == nil {
			result.Balance, result.BalanceRefreshed = credit.Balance, true
		} else {
			result.Warning = &OperationWarning{Stage: "balance_refresh", Code: upstream.Code(balanceErr), Message: result.auxiliaryMessage("余额暂未更新，请手动查询积分")}
		}
	}

	outcomeErr := claimErr
	if claimErr == nil {
		switch result.Status {
		case ClaimRejected:
			outcomeErr = &upstream.Error{Code: "CLAIM_REJECTED", Message: result.outcomeMessage()}
		case ClaimUnconfirmed:
			outcomeErr = &upstream.Error{Code: "CLAIM_OUTCOME_UNCONFIRMED", Message: result.outcomeMessage()}
		}
	}
	s.finishAttempt(&attempt, outcomeErr)
	if outcomeErr == nil && result.Warning != nil {
		attempt.ErrorCode, attempt.ErrorMessage = result.Warning.Code, result.Warning.Message
	}
	if result.RemoteCompleted {
		if result.Status == ClaimRejected || result.Status == ClaimUnconfirmed {
			// Only bounded code/category metadata is logged. Never log raw
			// provider bodies, messages, session credentials or passwords.
			s.logger.Warn("dreamina credit claim outcome", "account_id", id,
				"claim_status", result.Status, "provider_code", result.ProviderCode,
				"reason", result.Reason, "balance_refreshed", result.BalanceRefreshed)
		} else {
			s.logger.Info("dreamina credit claim outcome", "account_id", id,
				"claim_status", result.Status, "provider_code", result.ProviderCode,
				"balance_refreshed", result.BalanceRefreshed)
		}
	}

	writeCtx, cancel := completionContext(ctx)
	defer cancel()
	var persistErr error
	if result.RemoteCompleted {
		code, message := "", ""
		if balanceErr != nil {
			code, message = upstream.Code(balanceErr), "余额查询失败，请稍后查询积分"
		}
		_, persistErr = s.repository.CompleteCheck(writeCtx, record, checkState(balanceErr), result.Balance, code, message, attempt)
	} else {
		_, persistErr = s.repository.completeOperation(writeCtx, record, attempt, "updated_at = ?", *attempt.CompletedAt)
	}
	if persistErr != nil {
		s.logger.Error("dreamina claim outcome persistence failed", "account_id", id, "claim_status", result.Status, "error", persistErr)
		result.Warning = &OperationWarning{Stage: "persistence", Code: ErrorCode(persistErr), Message: result.auxiliaryMessage("本地结果保存失败，请刷新账号")}
		// Preserve known success or the primary rejected/unconfirmed result.
		return result, outcomeErr
	}
	result.Persisted = true
	return result, outcomeErr
}

func (r ClaimResult) auxiliaryMessage(message string) string {
	switch r.Status {
	case ClaimConfirmed:
		return "积分领取已完成，" + message
	case ClaimAlreadyReceived:
		return "积分已领取，" + message
	case ClaimRejected:
		return "本次领取被上游拒绝，" + message
	default:
		return "领取结果待确认，" + message
	}
}

func (r ClaimResult) outcomeMessage() string {
	var message string
	switch r.Status {
	case ClaimConfirmed:
		return "积分领取成功"
	case ClaimAlreadyReceived:
		return "积分已领取，无需重复领取"
	case ClaimRejected:
		message = "即梦拒绝了本次积分领取"
	default:
		message = "即梦返回的领取结果暂未识别"
		if r.Reason == "conflicting_signals" {
			message = "即梦返回的领取状态不一致"
		}
	}
	if r.ProviderCode != "" {
		message += fmt.Sprintf("（上游状态码：%s）", r.ProviderCode)
	}
	if r.Status == ClaimUnconfirmed {
		if r.BalanceRefreshed {
			return message + "；已自动查询当前余额，请核对到账情况，勿重复提交"
		}
		return message + "；余额也未能自动刷新，请先查询积分，勿重复提交"
	}
	return message
}
