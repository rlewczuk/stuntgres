package core

import (
	"context"
	"sync"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// AnalyzeProgress belongs to one admitted target. Engine.Mu owns membership;
// mu owns changing facts. The only nested lock order is engine then entry.
// The serial command owner updates facts without acquiring the engine mutex.
type AnalyzeProgress struct {
	mu                          sync.Mutex
	scope                       *StatementScope
	session                     ids.SessionID
	database, relation          ids.OID
	owner                       authorization.PrincipalRef
	phase                       string
	sampleTotal, sampleDone     int64
	extendedTotal, extendedDone int64
	childrenTotal, childrenDone int64
	currentChild                ids.OID
}

// BeginAnalyzeProgressLocked starts a fresh, zeroed target after admission.
// The caller holds Engine.Mu; no provider callback or target wait runs here.
func (s *StatementScope) BeginAnalyzeProgressLocked(relation ids.ObjectAddress) *AnalyzeProgress {
	e := s.Engine()
	p := &AnalyzeProgress{scope: s, session: s.Op.Session.Token().ID(), database: e.Catalog.DatabaseAddress().ObjectID, relation: relation.ObjectID, owner: s.Op.Session.CountedAuthenticated, phase: "initializing"}
	if e.Cluster.analyzeProgress == nil {
		e.Cluster.analyzeProgress = make(map[ids.SessionID]*AnalyzeProgress)
	}
	e.Cluster.analyzeProgress[p.session] = p
	s.analyzeProgress = p
	return p
}

// End removes only this target. It is idempotent, including after a newer target
// replaces it. The caller holds neither the engine nor entry mutex.
func (p *AnalyzeProgress) End() {
	if p == nil {
		return
	}
	s := p.scope
	e := s.Engine()
	e.Mu.Lock()
	if e.Cluster.analyzeProgress[p.session] == p {
		delete(e.Cluster.analyzeProgress, p.session)
	}
	if s.analyzeProgress == p {
		s.analyzeProgress = nil
	}
	e.Mu.Unlock()
}

func (p *AnalyzeProgress) Phase(phase string) {
	p.mu.Lock()
	p.phase = phase
	p.mu.Unlock()
}

// Logical blocks approximate physical sampling only, without addition overflow.
func analyzeLogicalBlocks(n int) int64 {
	blocks := int64(n / 128)
	if n%128 != 0 {
		blocks++
	}
	return blocks
}

func (p *AnalyzeProgress) SampleStart(total int) {
	p.mu.Lock()
	p.sampleTotal, p.sampleDone = analyzeLogicalBlocks(total), 0
	p.mu.Unlock()
}

func (p *AnalyzeProgress) SampleDone(completed, total int) {
	// Do not take a mutex on each row: only whole blocks and the final prefix.
	if completed%128 != 0 && completed != total {
		return
	}
	done := int64(completed / 128)
	blocks := analyzeLogicalBlocks(total)
	if completed == total {
		done = blocks
	}
	p.mu.Lock()
	p.sampleTotal, p.sampleDone = blocks, done
	p.mu.Unlock()
}

func (p *AnalyzeProgress) ChildrenStart(total int) {
	p.mu.Lock()
	p.childrenTotal, p.childrenDone = int64(total), 0
	p.mu.Unlock()
}

func (p *AnalyzeProgress) ChildStart(relation ids.OID) {
	p.mu.Lock()
	p.currentChild, p.sampleTotal, p.sampleDone = relation, 0, 0
	p.mu.Unlock()
}

func (p *AnalyzeProgress) ChildDone(done int) {
	p.mu.Lock()
	p.childrenDone = int64(done)
	p.mu.Unlock()
}

func (p *AnalyzeProgress) ExtendedStart(total int) {
	p.mu.Lock()
	p.phase, p.extendedTotal = "computing extended statistics", int64(total)
	// PostgreSQL retains prior-pass computed until a real definition completes.
	p.mu.Unlock()
}

func (p *AnalyzeProgress) ExtendedDone(done int) {
	p.mu.Lock()
	p.extendedDone = int64(done)
	p.mu.Unlock()
}

// VisitAnalyzeProgress captures coherent detached facts under Engine.Mu, using
// fresh observer authority and the immutable authenticated worker principal.
func (r *catalogRuntime) VisitAnalyzeProgress(ctx context.Context, visit func(pgcatalog.AnalyzeProgressFact) error) error {
	if len(r.engine.Cluster.analyzeProgress) == 0 {
		return ctx.Err()
	}
	viewer, err := pgcatalog.NewProgressViewer(ctx, r.auth.Roles, r.auth.State.CurrentUser)
	if err != nil {
		return err
	}
	for _, p := range r.engine.Cluster.analyzeProgress {
		if err := ctx.Err(); err != nil {
			return err
		}
		visible := viewer.Visible(p.owner)
		p.mu.Lock()
		f := pgcatalog.AnalyzeProgressFact{PID: int64(p.session), Database: p.database, Relation: p.relation, Phase: p.phase, SampleBlocksTotal: p.sampleTotal, SampleBlocksScanned: p.sampleDone, ExtendedStatsTotal: p.extendedTotal, ExtendedStatsComputed: p.extendedDone, ChildTablesTotal: p.childrenTotal, ChildTablesDone: p.childrenDone, CurrentChild: p.currentChild, Visible: visible}
		p.mu.Unlock()
		if d, ok := r.engine.Catalog.State.Database(ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: p.database}); ok {
			f.DatabaseExists, f.DatabaseName = true, d.Name()
		}
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}
