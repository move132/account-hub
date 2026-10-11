package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/netconfig"
	platformsqlite "account-hub/internal/platform/sqlite"
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

type RefreshPolicy struct {
	Enabled         bool
	Interval        time.Duration
	BatchSize       int
	AccountInterval time.Duration
}

type Snapshot struct {
	Token    RefreshPolicy
	Dreamina RefreshPolicy
	Proxy    netconfig.Proxy
}

type Change struct {
	After Snapshot
	Keys  map[string]bool
}

// ChangeEffect joins configuration changes in the same transaction. It must use tx, not a separate DB connection.
type ChangeEffect func(ctx context.Context, tx platformsqlite.DBTX, change Change) error

type Repository struct {
	db       *sql.DB
	writes   sync.Mutex
	snapshot atomic.Pointer[Snapshot]
}

type Service struct {
	repository *Repository
	effects    []ChangeEffect
	audit      *audit.Repository
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func NewService(repository *Repository, effects ...ChangeEffect) *Service {
	return &Service{repository: repository, effects: append([]ChangeEffect(nil), effects...)}
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
	_, err := r.Snapshot(ctx)
	return err
}

func (r *Repository) List(ctx context.Context) ([]Item, error) {
	return list(ctx, r.db)
}

func list(ctx context.Context, db platformsqlite.DBTX) ([]Item, error) {
	rows, err := db.QueryContext(ctx, "SELECT key, value, value_type FROM settings ORDER BY key")
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

// Snapshot is the last committed configuration. Callers never share mutable maps.
func (r *Repository) Snapshot(ctx context.Context) (Snapshot, error) {
	if snapshot := r.snapshot.Load(); snapshot != nil {
		return *snapshot, nil
	}
	r.writes.Lock()
	defer r.writes.Unlock()
	if snapshot := r.snapshot.Load(); snapshot != nil {
		return *snapshot, nil
	}
	items, err := r.List(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := snapshotFromValues(itemValues(items))
	r.snapshot.Store(&snapshot)
	return snapshot, nil
}

func (r *Repository) ProxyConfig(ctx context.Context) (netconfig.Proxy, error) {
	snapshot, err := r.Snapshot(ctx)
	return snapshot.Proxy, err
}

func snapshotFromValues(values map[string]any) Snapshot {
	return Snapshot{
		Token: RefreshPolicy{Enabled: values["token_auto_refresh_enabled"].(bool), Interval: time.Duration(values["token_refresh_interval_hours"].(int)) * time.Hour,
			BatchSize: values["token_refresh_batch_size"].(int), AccountInterval: time.Duration(values["token_refresh_account_interval_seconds"].(int)) * time.Second},
		Dreamina: RefreshPolicy{Enabled: values["dreamina_auto_refresh_enabled"].(bool), Interval: time.Duration(values["dreamina_refresh_interval_days"].(int)) * 24 * time.Hour,
			BatchSize: values["dreamina_refresh_batch_size"].(int), AccountInterval: time.Duration(values["dreamina_refresh_account_interval_minutes"].(int)) * time.Minute},
		Proxy: netconfig.Proxy{Enabled: values["proxy_enabled"].(bool), URL: values["proxy_url"].(string)},
	}
}

func (s *Service) List(ctx context.Context) ([]Item, error)        { return s.repository.List(ctx) }
func (s *Service) WithAudit(repository *audit.Repository) *Service { s.audit = repository; return s }

func (s *Service) Update(ctx context.Context, updates map[string]any) ([]Item, error) {
	effects := s.effects
	if s.audit != nil {
		effects = append(append([]ChangeEffect(nil), effects...), func(ctx context.Context, tx platformsqlite.DBTX, _ Change) error {
			entry, ok := audit.EntryFromContext(ctx)
			if !ok {
				entry = audit.Entry{Action: "settings_update", TargetKind: "settings", Summary: "更新系统设置"}
			}
			return s.audit.LogTx(ctx, tx, entry)
		})
	}
	return s.repository.update(ctx, updates, effects)
}

// Update persists settings only. Application-level effects belong to Service.
func (r *Repository) Update(ctx context.Context, updates map[string]any) ([]Item, error) {
	return r.update(ctx, updates, nil)
}

func (r *Repository) update(ctx context.Context, updates map[string]any, effects []ChangeEffect) ([]Item, error) {
	if len(updates) == 0 {
		return nil, apperror.New(apperror.Validation, "至少提供一个设置")
	}
	// Serialize commit and snapshot publication, so a later commit cannot publish an older snapshot.
	r.writes.Lock()
	defer r.writes.Unlock()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	items, err := list(ctx, tx)
	if err != nil {
		return nil, err
	}
	current := itemValues(items)
	change := Change{Keys: make(map[string]bool, len(updates))}
	for key, value := range updates {
		definition, ok := definitionFor(key)
		if !ok {
			return nil, apperror.New(apperror.Validation, fmt.Sprintf("未知设置键: %s", key))
		}
		normalized, err := normalize(definition, value)
		if err != nil {
			return nil, apperror.Wrap(apperror.Validation, fmt.Sprintf("%s: %s", key, err), err)
		}
		current[key] = normalized
		change.Keys[key] = true
	}
	if enabled, _ := current["proxy_enabled"].(bool); enabled {
		proxyURL, _ := current["proxy_url"].(string)
		if err := validateProxyURL(proxyURL); err != nil {
			return nil, apperror.Wrap(apperror.Validation, err.Error(), err)
		}
	}
	now := time.Now().UTC().UnixMilli()
	for key := range updates {
		definition, _ := definitionFor(key)
		encoded, err := encode(definition.Kind, current[key])
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value, value_type, updated_at) VALUES(?, ?, ?, ?)
   ON CONFLICT(key) DO UPDATE SET value = excluded.value, value_type = excluded.value_type, updated_at = excluded.updated_at`, key, encoded, definition.Kind, now); err != nil {
			return nil, err
		}
	}
	change.After = snapshotFromValues(current)
	for _, effect := range effects {
		if err := effect(ctx, tx, change); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	r.snapshot.Store(&change.After)
	for i := range items {
		items[i].Value = current[items[i].Key]
	}
	return items, nil
}

func itemValues(items []Item) map[string]any {
	values := make(map[string]any, len(items))
	for _, item := range items {
		values[item.Key] = item.Value
	}
	return values
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
