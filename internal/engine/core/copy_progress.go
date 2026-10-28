package core

import (
	"context"
	"sync/atomic"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// Immutable identity plus command-owned counters. The cluster mutex protects
// membership only; updates can run at already-locked DML and external I/O seams.
type copyProgress struct {
	session            ids.SessionID
	database, relation ids.OID
	owner              authorization.PrincipalRef
	command            string
	bytes              atomic.Int64
	tuples             dml.CopyProgress
}

func (s *StatementScope) beginCopyProgress(relation ids.OID, from bool) *copyProgress {
	e := s.Engine()
	p := &copyProgress{session: s.Op.Session.Token().ID(), database: e.Catalog.DatabaseAddress().ObjectID, relation: relation, owner: s.Op.Session.CountedAuthenticated, command: "COPY TO"}
	if from {
		p.command = "COPY FROM"
	}
	e.Mu.Lock()
	if e.Cluster.copyProgress == nil {
		e.Cluster.copyProgress = make(map[ids.SessionID]*copyProgress)
	}
	e.Cluster.copyProgress[p.session] = p
	s.copyProgress = p
	e.Mu.Unlock()
	return p
}

func (s *StatementScope) endCopyProgress(p *copyProgress) {
	if p == nil {
		return
	}
	e := s.Engine()
	e.Mu.Lock()
	if e.Cluster.copyProgress[p.session] == p {
		delete(e.Cluster.copyProgress, p.session)
	}
	if s.copyProgress == p {
		s.copyProgress = nil
	}
	e.Mu.Unlock()
}
func (p *copyProgress) completeRow() { p.tuples.Processed.Add(1) }

// VisitCopyProgress requires the caller's shared engine mutex and emits detached
// facts. Authorization is fresh for each capture, never cached on the worker.
func (r EngineCatalogRuntime) VisitCopyProgress(ctx context.Context, visit func(pgcatalog.CopyProgressFact) error) error {
	if len(r.engine.Cluster.copyProgress) == 0 {
		return ctx.Err()
	}
	viewer, err := pgcatalog.NewProgressViewer(ctx, r.Auth.Roles, r.Auth.State.CurrentUser)
	if err != nil {
		return err
	}
	for _, p := range r.engine.Cluster.copyProgress {
		if err := ctx.Err(); err != nil {
			return err
		}
		f := pgcatalog.CopyProgressFact{PID: int64(p.session), Database: p.database, Relation: p.relation, Command: p.command, BytesProcessed: p.bytes.Load(), TuplesProcessed: p.tuples.Processed.Load(), TuplesExcluded: p.tuples.Excluded.Load(), TuplesSkipped: p.tuples.Skipped.Load(), Visible: viewer.Visible(p.owner)}
		database, ok := r.engine.Catalog.State.Database(ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: p.database})
		if ok {
			f.DatabaseExists = true
			f.DatabaseName = database.Name()
		}
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}
