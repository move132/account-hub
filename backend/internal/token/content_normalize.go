package token

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var assetFilePattern = regexp.MustCompile(`file_[a-zA-Z0-9]+`)

func contentObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func contentString(value any) string {
	text, _ := value.(string)
	return text
}

func contentNumber(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, !math.IsNaN(value) && !math.IsInf(value, 0)
	case string:
		number, err := strconv.ParseFloat(value, 64)
		return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
	default:
		return 0, false
	}
}

func contentTime(value any) string {
	if number, ok := contentNumber(value); ok && number > 0 && number < 1e15 {
		if number < 1e12 {
			number *= 1000
		}
		return time.UnixMilli(int64(number)).UTC().Format(time.RFC3339Nano)
	}
	if text := contentString(value); text != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano)
		}
	}
	return ""
}

func conversationSummary(item map[string]any) ConversationSummary {
	return ConversationSummary{ID: contentString(item["id"]), Title: contentString(item["title"]), CreateTime: contentTime(item["create_time"]), UpdateTime: contentTime(item["update_time"])}
}

func normalizeConversations(payload any, offset, limit int) (ConversationPage, error) {
	result := ConversationPage{Items: []ConversationSummary{}, Offset: offset, Limit: limit}
	items, listPayload := payload.([]any)
	object := contentObject(payload)
	if !listPayload {
		for _, key := range []string{"items", "conversations", "data"} {
			if list, ok := object[key].([]any); ok {
				items = list
				break
			}
		}
		if items == nil {
			return result, invalidContent("聊天记录响应缺少会话列表")
		}
		for _, key := range []string{"total", "total_count"} {
			if total, ok := contentNumber(object[key]); ok && total >= 0 && total < 1e9 && math.Trunc(total) == total {
				count := int(total)
				result.Total = &count
				break
			}
		}
	} else {
		count := len(items)
		result.Total = &count
	}
	seen := make(map[string]bool)
	for _, item := range items {
		summary := conversationSummary(contentObject(item))
		if summary.ID == "" || seen[summary.ID] {
			continue
		}
		seen[summary.ID] = true
		result.Items = append(result.Items, summary)
	}
	result.NextOffset = offset + len(items)
	if more, ok := object["has_more"].(bool); ok {
		result.HasMore = more
	} else if !listPayload && result.Total != nil {
		result.HasMore = result.NextOffset < *result.Total
	} else if !listPayload {
		result.HasMore = len(items) >= limit
	}
	result.HasMore = result.HasMore && len(items) > 0
	return result, nil
}

func normalizeConversation(payload map[string]any, id string) (ConversationDetail, error) {
	result := ConversationDetail{ConversationSummary: conversationSummary(payload), Messages: []ConversationMessage{}}
	result.ID = id
	mapping := contentObject(payload["mapping"])
	if mapping == nil {
		return result, invalidContent("会话详情响应缺少消息结构")
	}
	ordered := []string{}
	visited := make(map[string]bool)
	current := contentString(payload["current_node"])
	for current != "" && !visited[current] {
		node := contentObject(mapping[current])
		if node == nil {
			break
		}
		visited[current] = true
		ordered = append(ordered, current)
		current = contentString(node["parent"])
	}
	for left, right := 0, len(ordered)-1; left < right; left, right = left+1, right-1 {
		ordered[left], ordered[right] = ordered[right], ordered[left]
	}
	if len(ordered) == 0 {
		for nodeID := range mapping {
			ordered = append(ordered, nodeID)
		}
		sort.Slice(ordered, func(i, j int) bool {
			left := contentObject(contentObject(mapping[ordered[i]])["message"])
			right := contentObject(contentObject(mapping[ordered[j]])["message"])
			timestamp := func(message map[string]any) time.Time {
				value := contentTime(message["create_time"])
				if value == "" {
					value = contentTime(message["update_time"])
				}
				parsed, _ := time.Parse(time.RFC3339Nano, value)
				return parsed
			}
			leftTime, rightTime := timestamp(left), timestamp(right)
			if leftTime.Equal(rightTime) {
				return ordered[i] < ordered[j]
			}
			return leftTime.Before(rightTime)
		})
	}
	for _, nodeID := range ordered {
		message := contentObject(contentObject(mapping[nodeID])["message"])
		if message == nil || contentObject(message["metadata"])["is_visually_hidden_from_conversation"] == true {
			continue
		}
		text, assets := normalizeMessageContent(message["content"])
		if text == "" && len(assets) == 0 {
			continue
		}
		author := contentObject(message["author"])
		role := contentString(author["role"])
		if role == "" {
			role = "unknown"
		}
		name := contentString(author["name"])
		if name == "" {
			name = role
		}
		messageID := contentString(message["id"])
		if messageID == "" {
			messageID = nodeID
		}
		result.Messages = append(result.Messages, ConversationMessage{ID: messageID, Role: role, Author: name, Content: text, Assets: assets, CreateTime: contentTime(message["create_time"])})
	}
	return result, nil
}

func normalizeMessageContent(value any) (string, []ConversationAsset) {
	assets := []ConversationAsset{}
	seen := make(map[string]bool)
	parts, isList := value.([]any)
	if !isList {
		object := contentObject(value)
		if list, ok := object["parts"].([]any); ok {
			parts = list
		} else {
			parts = []any{value}
		}
	}
	texts := []string{}
	for _, part := range parts {
		object := contentObject(part)
		pointer := contentString(object["asset_pointer"])
		if pointer == "" {
			pointer = contentString(object["file_id"])
		}
		if object["content_type"] == "image_asset_pointer" || strings.HasPrefix(pointer, "sediment://") {
			fileID := assetFilePattern.FindString(pointer)
			if fileID != "" && !seen[fileID] {
				assets = append(assets, ConversationAsset{FileID: fileID})
				seen[fileID] = true
			}
		}
		text := contentString(part)
		if text == "" && object != nil {
			for _, key := range []string{"text", "content", "caption", "transcript", "result", "stdout", "stderr", "name"} {
				if candidate := contentString(object[key]); strings.TrimSpace(candidate) != "" {
					text = candidate
					break
				}
			}
			if text == "" {
				switch object["content_type"] {
				case "image_asset_pointer":
					text = "[图片]"
				case "audio_asset_pointer":
					text = "[音频]"
				default:
					if pointer != "" {
						text = "[附件: " + pointer + "]"
					}
				}
			}
		}
		if text == "" && part != nil {
			if _, isText := part.(string); !isText {
				encoded, _ := json.Marshal(part)
				text = string(encoded)
			}
		}
		if text = strings.TrimSpace(text); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n"), assets
}
