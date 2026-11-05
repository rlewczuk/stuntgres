package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
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
	AdmissionMu    sync.Mutex
	admissions     map[*Engine]int
	templateCopies map[*Engine]*databaseTemplateReaders
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

// Catalogs yields the catalog store of every registered runtime. A nil
// cluster yields none.
func (c *DatabaseCluster) Catalogs(yield func(*catalog.Store) bool) {
	if c == nil {
		return
	}
	for _, runtime := range c.Targets {
		if !yield(&runtime.Catalog) {
			return
		}
	}
}

// SelectDatabase resolves an exact decoded NAME, never an opaque host key.
func (e *Engine) SelectDatabase(ctx context.Context, name string) (*Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	target := e.Cluster.Targets[name]
	if target == nil {
		return nil, &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "3D000", Message: fmt.Sprintf(`database "%s" does not exist`, name)}
	}
	return target, nil
}

func (e *Engine) publishDatabaseFrozenXIDLocked() {
	old := e.Catalog.State.MustDatabase(e.Catalog.DatabaseAddress())
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
