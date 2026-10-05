package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jxburros/GWatch/internal/model"
	"slices"
	"time"
)

const incidentSchema = `
CREATE TABLE IF NOT EXISTS incidents (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 active_node_id INTEGER UNIQUE,
 state TEXT NOT NULL,
 opened_at TEXT NOT NULL,
 document TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_incidents_node ON incidents(node_id);
`

func decodeIncident(id int64, raw, name string) (model.Incident, error) {
	var v model.Incident
	err := json.Unmarshal([]byte(raw), &v)
	v.ID, v.NodeName = id, name
	end := time.Now()
	if v.ResolvedAt != nil {
		end = *v.ResolvedAt
	}
	v.DurationSeconds = end.Sub(v.OpenedAt).Seconds()
	if v.AcknowledgedAt != nil {
		n := v.AcknowledgedAt.Sub(v.OpenedAt).Seconds()
		v.TimeToAcknowledgeSeconds = &n
	}
	if v.ResolvedAt != nil {
		n := v.ResolvedAt.Sub(v.OpenedAt).Seconds()
		v.TimeToResolveSeconds = &n
	}
	return v, err
}

func (s *Store) ListIncidents(ctx context.Context, state string) ([]model.Incident, error) {
	return s.listIncidents(ctx, state, 5000)
}

// ExportIncidents preserves all incident history in encrypted backups.
func (s *Store) ExportIncidents(ctx context.Context) ([]model.Incident, error) {
	return s.listIncidents(ctx, "all", 0)
}

func (s *Store) listIncidents(ctx context.Context, state string, limit int) ([]model.Incident, error) {
	q := "SELECT i.id, i.document, n.name FROM incidents i JOIN nodes n ON n.id=i.node_id"
	var args []any
	if state == "active" || state == "" {
		q += " WHERE i.state <> 'resolved'"
	} else if state != "all" {
		q += " WHERE i.state=?"
		args = append(args, state)
	}
	q += " ORDER BY i.id DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Incident{}
	for rows.Next() {
		var id int64
		var raw, name string
		if err := rows.Scan(&id, &raw, &name); err != nil {
			return nil, err
		}
		v, err := decodeIncident(id, raw, name)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetIncident(ctx context.Context, id int64) (model.Incident, error) {
	var raw, name string
	err := s.queryRow(ctx, "SELECT i.document, n.name FROM incidents i JOIN nodes n ON n.id=i.node_id WHERE i.id=?", id).Scan(&raw, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Incident{}, ErrNotFound
	}
	if err != nil {
		return model.Incident{}, err
	}
	return decodeIncident(id, raw, name)
}

func saveIncidentTx(ctx context.Context, tx *wtx, v model.Incident) error {
	var active any = v.NodeID
	if v.State == "resolved" {
		active = nil
	}
	_, err := tx.exec(ctx, "UPDATE incidents SET active_node_id=?, state=?, document=? WHERE id=?", active, v.State, jsonString(v), v.ID)
	return err
}

// Called in the same transaction as result/state persistence: no phantom
// incidents from failed writes, and concurrent checks cannot open duplicates.
func (s *Store) reconcileIncidentTx(ctx context.Context, tx *wtx, r model.Result, st model.CheckState, wasDown bool) error {
	var nodeID int64
	if err := tx.queryRow(ctx, "SELECT node_id FROM checks WHERE id=?", r.CheckID).Scan(&nodeID); err != nil {
		return err
	}
	var id int64
	var raw string
	err := tx.queryRow(ctx, "SELECT id,document FROM incidents WHERE active_node_id=?", nodeID).Scan(&id, &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	v := model.Incident{}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return err
		}
		v.ID = id
	}
	if st.Status == model.StatusDown {
		if v.ID == 0 {
			if wasDown {
				return nil
			}
			v = model.Incident{NodeID: nodeID, State: "open", OpenedAt: r.Timestamp, CheckIDs: []int64{r.CheckID}, Notes: []model.IncidentNote{}}
			_, err := tx.insertID(ctx, "INSERT INTO incidents(node_id,active_node_id,state,opened_at,document) VALUES(?,?,?,?,?)", nodeID, nodeID, v.State, fmtTime(v.OpenedAt), jsonString(v))
			return err
		}
		if !slices.Contains(v.CheckIDs, r.CheckID) {
			v.CheckIDs = append(v.CheckIDs, r.CheckID)
		}
		return saveIncidentTx(ctx, tx, v)
	}
	if v.ID == 0 {
		return nil
	}
	var down int
	if err := tx.queryRow(ctx, "SELECT COUNT(*) FROM check_state st JOIN checks c ON c.id=st.check_id WHERE c.node_id=? AND c.enabled=1 AND st.status='down'", nodeID).Scan(&down); err != nil {
		return err
	}
	if down == 0 {
		v.State = "resolved"
		v.ResolvedAt = &r.Timestamp
		v.ResolvedBy = "recovery"
		return saveIncidentTx(ctx, tx, v)
	}
	return nil
}

func (s *Store) UpdateIncident(ctx context.Context, id int64, action, actor, note string) (model.Incident, error) {
	err := s.writeTx(ctx, func(tx *wtx) error {
		var raw string
		if err := tx.queryRow(ctx, "SELECT document FROM incidents WHERE id=?", id).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var v model.Incident
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return err
		}
		v.ID = id
		now := time.Now()
		switch action {
		case "acknowledge":
			if v.State == "resolved" {
				return fmt.Errorf("incident is already resolved")
			}
			if v.AcknowledgedAt == nil {
				v.AcknowledgedAt = &now
				v.AcknowledgedBy = actor
			}
			v.State = "acknowledged"
		case "resolve":
			if v.State != "resolved" {
				v.State = "resolved"
				v.ResolvedAt = &now
				v.ResolvedBy = actor
			}
		case "note":
		default:
			return fmt.Errorf("unknown incident action")
		}
		if note != "" {
			v.Notes = append(v.Notes, model.IncidentNote{At: now, Actor: actor, Text: note})
		}
		return saveIncidentTx(ctx, tx, v)
	})
	if err != nil {
		return model.Incident{}, err
	}
	return s.GetIncident(ctx, id)
}

func (s *Store) NodeIncidentAcknowledged(ctx context.Context, nodeID int64) (bool, error) {
	var n int
	err := s.queryRow(ctx, "SELECT COUNT(*) FROM incidents WHERE active_node_id=? AND state='acknowledged'", nodeID).Scan(&n)
	return n > 0, err
}

// RestoreIncidents preserves attribution and notes in encrypted backups.
func (s *Store) RestoreIncidents(ctx context.Context, list []model.Incident) error {
	return s.writeTx(ctx, func(tx *wtx) error {
		for _, v := range list {
			var active any = v.NodeID
			if v.State == "resolved" {
				active = nil
			}
			if _, err := tx.insertID(ctx, "INSERT INTO incidents(node_id,active_node_id,state,opened_at,document) VALUES(?,?,?,?,?)", v.NodeID, active, v.State, fmtTime(v.OpenedAt), jsonString(v)); err != nil {
				return err
			}
		}
		return nil
	})
}
