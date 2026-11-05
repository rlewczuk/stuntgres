package core

import (
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsCountersPhase(t *testing.T) {
	now := time.Now()
	store := statistics.NewStatements(20, now)
	frame := func(id int64) *statementStatsFrame {
		key := statistics.StatementKey{DatabaseID: 1, QueryID: id}
		return &statementStatsFrame{store: store, key: key, terminalKey: key, token: store.Begin(key,key,"real phase owner",now), admitted: true, planning: true, terminalCaptured: true, terminalEnabled: true}
	}
	parent, child := frame(1), frame(2)
	parent.planStart = now
	child.planAccepted, child.executed, child.parent = true, true, parent
	s := &StatementScope{statementStats: child}
	work := statistics.StatementCounters{SharedRead: 1, WALRecords: 1, WALBytes: 72}
	s.recordStatementCounters(work)
	if child.execCounters != work || parent.planCounters != work || parent.execCounters != (statistics.StatementCounters{}) {
		t.Fatal("actual phase ancestry", parent, child)
	}
	child.finish(true)
	if child.parent != nil || parent.planCounters != work {
		t.Fatal("child terminal retained/merged ancestry", parent, child)
	}
	s.statementStats = parent
	s.statementStatsPlanBuilt()
	s.statementStatsPlanAccepted()
	s.statementStatsPlanAccepted()
	if parent.planCounters != (statistics.StatementCounters{}) {
		t.Fatal("accepted plan retained pending work")
	}
	// An untracked child's reached work still belongs to its active parent.
	s.statementStats, s.statementStatsParent = nil, parent
	s.recordStatementCounters(statistics.StatementCounters{SharedHit: 2})
	parent.executed, parent.failed = true, true
	parent.finish(false)
	var p, c statistics.StatementEntry
	for _, entry := range store.Snapshot() {
		if entry.Key.QueryID == 1 { p = entry } else if entry.Key.QueryID == 2 { c = entry }
	}
	if p.Planning.Count != 1 || p.Execution.Count != 0 || p.Counters != work || c.Execution.Count != 1 || c.Counters != work {
		t.Fatal("independent accepted plan/child versus rejected outer execution", p,c)
	}
	// An unfinished or canceled planning attempt publishes no pending vector.
	rejected := frame(3)
	s.statementStats, s.statementStatsParent = rejected, nil
	rejected.planStart = now
	s.recordStatementCounters(work)
	rejected.finish(false)
	for _, entry := range store.Snapshot() {
		if entry.Key.QueryID == 3 { t.Fatal("rejected planning became visible", entry) }
	}
}

func TestPgStatStatementsCountersPortalAncestry(t *testing.T) {
	parent := &statementStatsFrame{utility: true}
	retained := &statementStatsFrame{planAccepted: true, parent: parent}
	s := &StatementScope{statementStats: retained}
	portal := &protocolPortal{}
	s.transferStatementStatsPlanningBind(portal)
	if portal.statementStats != retained || retained.parent != nil || s.statementStats != nil {
		t.Fatal("transfer retained lexical sink")
	}
	s.statementStats = parent
	previous := s.activateStatementCounters(retained)
	s.recordStatementCounters(statistics.StatementCounters{SharedRead: 1})
	s.restoreStatementCounters(retained, previous)
	if retained.execCounters.SharedRead != 1 || parent.execCounters.SharedRead != 1 || retained.parent != nil || s.statementStats != parent {
		t.Fatal("resume did not borrow/release genuine enclosing phase", parent, retained)
	}
	s.statementStats = nil
	previous = s.activateStatementCounters(retained)
	s.recordStatementCounters(statistics.StatementCounters{SharedHit: 1})
	s.restoreStatementCounters(retained, previous)
	if parent.execCounters.SharedHit != 0 || retained.execCounters.SharedHit != 1 || retained.parent != nil {
		t.Fatal("suspended ancestry was resurrected", parent, retained)
	}
}
