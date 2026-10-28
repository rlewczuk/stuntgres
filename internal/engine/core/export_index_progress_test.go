package core

import "stuntgres/internal/engine/pgcatalog"

type IndexProgressEntry = indexProgress

func ScopeIndexProgressEntry(s *StatementScope) *IndexProgressEntry       { return s.indexProgress }
func ScopeEndIndexProgressEntry(s *StatementScope, p *IndexProgressEntry) { s.endIndexProgress(p) }
func IndexProgressAttempt(p *IndexProgressEntry) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempt
}

func IndexProgressSnapshot(p *IndexProgressEntry) pgcatalog.CreateIndexProgressFact {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pgcatalog.CreateIndexProgressFact{Relation: p.relation, Index: p.target, Command: p.command, Phase: p.phase, BlocksTotal: p.blocksTotal, BlocksDone: p.blocksDone, TuplesTotal: p.tuplesTotal, PartitionsTotal: p.partitionsTotal, PartitionsDone: p.partitionsDone, LockersTotal: p.lockersTotal, LockersDone: p.lockersDone, CurrentLockerPID: p.lockerPID}
}
