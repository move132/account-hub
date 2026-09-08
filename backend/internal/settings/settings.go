package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

type Kind string

const (
	Boolean Kind = "boolean"
	Integer Kind = "integer"
	String  Kind = "string"
)

type Definition struct {
	Key          string `json:"key"`
	Group        string `json:"group"`
	Label        string `json:"label"`
	Kind         Kind   `json:"type"`
	DefaultValue any    `json:"default_value"`
	Min          int    `json:"min,omitempty"`
	Max          int    `json:"max,omitempty"`
}

var definitions = []Definition{
	{Key: "token_auto_refresh_enabled", Group: "token", Label: "Token 自动刷新", Kind: Boolean, DefaultValue: false},
	{Key: "token_refresh_interval_hours", Group: "token", Label: "Token 刷新周期（小时）", Kind: Integer, DefaultValue: 168, Min: 1, Max: 216},
	{Key: "token_refresh_batch_size", Group: "token", Label: "Token 单次刷新数量", Kind: Integer, DefaultValue: 100, Min: 1, Max: 1000},
	{Key: "token_refresh_account_interval_seconds", Group: "token", Label: "Token 账号刷新间隔（秒）", Kind: Integer, DefaultValue: 6, Min: 1, Max: 300},
	{Key: "dreamina_auto_refresh_enabled", Group: "dreamina", Label: "即梦自动刷新", Kind: Boolean, DefaultValue: false},
	{Key: "dreamina_refresh_interval_days", Group: "dreamina", Label: "即梦刷新周期（天）", Kind: Integer, DefaultValue: 7, Min: 0, Max: 365},
	{Key: "dreamina_refresh_batch_size", Group: "dreamina", Label: "即梦单次刷新数量", Kind: Integer, DefaultValue: 100, Min: 1, Max: 1000},
	{Key: "dreamina_refresh_account_interval_minutes", Group: "dreamina", Label: "即梦账号刷新间隔（分钟）", Kind: Integer, DefaultValue: 2, Min: 1, Max: 60},
	{Key: "proxy_enabled", Group: "network", Label: "启用代理", Kind: Boolean, DefaultValue: false},
	{Key: "proxy_url", Group: "network", Label: "代理地址", Kind: String, DefaultValue: ""},
}

type Item struct {
	Definition
	Value any `json:"value"`
}

type CleanupResult struct {
	AuditLogs        int `json:"audit_logs"`
	TokenAttempts    int `json:"token_attempts"`
	DreaminaAttempts int `json:"dreamina_attempts"`
	Jobs             int `json:"jobs"`
}

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func Definitions() []Definition {
	result := make([]Definition, len(definitions))
	copy(result, definitions)
	return result
}

func definitionFor(key string) (Definition, bool) {
	for _, definition := range definitions {
		if definition.Key == key {
			return definition, true
		}
	}
	return Definition{}, false
}

func (r *Repository) EnsureDefaults(ctx context.Context) error {
	now := time.Now().UTC().UnixMilli()
	for _, definition := range definitions {
		value, _ := encode(definition.Kind, definition.DefaultValue)
		if _, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value, value_type, updated_at)
            VALUES(?, ?, ?, ?)`, definition.Key, value, definition.Kind, now); err != nil {
			return fmt.Errorf("seed setting %s: %w", definition.Key, err)
		}
	}
	return nil
}

func (r *Repository) List(ctx context.Context, maskSecrets bool) ([]Item, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT key, value, value_type FROM settings ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]any, len(definitions))
	for rows.Next() {
		var key, raw, kind string
		if err := rows.Scan(&key, &raw, &kind); err != nil {
			return nil, err
		}
		definition, ok := definitionFor(key)
		if !ok || string(definition.Kind) != kind {
			continue
		}
		value, err := decode(definition.Kind, raw)
		if err != nil {
			return nil, fmt.Errorf("decode setting %s: %w", key, err)
		}
		if maskSecrets && key == "proxy_url" && value != "" {
			value = "••••••••"
		}
		values[key] = value
	}
	items := make([]Item, 0, len(definitions))
	for _, definition := range definitions {
		value, found := values[definition.Key]
		if !found {
			value = definition.DefaultValue
		}
		items = append(items, Item{Definition: definition, Value: value})
	}
	return items, rows.Err()
}

func (r *Repository) Values(ctx context.Context) (map[string]any, error) {
	items, err := r.List(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(items))
	for _, item := range items {
		result[item.Key] = item.Value
	}
	return result, nil
}

func (r *Repository) Update(ctx context.Context, updates map[string]any) ([]Item, error) {
	if len(updates) == 0 {
		return nil, errors.New("至少提供一个设置")
	}
	current, err := r.Values(ctx)
	if err != nil {
		return nil, err
	}
	for key, value := range updates {
		definition, ok := definitionFor(key)
		if !ok {
			return nil, fmt.Errorf("未知设置键: %s", key)
		}
		normalized, err := normalize(definition, value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		current[key] = normalized
	}
	if enabled, _ := current["proxy_enabled"].(bool); enabled {
		proxyURL, _ := current["proxy_url"].(string)
		if err := validateProxyURL(proxyURL); err != nil {
			return nil, err
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	for key := range updates {
		definition, _ := definitionFor(key)
		normalized := current[key]
		encoded, _ := encode(definition.Kind, normalized)
		if _, err := tx.ExecContext(ctx, "UPDATE settings SET value = ?, updated_at = ? WHERE key = ?", encoded, now, key); err != nil {
			return nil, err
		}
	}
	if _, changedInterval := updates["token_refresh_interval_hours"]; changedInterval || hasKey(updates, "token_auto_refresh_enabled") {
		if enabled, _ := current["token_auto_refresh_enabled"].(bool); enabled {
			hours, _ := current["token_refresh_interval_hours"].(int)
			intervalMillis := int64(time.Duration(hours) * time.Hour / time.Millisecond)
			if _, err := tx.ExecContext(ctx, `UPDATE tokens SET next_refresh_at = CASE
                    WHEN last_refreshed_at IS NULL THEN 0 ELSE last_refreshed_at + ? END
                    WHERE status = 'enabled'`, intervalMillis); err != nil {
				return nil, err
			}
		}
	}
	if _, changedInterval := updates["dreamina_refresh_interval_days"]; changedInterval || hasKey(updates, "dreamina_auto_refresh_enabled") {
		if enabled, _ := current["dreamina_auto_refresh_enabled"].(bool); enabled {
			days, _ := current["dreamina_refresh_interval_days"].(int)
			intervalMillis := int64(time.Duration(days) * 24 * time.Hour / time.Millisecond)
			if _, err := tx.ExecContext(ctx, `UPDATE dreamina_accounts SET next_refresh_at = CASE
                    WHEN last_refreshed_at IS NULL THEN 0 ELSE last_refreshed_at + ? END
                    WHERE status = 'enabled'`, intervalMillis); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.List(ctx, true)
}

func (r *Repository) CleanupHistory(ctx context.Context, before int64) (CleanupResult, error) {
	if before <= 0 {
		return CleanupResult{}, errors.New("before 必须是有效时间戳")
	}
	if before > time.Now().UTC().UnixMilli() {
		return CleanupResult{}, errors.New("清理时间不能晚于当前时间")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CleanupResult{}, err
	}
	defer tx.Rollback()
	result := CleanupResult{}
	tokenAttempts, err := deleteWhere(ctx, tx, "DELETE FROM token_refresh_attempts WHERE started_at < ?", before)
	if err != nil {
		return CleanupResult{}, err
	}
	result.TokenAttempts = tokenAttempts
	dreaminaAttempts, err := deleteWhere(ctx, tx, "DELETE FROM dreamina_refresh_attempts WHERE started_at < ?", before)
	if err != nil {
		return CleanupResult{}, err
	}
	result.DreaminaAttempts = dreaminaAttempts
	jobs, err := finishedJobIDsBefore(ctx, tx, before)
	if err != nil {
		return CleanupResult{}, err
	}
	for _, id := range jobs {
		if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE response_body = ?", id); err != nil {
			return CleanupResult{}, err
		}
	}
	if len(jobs) > 0 {
		deletedJobs := 0
		for _, id := range jobs {
			changed, err := deleteWhere(ctx, tx, "DELETE FROM jobs WHERE id = ?", id)
			if err != nil {
				return CleanupResult{}, err
			}
			deletedJobs += changed
		}
		result.Jobs = deletedJobs
	}
	auditLogs, err := deleteWhere(ctx, tx, "DELETE FROM audit_logs WHERE created_at < ?", before)
	if err != nil {
		return CleanupResult{}, err
	}
	result.AuditLogs = auditLogs
	return result, tx.Commit()
}

func deleteWhere(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	execResult, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	deleted, err := execResult.RowsAffected()
	return int(deleted), err
}

func finishedJobIDsBefore(ctx context.Context, tx *sql.Tx, before int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM jobs WHERE created_at < ? AND state NOT IN ('queued', 'running') AND running = 0", before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func normalize(definition Definition, value any) (any, error) {
	switch definition.Kind {
	case Boolean:
		boolean, ok := value.(bool)
		if !ok {
			return nil, errors.New("必须是 boolean")
		}
		return boolean, nil
	case Integer:
		var integer int
		switch typed := value.(type) {
		case float64:
			integer = int(typed)
			if float64(integer) != typed {
				return nil, errors.New("必须是整数")
			}
		case int:
			integer = typed
		default:
			return nil, errors.New("必须是整数")
		}
		if integer < definition.Min || integer > definition.Max {
			return nil, fmt.Errorf("必须在 %d 到 %d 之间", definition.Min, definition.Max)
		}
		return integer, nil
	case String:
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("必须是字符串")
		}
		if len(text) > 2048 {
			return nil, errors.New("长度不能超过 2048")
		}
		if definition.Key == "proxy_url" && text == "••••••••" {
			return nil, errors.New("代理遮罩值不能作为新地址保存")
		}
		return text, nil
	default:
		return nil, errors.New("不支持的设置类型")
	}
}

func hasKey(values map[string]any, key string) bool {
	_, found := values[key]
	return found
}

func validateProxyURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return errors.New("启用代理时必须提供有效代理地址")
	}
	switch parsed.Scheme {
	case "http", "https", "socks5":
		return nil
	default:
		return errors.New("代理地址只支持 http、https 或 socks5")
	}
}

func encode(kind Kind, value any) (string, error) {
	switch kind {
	case Boolean:
		return strconv.FormatBool(value.(bool)), nil
	case Integer:
		switch typed := value.(type) {
		case int:
			return strconv.Itoa(typed), nil
		case float64:
			return strconv.Itoa(int(typed)), nil
		}
	case String:
		return value.(string), nil
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func decode(kind Kind, value string) (any, error) {
	switch kind {
	case Boolean:
		return strconv.ParseBool(value)
	case Integer:
		return strconv.Atoi(value)
	case String:
		return value, nil
	default:
		return nil, errors.New("unsupported kind")
	}
}
