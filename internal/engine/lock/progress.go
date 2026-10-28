package lock

// ProgressPID returns the same public participant identity used by pg_locks,
// without allocating an inspection snapshot. Prepared transactions have PID0.
func (m *Manager) ProgressPID(t *Ticket) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pid, visible, err := BlockingPid(t.session, t.detached, t.session.Domain())
	if err != nil || !visible {
		return 0, err
	}
	return int64(pid), nil
}
