package core

import (
	"sync"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// ClusterProgress owns one admitted target. Engine.Mu owns map membership;
// mu owns mutable facts. Capture/installation order is Engine.Mu then mu.
// Builder callbacks acquire only mu, never engine/session/publication locks.
// Detached entries may still receive old callbacks, but cannot regain membership.
type ClusterProgress struct {
	mu        sync.Mutex
	fact      pgcatalog.ClusterProgressFact
	owner     authorization.PrincipalRef
	building  ids.ObjectAddress
	completed map[ids.ObjectAddress]bool
}

// BeginClusterProgressLocked starts after target admission and eligibility. A
// new target has its own owner, even when bare CLUSTER retains the statement.
// The caller holds Engine.Mu and owns the matching EndClusterProgress defer.
func (s *StatementScope) BeginClusterProgressLocked(relation ids.ObjectAddress, command string) *ClusterProgress {
	e := s.Engine()
	p := &ClusterProgress{
		fact: pgcatalog.ClusterProgressFact{
			PID:          int64(s.Op.Session.SessionToken.ID()),
			Database:     e.Catalog.DatabaseAddress().ObjectID,
			DatabaseName: s.Op.Session.DatabaseName,
			Relation:     relation.ObjectID,
			Command:      command,
			Phase:        "initializing",
		},
		owner: s.Op.Session.CountedAuthenticated,
	}
	if command == "CLUSTER" {
		p.completed = make(map[ids.ObjectAddress]bool)
	}
	if e.ClusterProgress == nil {
		e.ClusterProgress = make(map[ids.TxRef]*ClusterProgress)
	}
	e.ClusterProgress[s.TxRef] = p
	return p
}

// EndClusterProgress removes only this exact target. Old defers cannot erase a
// replacement target or a command admitted after catalog Reset/Restore.
func (e *Engine) EndClusterProgress(ref ids.TxRef, p *ClusterProgress) {
	e.Mu.Lock()
	if e.ClusterProgress[ref] == p {
		delete(e.ClusterProgress, ref)
	}
	e.Mu.Unlock()
}

func (p *ClusterProgress) SetPhase(phase string) {
	p.mu.Lock()
	p.fact.Phase = phase
	p.mu.Unlock()
}

// RewriteProgress is for FULL's actual heap rewrite and successful index work.
// Physical blocks remain exact zero: the native rewrite has no physical pages.
func (p *ClusterProgress) RewriteProgress(phase string, scanned, written, indexes int64) {
	p.mu.Lock()
	p.fact.Phase, p.fact.Scanned, p.fact.Written, p.fact.Indexes = phase, scanned, written, indexes
	p.mu.Unlock()
}

func (p *ClusterProgress) IndexStart(_ ids.ObjectAddress, address ids.ObjectAddress) {
	p.mu.Lock()
	p.building = address
	p.fact.Phase = "rebuilding index"
	p.mu.Unlock()
}

// Index-root scans are not CLUSTER heap rewriting. In the authorized metadata
// simulation all heap tuple/block counters and cluster_index_relid stay zero,
// B(0)=0. ScanFinish is not successful baseline/output attachment.
func (p *ClusterProgress) ScanStart(int, catalog.IndexMethod)  {}
func (p *ClusterProgress) ScanDone(int)                        {}
func (p *ClusterProgress) ScanFinish(int, catalog.IndexMethod) {}

func (p *ClusterProgress) IndexComplete(address ids.ObjectAddress) {
	p.mu.Lock()
	if p.building == address && !p.completed[address] {
		p.completed[address] = true
		p.fact.Indexes++
	}
	p.mu.Unlock()
}
