package hostmon

import (
	"context"
	"errors"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// The services, programs, containers and virtual machines this computer
// watches (#62).
//
// A machine with an agent is told what to watch through its collection
// settings, which the agent fetches. The computer GWatch runs on has no agent
// and no check-in, so its list is kept here, in the settings table, and
// handed straight to the collector. Only the watch lists are kept: what else
// this computer reports is not narrowed, as it never has been.

// localWatchSetting is the settings key the list is stored under.
const localWatchSetting = "local_watch"

// LocalWatch is what this computer watches.
func (m *Monitor) LocalWatch() model.CollectSelection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.watch
}

// SetLocalWatch stores and applies what this computer watches. The next
// sample reports it; an empty selection stops watching.
func (m *Monitor) SetLocalWatch(ctx context.Context, sel model.CollectSelection) (model.CollectSelection, error) {
	sel = sel.WatchOnly().Normalize()
	if err := sel.Validate(); err != nil {
		return model.CollectSelection{}, err
	}
	if m.store != nil {
		if err := m.store.PutSetting(ctx, localWatchSetting, sel); err != nil {
			return model.CollectSelection{}, err
		}
	}
	m.applyWatch(sel)
	return sel, nil
}

// loadLocalWatch reads the stored list when sampling starts. A list that
// cannot be read is left empty — nothing watched — which is what a computer
// had before there was a list.
func (m *Monitor) loadLocalWatch(ctx context.Context) {
	if m.store == nil {
		return
	}
	var sel model.CollectSelection
	if err := m.store.GetSetting(ctx, localWatchSetting, &sel); err != nil {
		if !errors.Is(err, store.ErrNotFound) && m.OnError != nil {
			m.OnError(err)
		}
		return
	}
	m.applyWatch(sel.Normalize())
}

func (m *Monitor) applyWatch(sel model.CollectSelection) {
	m.mu.Lock()
	m.watch = sel
	m.mu.Unlock()
	m.collector.SetWatch(sel)
}
