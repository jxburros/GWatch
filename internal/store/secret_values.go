package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/secrets"
	"time"
)

func (s *Store) sealSecret(v string) (string, error) {
	if v == "" || secrets.IsSealed(v) {
		return v, nil
	}
	return s.secrets.Seal(v)
}

func (s *Store) openSecret(v string) (string, error) {
	plain, err := s.secrets.Open(v)
	if err != nil {
		s.setSecretsErr(fmt.Errorf("configuration secret: %w", err))
		return "", nil
	}
	return plain, nil
}

// Re-saving is idempotent and upgrades legacy plaintext rows through exactly
// the same boundary as new configuration writes.
func (s *Store) migrateActionSecrets(ctx context.Context) error {
	for _, spec := range []struct {
		table, column string
		array         bool
	}{{"triggers", "action", false}, {"endpoints", "action", false}, {"rules", "actions", true}} {
		rows, err := s.query(ctx, "SELECT id,"+spec.column+" FROM "+spec.table)
		if err != nil {
			return err
		}
		type change struct {
			id  int64
			raw string
		}
		var pending []change
		for rows.Next() {
			var id int64
			var raw string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var actions []model.Action
			if spec.array {
				if err := json.Unmarshal([]byte(raw), &actions); err != nil {
					rows.Close()
					return err
				}
			} else {
				var action model.Action
				if err := json.Unmarshal([]byte(raw), &action); err != nil {
					rows.Close()
					return err
				}
				actions = []model.Action{action}
			}
			changed := false
			for i := range actions {
				if err := actions[i].TransformSecrets(func(v string) (string, error) {
					if v != "" && !secrets.IsSealed(v) {
						changed = true
					}
					return s.sealSecret(v)
				}); err != nil {
					rows.Close()
					return err
				}
			}
			if changed {
				encoded := jsonString(actions)
				if !spec.array {
					encoded = jsonString(actions[0])
				}
				pending = append(pending, change{id, encoded})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, c := range pending {
			if _, err := s.exec(ctx, "UPDATE "+spec.table+" SET "+spec.column+"=? WHERE id=?", c.raw, c.id); err != nil {
				return err
			}
		}
	}
	rows, err := s.query(ctx, "SELECT id,token FROM endpoints")
	if err != nil {
		return err
	}
	type tokenChange struct {
		id    int64
		token string
	}
	var pending []tokenChange
	for rows.Next() {
		var id int64
		var token string
		if err := rows.Scan(&id, &token); err != nil {
			rows.Close()
			return err
		}
		if token != "" && !secrets.IsSealed(token) {
			v, err := s.sealSecret(token)
			if err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, tokenChange{id, v})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range pending {
		if _, err := s.exec(ctx, "UPDATE endpoints SET token=? WHERE id=?", c.token, c.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PruneExpiredPairings(now time.Time) error {
	_, err := s.exec(context.Background(), "DELETE FROM agent_pairings WHERE expires_at < ?", fmtTime(now))
	return err
}
