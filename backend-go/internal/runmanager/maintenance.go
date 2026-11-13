package runmanager

import "time"

func (m *AiRunManager) Maintain(now time.Time) error {
	// Match the existing manager-to-store lock order used by interactions. A
	// finishing provider task still owns its maps until its own defer runs.
	m.guard.Lock()
	defer m.guard.Unlock()
	if err := m.store.ExpireAwaiting(now); err != nil {
		return err
	}
	if _, err := m.store.PurgeExpired(); err != nil {
		return err
	}
	active, err := m.store.ActiveRunIDs()
	if err != nil {
		return err
	}
	for id, w := range m.wakeups {
		if !active[id] && m.tasks[id] == nil {
			w.set()
			if m.waiters[id] == 0 {
				delete(m.wakeups, id)
			}
		}
	}
	for id := range m.requests {
		if !active[id] && m.tasks[id] == nil {
			delete(m.requests, id)
			delete(m.programContexts, id)
		}
	}
	for id := range m.programContexts {
		if !active[id] && m.tasks[id] == nil {
			delete(m.programContexts, id)
		}
	}
	return nil
}
