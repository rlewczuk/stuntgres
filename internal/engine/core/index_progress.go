package core

import (
	"context"
	"sync"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
)

// Membership belongs to the cluster mutex. All changing command facts belong
// to mu; builders never acquire the cluster mutex while holding this mutex.
type indexProgress struct {
	mu sync.Mutex

	session  ids.SessionID
	database ids.OID
	owner    authorization.PrincipalRef

	relation, target                     ids.OID
	command, phase                       string
	lockersTotal, lockersDone, lockerPID int64
	partitionsTotal, partitionsDone      int64
	blocksTotal, blocksDone, tuplesTotal int64
	versions                             int
	attempt                              uint64
}

// BeginCreateIndexProgressLocked starts admitted work, never a table-lock wait.
// The caller holds Engine.Mu. Retries retain the authenticated command identity.
func (s *StatementScope) BeginCreateIndexProgressLocked(relation, target ids.ObjectAddress) index.ProgressBinding {
	if s.indexProgress == nil {
		e := s.Engine()
		p := &indexProgress{
			session:  s.Op.Session.Token().ID(),
			database: e.Catalog.DatabaseAddress().ObjectID,
			relation: relation.ObjectID,
			owner:    s.Op.Session.CountedAuthenticated,
			command:  "CREATE INDEX",
			phase:    "initializing",
		}
		if e.Cluster.indexProgress == nil {
			e.Cluster.indexProgress = make(map[ids.SessionID]*indexProgress)
		}
		e.Cluster.indexProgress[p.session] = p
		s.indexProgress = p
	} else {
		p := s.indexProgress
		p.mu.Lock()
		p.phase = "initializing"
		p.blocksTotal, p.blocksDone, p.tuplesTotal = 0, 0, 0
		p.mu.Unlock()
	}
	return index.ProgressBinding{Index: target, Observer: s.indexProgress}
}

func (s *StatementScope) endIndexProgress(p *indexProgress) {
	if p == nil {
		return
	}
	e := s.Engine()
	e.Mu.Lock()
	if e.Cluster.indexProgress[p.session] == p {
		delete(e.Cluster.indexProgress, p.session)
	}
	if s.indexProgress == p {
		s.indexProgress = nil
	}
	e.Mu.Unlock()
}

func (p *indexProgress) ScanStart(n int, method catalog.IndexMethod) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = "building index"
	if method == catalog.BtreeMethod {
		p.phase = "building index: scanning table"
	}
	p.versions = n
	p.attempt++
	p.blocksTotal = int64(n / 128)
	if n%128 != 0 {
		p.blocksTotal++
	}
	p.blocksDone, p.tuplesTotal = 0, 0
}

func (p *indexProgress) ScanDone(n int) {
	// Only the serial builder reads/writes versions. Captures never read it.
	// Publish completed logical blocks, including the final partial block.
	if n%128 != 0 && n != p.versions {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocksDone = int64(n / 128)
	if n == p.versions {
		p.blocksDone = p.blocksTotal
	}
}

func (p *indexProgress) ScanFinish(n int, method catalog.IndexMethod) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = "building index"
	if method == catalog.BtreeMethod {
		p.phase = "building index: sorting live tuples"
		p.blocksTotal, p.blocksDone = 0, 0
	}
	if method == catalog.BtreeMethod || method == catalog.HashMethod {
		p.tuplesTotal = int64(n)
	}
}

// VisitCreateIndexProgress runs under the cluster mutex. Entry snapshots are
// coherent, detached, and authorized against the observer's fresh current role.
func (r EngineCatalogRuntime) VisitCreateIndexProgress(ctx context.Context, visit func(pgcatalog.CreateIndexProgressFact) error) error {
	if len(r.engine.Cluster.indexProgress) == 0 {
		return ctx.Err()
	}
	viewer, err := pgcatalog.NewProgressViewer(ctx, r.Auth.Roles, r.Auth.State.CurrentUser)
	if err != nil {
		return err
	}
	for _, p := range r.engine.Cluster.indexProgress {
		if err := ctx.Err(); err != nil {
			return err
		}
		visible := viewer.Visible(p.owner)
		p.mu.Lock()
		f := pgcatalog.CreateIndexProgressFact{
			PID:              int64(p.session),
			Database:         p.database,
			Relation:         p.relation,
			Index:            p.target,
			Command:          p.command,
			Phase:            p.phase,
			LockersTotal:     p.lockersTotal,
			LockersDone:      p.lockersDone,
			CurrentLockerPID: p.lockerPID,
			BlocksTotal:      p.blocksTotal,
			BlocksDone:       p.blocksDone,
			TuplesTotal:      p.tuplesTotal,
			PartitionsTotal:  p.partitionsTotal,
			PartitionsDone:   p.partitionsDone,
			Visible:          visible,
		}
		p.mu.Unlock()
		if d, ok := r.engine.Catalog.State.Database(ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: p.database}); ok {
			f.DatabaseExists = true
			f.DatabaseName = d.Name()
		}
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}
