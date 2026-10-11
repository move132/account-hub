package token

import (
	"context"
	"strings"
)

// TargetLabels reads only non-secret display fields in bounded batches.
func (s *Service) TargetLabels(ctx context.Context, ids []string) (map[string]string, error) {
	return s.repository.TargetLabels(ctx, ids)
}
func (r *Repository) TargetLabels(ctx context.Context, ids []string) (map[string]string, error) {
	labels := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += 400 {
		batch := ids[start:min(start+400, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.db.QueryContext(ctx, "SELECT id, CASE WHEN email <> '' THEN email WHEN name <> '' THEN name ELSE id END FROM tokens WHERE id IN ("+placeholders+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, label string
			if err := rows.Scan(&id, &label); err != nil {
				rows.Close()
				return nil, err
			}
			labels[id] = label
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return labels, nil
}
