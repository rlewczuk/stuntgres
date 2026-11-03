package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"time"
)

// Cumulative facts remain database-local. Only the default provider's activity
// map is cluster-wide; callers never mirror activity into another provider.
func (e *Engine) activityProvider() *statistics.Provider {
	if e.Cluster != nil {
		return &e.Cluster.DefaultTarget.Statistics
	}
	return &e.Statistics
}

func activityTime() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func activityIdle(s *session.Session) {
	e := EngineOf(s)
	if e == nil || s.SessionToken.Private() != nil {
		return
	}
	application := ""
	if s.Settings != nil {
		s.Settings.Mu.Lock()
		application = s.Settings.Value("application_name")
		s.Settings.Mu.Unlock()
	}
	state, start := "idle", time.Time{}
	t := s.Transactions
	t.Mu.Lock()
	ref := t.Ref
	if t.State == session.TxAborted {
		state = "idle in transaction (aborted)"
		if len(t.Savepoints) != 0 {
			start = t.Start
		}
	} else if t.State != session.TxIdle {
		state, start = "idle in transaction", t.Start
	}
	t.Mu.Unlock()
	e.activityProvider().ReportIdle(s.SessionToken, application, state, ref, start, activityTime())
}

func (s *StatementScope) activityCommandIdentity() ids.CommandIdentity {
	return s.activityCommand
}

func (h *statementHost) FunctionActivitySleep(enter bool) {
	s := h.scope()
	if s == nil || s.Op == nil {
		return
	}
	s.Engine().activityProvider().ActivityWait(s.Op.Session.SessionToken, s.activityCommandIdentity(), enter)
}

// Engine.Mu is held. This is observational and never allocates an XID or a
// snapshot. Rejected safe candidates are not advertised by this operation.
func (e *Engine) advertiseActivitySnapshotLocked(ref ids.TxRef, snapshot *sqltype.Snapshot) {
	entry, ok := e.Visibility.Entries[ref]
	if !ok {
		return
	}
	entry.ActivityFullXmin = 0
	if snapshot != nil {
		entry.ActivityFullXmin = snapshot.Xmin()
	}
	e.Visibility.Entries[ref] = entry
}
