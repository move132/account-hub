package dreamina

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"account-hub/internal/platform/upstream"
)

type ClaimStatus string

const (
	ClaimConfirmed       ClaimStatus = "claimed"
	ClaimAlreadyReceived ClaimStatus = "already_claimed"
	ClaimRejected        ClaimStatus = "rejected"
	ClaimUnconfirmed     ClaimStatus = "unconfirmed"
)

// ClaimDecision exposes bounded diagnostics, never provider messages or payloads.
type ClaimDecision struct {
	Status       ClaimStatus `json:"status"`
	ProviderCode string      `json:"provider_code,omitempty"`
	Reason       string      `json:"reason"`
}

type claimEvidence struct {
	positive      bool
	receivedFalse bool
	rejected      bool
	already       bool
	unknown       bool
}

// normalizeClaimDecision requires recognizable business evidence. A successful
// exchange, provider message, or balance change alone cannot confirm a claim.
func normalizeClaimDecision(raw any) ClaimDecision {
	payload, ok := raw.(map[string]any)
	if !ok || payload == nil {
		return ClaimDecision{Status: ClaimUnconfirmed, Reason: "invalid_shape"}
	}
	var evidence claimEvidence
	ret, hasRet, numericRet := claimBusinessCode(payload["ret"])
	code, hasCode, numericCode := claimBusinessCode(payload["code"])
	diagnostic := code
	if hasRet {
		diagnostic = ret
	}
	decision := ClaimDecision{Status: ClaimUnconfirmed, ProviderCode: safeClaimProviderCode(diagnostic), Reason: "unrecognized_response"}
	if hasRet {
		if !numericRet {
			evidence.unknown = true
		} else {
			switch ret {
			case "0":
				evidence.positive = true
			case "3001":
				evidence.already = true
			default:
				evidence.rejected = true
			}
		}
	}
	if hasCode {
		if !numericCode {
			evidence.unknown = true
		} else {
			switch {
			case code == "0":
				evidence.positive = true
			case hasRet && numericRet && ret == "3001" && code == ret:
				// Preserve the known ret mapping if a second field repeats that same code.
				evidence.already = true
			default:
				evidence.rejected = true
			}
		}
	}
	layers := []map[string]any{payload}
	if data, ok := payload["data"].(map[string]any); ok {
		layers = append(layers, data)
	}
	for _, layer := range layers {
		for _, field := range []string{"received", "success"} {
			value, present, recognized := claimBoolean(layer[field])
			if !present {
				continue
			}
			if !recognized {
				evidence.unknown = true
				continue
			}
			if value {
				evidence.positive = true
			} else if field == "received" {
				evidence.receivedFalse = true
			} else {
				evidence.rejected = true
			}
		}
		if claimHasError(layer["error"]) {
			evidence.rejected = true
		}
	}
	// Conflicts take precedence over both positive and rejection codes. In
	// particular, ret/code zero cannot override received=false or an explicit error.
	if evidence.positive && (evidence.receivedFalse || evidence.rejected || evidence.already) || evidence.already && evidence.rejected {
		decision.Reason = "conflicting_signals"
		return decision
	}
	// An unfamiliar symbolic code does not contradict explicit rejection. Keep
	// negative business evidence once positive/negative conflicts are excluded.
	if evidence.rejected {
		decision.Status, decision.Reason = ClaimRejected, "explicit_rejection"
		return decision
	}
	if evidence.unknown {
		return decision
	}
	switch {
	case evidence.already:
		decision.Status, decision.Reason = ClaimAlreadyReceived, "already_claimed"
	case evidence.positive:
		decision.Status, decision.Reason = ClaimConfirmed, "confirmed"
	}
	return decision
}

// claimBusinessCode canonicalizes integer scalars without interpreting message
// text as a business code. A malformed nonempty code remains unknown.
func claimBusinessCode(raw any) (code string, present, numeric bool) {
	switch value := raw.(type) {
	case nil:
		return "", false, false
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return "", false, false
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return value, true, false
		}
		return strconv.FormatInt(number, 10), true, true
	case json.Number:
		if number, err := value.Int64(); err == nil {
			return strconv.FormatInt(number, 10), true, true
		}
		number, err := value.Float64()
		if err != nil {
			return string(value), true, false
		}
		return claimBusinessCode(number)
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			return "", true, false
		}
		if value == 0 {
			return "0", true, true
		}
		return strconv.FormatFloat(value, 'f', 0, 64), true, true
	case float32:
		return claimBusinessCode(float64(value))
	case int:
		return strconv.Itoa(value), true, true
	case int8:
		return strconv.FormatInt(int64(value), 10), true, true
	case int16:
		return strconv.FormatInt(int64(value), 10), true, true
	case int32:
		return strconv.FormatInt(int64(value), 10), true, true
	case int64:
		return strconv.FormatInt(value, 10), true, true
	case uint:
		return strconv.FormatUint(uint64(value), 10), true, true
	case uint8:
		return strconv.FormatUint(uint64(value), 10), true, true
	case uint16:
		return strconv.FormatUint(uint64(value), 10), true, true
	case uint32:
		return strconv.FormatUint(uint64(value), 10), true, true
	case uint64:
		return strconv.FormatUint(value, 10), true, true
	default:
		return "", true, false
	}
}

// claimBoolean accepts only explicit true/false and zero/one scalar encodings.
// It reports absence separately so nullable optional fields do not become false.
func claimBoolean(raw any) (value, present, recognized bool) {
	switch value := raw.(type) {
	case nil:
		return false, false, false
	case bool:
		return value, true, true
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "":
			return false, false, false
		case "true", "1":
			return true, true, true
		case "false", "0":
			return false, true, true
		default:
			return false, true, false
		}
	default:
		code, present, numeric := claimBusinessCode(raw)
		if numeric && (code == "0" || code == "1") {
			return code == "1", present, true
		}
		return false, present, false
	}
}

func claimHasError(raw any) bool {
	if value, _, recognized := claimBoolean(raw); recognized {
		return value
	}
	switch value := raw.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(value) != ""
	case map[string]any:
		return len(value) > 0
	case []any:
		return len(value) > 0
	default:
		code, _, numeric := claimBusinessCode(raw)
		return numeric && code != "0"
	}
}

func safeClaimProviderCode(code string) string {
	if len(code) == 0 || len(code) > 64 {
		return ""
	}
	for _, character := range code {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return ""
	}
	return code
}

type AssetSummary struct {
	ID string `json:"id"`
}
type AssetPage struct {
	Items      []AssetSummary `json:"items"`
	Offset     int            `json:"offset"`
	NextOffset int            `json:"next_offset"`
	HasMore    bool           `json:"has_more"`
	// Legacy keeps the existing assets response available while clients migrate.
	Legacy any `json:"-"`
}

func normalizeAssetPage(raw any, offset int) (AssetPage, error) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return AssetPage{}, &upstream.Error{Code: upstream.InvalidResponse, Message: "即梦作品响应格式错误"}
	}
	entries, ok := payload["asset_list"].([]any)
	if !ok {
		return AssetPage{}, &upstream.Error{Code: upstream.InvalidResponse, Message: "即梦作品响应缺少 asset_list"}
	}
	page := AssetPage{Items: make([]AssetSummary, 0, len(entries)), Offset: offset, NextOffset: offset + len(entries), Legacy: raw}
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			return AssetPage{}, &upstream.Error{Code: upstream.InvalidResponse, Message: "即梦作品条目格式错误"}
		}
		id := scalarString(item["id"])
		if id == "" {
			id = scalarString(item["asset_id"])
		}
		page.Items = append(page.Items, AssetSummary{ID: id})
	}
	// Paging metadata is optional. A valid asset list must remain available when
	// an end-of-list cursor is null/empty or a provider uses scalar variants.
	if next, ok := assetOffset(payload["next_offset"]); ok {
		page.NextOffset = next
	}
	page.HasMore = assetHasMore(payload["has_more"])

	// Existing clients consume data.assets rather than the additive data.page.
	// Normalize both contracts without changing the provider's original payload.
	legacy := make(map[string]any, len(payload)+2)
	for key, value := range payload {
		legacy[key] = value
	}
	legacy["next_offset"] = page.NextOffset
	legacy["has_more"] = page.HasMore
	page.Legacy = legacy
	return page, nil
}

func assetOffset(value any) (int, bool) {
	switch value := value.(type) {
	case float64:
		// JSON numbers decode to float64. Formatting them first can produce
		// scientific notation, which Atoi rejects even for valid integer cursors.
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 ||
			value >= math.Ldexp(1, strconv.IntSize-1) || math.Trunc(value) != value {
			return 0, false
		}
		return int(value), true
	case int:
		return value, value >= 0
	case int64:
		parsed := int(value)
		return parsed, value >= 0 && int64(parsed) == value
	case json.Number:
		parsed, err := strconv.Atoi(string(value))
		return parsed, err == nil && parsed >= 0
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		return parsed, err == nil && parsed >= 0
	default:
		return 0, false
	}
}

func assetHasMore(value any) bool {
	switch value := value.(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return err == nil && parsed
	default:
		parsed, ok := assetOffset(value)
		return ok && parsed == 1
	}
}
