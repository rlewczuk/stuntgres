package core

import "stuntgres/internal/engine/ids"

// IndexStart switches the active existing REINDEX target at its real builder.
func (p *indexProgress) IndexStart(parent, target ids.ObjectAddress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.command != "REINDEX" {
		return
	}
	p.relation, p.target = parent.ObjectID, target.ObjectID
	p.phase = "initializing"
	p.blocksTotal, p.blocksDone, p.tuplesTotal = 0, 0, 0
}

func (p *indexProgress) Partitions(total, done int) {
	p.mu.Lock()
	p.partitionsTotal, p.partitionsDone = int64(total), int64(done)
	p.mu.Unlock()
}

func (s *StatementScope) IndexProgressCommand(command string, target ids.OID) {
	p := s.indexProgress
	if p == nil {
		return
	}
	p.mu.Lock()
	p.command, p.target = command, target
	p.mu.Unlock()
}

func (s *StatementScope) IndexProgressPhase(phase string) {
	p := s.indexProgress
	if p == nil {
		return
	}
	p.mu.Lock()
	p.phase = phase
	p.mu.Unlock()
}

// Changing the current snapshot holder must not advance an original-cohort
// ordinal that the wait loop has not actually settled.
func (s *StatementScope) IndexProgressWaitPID(pid int64) {
	p := s.indexProgress
	if p == nil {
		return
	}
	p.mu.Lock()
	p.lockerPID = pid
	p.mu.Unlock()
}

func (s *StatementScope) IndexProgressWaitDone(done int) {
	p := s.indexProgress
	if p == nil {
		return
	}
	p.mu.Lock()
	p.lockersDone = int64(done)
	p.mu.Unlock()
}

func (s *StatementScope) IndexProgressWait(total, done int, pid int64) {
	p := s.indexProgress
	if p == nil {
		return
	}
	p.mu.Lock()
	p.lockersTotal, p.lockersDone, p.lockerPID = int64(total), int64(done), int64(pid)
	p.mu.Unlock()
}
