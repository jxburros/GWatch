package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

const wallCols = `id, name, sort_order, layout, panels, share_enabled, share_token, created_at, updated_at`

func scanWallboard(sc interface{ Scan(...any) error }) (model.Wallboard, error) {
	var b model.Wallboard
	var layout, panels, created, updated string
	var shareEnabled int
	if err := sc.Scan(&b.ID, &b.Name, &b.SortOrder, &layout, &panels, &shareEnabled, &b.Share.Token, &created, &updated); err != nil {
		return b, err
	}
	_ = json.Unmarshal([]byte(layout), &b.Layout)
	_ = json.Unmarshal([]byte(panels), &b.Panels)
	if b.Panels == nil {
		b.Panels = []model.WallPanel{}
	}
	b.Share.Enabled = shareEnabled == 1
	b.Layout.Normalize()
	b.CreatedAt, b.UpdatedAt = mustTime(created), mustTime(updated)
	return b, nil
}

// ListWallboards returns every wallboard, in the order they are shown.
func (s *Store) ListWallboards(ctx context.Context) ([]model.Wallboard, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+wallCols+" FROM wallboards ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Wallboard{}
	for rows.Next() {
		b, err := scanWallboard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetWallboard returns one wallboard.
func (s *Store) GetWallboard(ctx context.Context, id int64) (model.Wallboard, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+wallCols+" FROM wallboards WHERE id = ?", id)
	b, err := scanWallboard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// WallboardByShareToken finds the wallboard a projected address refers to.
// Sharing being switched off makes the board unreachable however good the
// token is, which is what makes turning it off a revocation rather than a
// suggestion.
func (s *Store) WallboardByShareToken(ctx context.Context, token string) (model.Wallboard, error) {
	if token == "" {
		return model.Wallboard{}, ErrNotFound
	}
	row := s.reader.QueryRowContext(ctx,
		"SELECT "+wallCols+" FROM wallboards WHERE share_token = ? AND share_enabled = 1", token)
	b, err := scanWallboard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// SaveWallboard inserts (ID == 0) or updates a wallboard. The share settings
// are not touched here: they are changed through SetWallboardShare, so saving
// a layout can never hand out or withdraw an address by accident.
func (s *Store) SaveWallboard(ctx context.Context, b model.Wallboard) (model.Wallboard, error) {
	now := time.Now()
	if b.Panels == nil {
		b.Panels = []model.WallPanel{}
	}
	b.Layout.Normalize()
	for i := range b.Panels {
		if b.Panels[i].ID == "" {
			b.Panels[i].ID = fmt.Sprintf("p%d%d", now.UnixNano()%1000000, i)
		}
		b.Panels[i].Normalize(b.Layout.Columns)
	}
	b.UpdatedAt = now
	if b.ID == 0 {
		b.CreatedAt = now
		res, err := s.Exec(ctx, `INSERT INTO wallboards(name, sort_order, layout, panels, share_enabled, share_token, created_at, updated_at)
			VALUES (?,?,?,?,0,'',?,?)`, b.Name, b.SortOrder, jsonString(b.Layout), jsonString(b.Panels), fmtTime(now), fmtTime(now))
		if err != nil {
			return b, err
		}
		b.ID, _ = res.LastInsertId()
		b.Share = model.WallboardLink{}
		return b, nil
	}
	res, err := s.Exec(ctx, `UPDATE wallboards SET name=?, sort_order=?, layout=?, panels=?, updated_at=? WHERE id=?`,
		b.Name, b.SortOrder, jsonString(b.Layout), jsonString(b.Panels), fmtTime(now), b.ID)
	if err != nil {
		return b, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return b, ErrNotFound
	}
	current, err := s.GetWallboard(ctx, b.ID)
	if err != nil {
		return b, err
	}
	b.Share = current.Share
	return b, nil
}

// SetWallboardShare switches projection on or off and sets the token. Passing
// an empty token with enabled leaves the existing one in place, so re-enabling
// a board does not silently change an address somebody has already typed into
// a display.
func (s *Store) SetWallboardShare(ctx context.Context, id int64, enabled bool, token string) (model.Wallboard, error) {
	current, err := s.GetWallboard(ctx, id)
	if err != nil {
		return current, err
	}
	if token == "" {
		token = current.Share.Token
	}
	if !enabled {
		// Withdrawing an address means the token stops working, not that it
		// is kept around waiting to be switched back on.
		token = ""
	}
	if _, err := s.Exec(ctx, `UPDATE wallboards SET share_enabled=?, share_token=?, updated_at=? WHERE id=?`,
		boolInt(enabled), token, fmtTime(time.Now()), id); err != nil {
		return current, err
	}
	return s.GetWallboard(ctx, id)
}

// DeleteWallboard removes a wallboard.
func (s *Store) DeleteWallboard(ctx context.Context, id int64) error {
	res, err := s.Exec(ctx, "DELETE FROM wallboards WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
