package engine

import (
	"context"
	"fmt"
	"sync"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// databaseCluster is owned by the pointer-stable mutex shared by its runtimes.
// targets is immutable after each publication; runtimes retain independent
// storage, object domains, visibility and session registries.
type databaseCluster struct {
	mu            sync.Mutex
	roles         access.RoleStore
	locks         *lock.Manager
	roleDomain    ids.Domain
	defaultTarget *Engine
	targets       map[string]*Engine
	nextOID       ids.OID
	seed          *Engine
	// Immutable queue head root; all runtimes use this cluster's mu.
	notificationHead *session.NotificationQueuePosition
	// Seed initialization never holds mu and never invokes host callbacks.
	seedOnce  sync.Once
	seedError error
}

// catalogs yields the catalog store of every registered runtime. A nil
// cluster yields none.
func (c *databaseCluster) catalogs(yield func(*catalog.Store) bool) {
	if c == nil {
		return
	}
	for _, runtime := range c.targets {
		if !yield(&runtime.catalog) {
			return
		}
	}
}

// SelectDatabase resolves an exact decoded NAME, never an opaque host key.
func (e *Engine) SelectDatabase(ctx context.Context, name string) (*Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	target := e.cluster.targets[name]
	if target == nil {
		return nil, &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "3D000", Message: fmt.Sprintf(`database "%s" does not exist`, name)}
	}
	return target, nil
}

func (e *Engine) publishDatabaseFrozenXIDLocked() {
	old := e.catalog.State.MustDatabase(e.catalog.DatabaseAddress())
	if old.FrozenXID == e.databaseFrozenXID {
		return
	}
	database := *old
	database.FrozenXID = e.databaseFrozenXID
	next := catalog.CloneState(e.catalog.State)
	next.SetDescriptor(database.Address(), &database)
	roots := e.catalog.ProjectDatabasesLocked(next, e.cluster.catalogs)
	e.catalog.PublishLocked(next)
	roots.InstallLocked()
}
