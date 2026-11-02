package core

import (
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
)

// AdmitUtilityStatementStatsLocked joins the genuine utility owner's decided
// identity to its early hook frame. Engine.Mu is held; no role resolution or
// settings callback runs below the collector lock. A failed decision passes the
// current principal, preserving normalization admission without a successful phase.
func (s *StatementScope) AdmitUtilityStatementStatsLocked(statement parser.Statement, target authorization.PrincipalRef) {
	frame := s.statementStats
	if frame == nil || frame.admitted {
		return
	}
	frame.prospective, frame.utilityTarget = target, true
	s.beginStatementStatsLocked(statement, nil, frame.key.UserID)
}

// Capture the effective runtime actor, not a descriptor retained by analysis.
// This is a scalar projection of the same physical/local/restricted/RI authority
// used by the executing host; it does not resolve another role or grant access.
func (s *StatementScope) captureStatementStatsTerminal(frame *statementStatsFrame) {
	if frame == nil || frame.finished || frame.failed || frame.terminalCaptured || !frame.admitted {
		return
	}
	user := s.Op.RoleState().CurrentUser
	if owner := OperationReplayOrZero(s.Op).MaterializedOwner; owner != (authorization.PrincipalRef{}) {
		user = owner
	}
	if owner, active := s.Op.EffectiveUserOwner(); active {
		user = owner
	}
	if s.Op.Restricted == nil && s.ForeignActionAuth != nil {
		user = s.ForeignActionAuth.State.CurrentUser
	}
	topLevel := s.statementStatsDepth == 0
	if !frame.utility && s.statementStatsUtilityBody {
		topLevel = false
	}
	enabled := true
	if !frame.utility {
		enabled, _ = s.statementStatsPolicyAt(false, topLevel)
	}
	frame.terminalKey, frame.terminalCaptured, frame.terminalEnabled = frame.key, true, enabled
	frame.terminalKey.UserID = uint32(user.ID())
	frame.terminalKey.TopLevel = topLevel
}

// Owner-only teardown (native transaction APIs, replacement or session end) has
// no live StatementScope. Normal disposal samples the physical owner's current
// settings/identity; failed or ended owners only release the admission. Explicit
// SQL/nested disposal captures its lexical provenance before reaching this path.
func (f *statementStatsFrame) captureOwnerTerminal() {
	if f.terminalCaptured || f.session == nil {
		return
	}
	s := f.session
	settings := s.Settings
	settings.Mu.Lock()
	s.Mu.Lock()
	f.terminalKey, f.terminalCaptured = f.key, true
	f.terminalKey.TopLevel = true // Owner-only teardown runs outside SQL utility/executor nesting.
	if s.Authorization != nil {
		f.terminalKey.UserID = uint32(s.Authorization.State.CurrentUser.ID())
		track := settings.Value("pg_stat_statements.track")
		f.terminalEnabled = f.utility || track == "all" || track == "top"
	}
	s.Mu.Unlock()
	settings.Mu.Unlock()
}

// Capture retained executors before transaction settings/identity are restored.
// Actual producer disposal remains in its existing unlocked cleanup lane.
func captureOwnerStatementStats(t *session.TransactionOwner, scope *StatementScope) {
	capture := func(frame *statementStatsFrame) {
		if frame == nil {
			return
		}
		if scope != nil {
			scope.captureStatementStatsTerminal(frame)
		} else {
			frame.captureOwnerTerminal()
		}
	}
	payload := PayloadOf(t.Session)
	if payload.Cursors != nil {
		for _, cursor := range payload.Cursors.Entries {
			if cursor.tx == t.Ref {
				capture(cursor.statementStats)
			}
		}
	}
	if payload.PreparedSQL != nil {
		for _, binding := range payload.PreparedSQL.Bindings() {
			if portal := protocolPortalOf(binding); portal != nil && portal.Transaction == t.Ref {
				capture(portal.statementStats)
			}
		}
	}
}

func (f *statementStatsFrame) finish(success bool) {
	if f == nil || f.finished {
		return
	}
	f.finished = true
	if !f.admitted {
		f.pending, f.session = nil, nil
		return
	}
	defer func() {
		f.store.Release(f.token)
		f.source, f.session = "", nil
	}()
	if success && f.executed && !f.failed {
		f.captureOwnerTerminal()
		if f.terminalEnabled {
			f.store.RecordExecution(f.token, statistics.StatementRecord{Key: f.terminalKey, Query: f.source, At: time.Now(), Milliseconds: float64(f.elapsed) / float64(time.Millisecond), Rows: f.rows})
		}
	}
}
