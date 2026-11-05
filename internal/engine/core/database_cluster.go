package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

// DatabaseCluster is owned by the pointer-stable mutex shared by its runtimes.
// targets is immutable after each publication; runtimes retain independent
// storage, object domains, visibility and session registries.
type DatabaseCluster struct {
	// Real initialization events in PostgreSQL timestamp microseconds, immutable
	// after construction and never replaced with catalogs.
	StartedAt      int64
	ConfigLoadedAt int64
	// Cluster reset authorities, guarded by Mu. Database providers mirror these
	// instants; local catalog replacement is not a shared reset.
	ArchiverResetAt     time.Time
	BGWriterResetAt     time.Time
	CheckpointerResetAt time.Time
	WALResetAt          time.Time
	Mu                  sync.Mutex
	Roles               access.RoleStore
	Locks               *lock.Manager
	RoleDomain          ids.Domain
	DefaultTarget       *Engine
	Targets             map[string]*Engine
	NextOID             ids.OID
	// StatementStatistics survives database catalog replacement and session end.
	// Its private mutex is a leaf beneath every runtime/authority lock.
	StatementStatistics *statistics.Statements
	// Sparse logical read-cache state; lazily initialized under Mu. Its mutex
	// is a leaf and it never retains engines, catalog roots or source owners.
	buffercache *logicalBufferCache
	Seed        *Engine
	// AdmissionMu protects only reservation states/counts. Metadata admission
	// takes Mu first; terminal cleanup takes AdmissionMu without Mu or sessionMu.
	AdmissionMu                  sync.Mutex
	admissions                   map[*Engine]int
	admissionHead, admissionTail *DatabaseAdmission // Intrusive owning cohort, in admission order.
	// Both dimensions derive from the same single-use admission owner; neither
	// session publication nor SET ROLE adds a second physical reservation.
	principalAdmissions map[authorization.PrincipalRef]int
	templateCopies      map[*Engine]*databaseTemplateReaders
	// Immutable queue head root; all runtimes use this cluster's mu.
	notificationHead *session.NotificationQueuePosition
	// Active commands across every target, guarded by the shared engine mutex.
	copyProgress    map[ids.SessionID]*copyProgress
	indexProgress   map[ids.SessionID]*indexProgress
	analyzeProgress map[ids.SessionID]*AnalyzeProgress
	// Transient exclusion only; live registrations belong to the catalog.
	replicationOrigins                  map[ids.OID]replicationOriginReservation
	replicationOriginProgress           map[ids.OID]uint64
	replicationOriginOwners             map[ids.OID]*replicationOriginSession
	replicationOriginProgressGeneration map[ids.ObjectAddress]uint64
	// Logical WAL capability, stream positions and the slot registry.
	logical logicalCluster
	// Exported cuts share this cluster mutex with all runtime publication.
	snapshotSerial uint64
	snapshots      map[string]*exportedSnapshot
	// Seed initialization never holds mu and never invokes host callbacks.
	SeedOnce  sync.Once
	SeedError error
}

// Catalogs yields shared-authority holders: registered runtimes and the distinct
// retained default tombstone. It does not confer public database selection.
func (c *DatabaseCluster) Catalogs(yield func(*catalog.Store) bool) {
	if c == nil {
		return
	}
	for _, runtime := range c.Targets {
		if !yield(&runtime.Catalog) {
			return
		}
	}
	if c.DefaultTarget != nil && c.Targets[c.DefaultTarget.DatabaseName] != c.DefaultTarget {
		yield(&c.DefaultTarget.Catalog)
	}
}

// selectDatabaseLocked resolves only the selection mode, under cluster metadata.
// An empty opaque host key may own the original attachment, but an explicit
// empty database name never selects it. Default selection retains its identity.
func (c *DatabaseCluster) selectDatabaseLocked(selector DatabaseSelector) *Engine {
	if !selector.Explicit {
		target := c.DefaultTarget
		if target == nil || target.retired || c.Targets[target.DatabaseName] != target {
			return nil
		}
		return target
	}
	if selector.Name == "" {
		return nil
	}
	return c.Targets[selector.Name]
}

// SelectDatabase resolves an exact decoded NAME, never an opaque host key.
// Physical startup still requires AdmitDatabase's exact selector recheck.
func (e *Engine) SelectDatabase(ctx context.Context, name string) (*Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	target := e.Cluster.selectDatabaseLocked(DatabaseSelector{Name: name, Explicit: true})
	if target == nil {
		return nil, databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, name))
	}
	return target, nil
}

func (e *Engine) publishDatabaseFrozenXIDLocked() {
	old, ok := e.Catalog.State.Get(e.Catalog.DatabaseAddress()).(*catalog.Database)
	if !ok || e.retired {
		return
	}
	if old.FrozenXID == e.DatabaseFrozenXID {
		return
	}
	database := *old
	database.FrozenXID = e.DatabaseFrozenXID
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(database.Address(), &database)
	roots := e.Catalog.ProjectSharedLocked(next, e.Cluster.Catalogs)
	e.Catalog.PublishLocked(next)
	roots.InstallLocked()
}
