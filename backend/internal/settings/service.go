package settings

import (
	"context"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/platform/netconfig"
	platformsqlite "account-hub/internal/platform/sqlite"
)

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

type Change struct {
	Before Snapshot
	After  Snapshot
	Keys   map[string]bool
}

// ChangeEffect joins configuration changes in the same transaction. It must use tx, not a separate DB connection.
type ChangeEffect func(ctx context.Context, tx platformsqlite.DBTX, change Change) error

type Service struct {
	repository *Repository
	effects    []ChangeEffect
	audit      *audit.Repository
}

func NewService(repository *Repository, effects ...ChangeEffect) *Service {
	return &Service{repository: repository, effects: append([]ChangeEffect(nil), effects...)}
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
