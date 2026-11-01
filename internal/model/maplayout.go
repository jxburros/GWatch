package model

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

// MapLayout is how the network map's clusters layout has been arranged by
// hand: where nodes were put, and which clusters are folded away. There is
// one, shared by everyone who looks at the map — the page, a dashboard
// widget, a presentation — so an arrangement made on one computer is the one
// every other computer sees. Only an administrator saves it; anyone else may
// move nodes about in their own browser without it being kept.
//
// Positions are in the map's drawing units at the medium node size, with the
// biggest tree's root near 0,0; the browser scales them for the other sizes.
// A node with no position is placed by the layout itself, next to its parent.
type MapLayout struct {
	Positions map[string]MapPosition `json:"positions"`
	// Collapsed lists the nodes whose clusters are folded into them.
	Collapsed []int64   `json:"collapsed"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// MapPosition is one node's place on the map. Pinned is a node someone put
// there; an unpinned one was carried along with a node above it, or kept
// where the layout put it when the map was first arranged, and "Tidy up
// unpinned" may move it again.
type MapPosition struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Pinned bool    `json:"pinned,omitempty"`
}

// MapLayoutSettingKey is the settings key the arrangement is kept under. It
// travels in a backup like the rest of the configuration.
const MapLayoutSettingKey = "netmap.layout"

// Limits on a layout: generous for any real network, small enough that a
// layout cannot be used to store anything else.
const (
	MaxMapLayoutEntries = 20000
	MaxMapCoordinate    = 1e6
)

// Clean checks a layout before it is stored, keeping only nodes in known (by
// id) when known is not nil. Positions must be finite and within
// MaxMapCoordinate; anything else is refused rather than quietly fixed, since
// it can only come from a client that is not the map.
func (l *MapLayout) Clean(known map[int64]bool) error {
	if len(l.Positions) > MaxMapLayoutEntries || len(l.Collapsed) > MaxMapLayoutEntries {
		return fmt.Errorf("a map layout holds at most %d nodes", MaxMapLayoutEntries)
	}
	pos := make(map[string]MapPosition, len(l.Positions))
	for k, p := range l.Positions {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("map layout: %q is not a node id", k)
		}
		for _, v := range []float64{p.X, p.Y} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > MaxMapCoordinate {
				return fmt.Errorf("map layout: the position of node %d is out of range", id)
			}
		}
		if known != nil && !known[id] {
			continue
		}
		// Rounded to a tenth of a unit: finer than anyone can place a node.
		pos[strconv.FormatInt(id, 10)] = MapPosition{X: math.Round(p.X*10) / 10, Y: math.Round(p.Y*10) / 10, Pinned: p.Pinned}
	}
	l.Positions = pos
	seen := map[int64]bool{}
	col := []int64{}
	for _, id := range l.Collapsed {
		if id <= 0 {
			return fmt.Errorf("map layout: %d is not a node id", id)
		}
		if seen[id] || (known != nil && !known[id]) {
			continue
		}
		seen[id] = true
		col = append(col, id)
	}
	sort.Slice(col, func(i, j int) bool { return col[i] < col[j] })
	l.Collapsed = col
	return nil
}
