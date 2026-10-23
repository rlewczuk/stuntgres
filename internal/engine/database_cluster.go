package engine

import (
	"context"
	"fmt"
	"sync"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// databaseCluster is owned by the pointer-stable mutex shared by its runtimes.
// targets is immutable after each publication; runtimes retain independent
// storage, object domains, visibility and session registries.
type databaseCluster struct {
	mu            sync.Mutex
	roles         roleStore
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

// databaseOIDLocked reserves beyond local allocation frontiers, including
// transaction-private allocations not yet present in a published root. Imported
// definitions may lie beyond those frontiers, so inspect the actual roots too.
// No frontier changes until the complete database publication succeeds.
func (e *Engine) databaseOIDLocked(target *Engine) (ids.OID, error) {
	oid := e.cluster.nextOID
	advance := func(runtime *Engine) bool {
		if runtime.catalog.nextOID == 0 {
			return false
		}
		if runtime.catalog.nextOID > oid {
			oid = runtime.catalog.nextOID
		}
		return true
	}
	if oid == 0 || !advance(target) {
		return 0, fmt.Errorf("stuntgres: database identity exhausted")
	}
	for _, runtime := range e.cluster.targets {
		if !advance(runtime) {
			return 0, fmt.Errorf("stuntgres: database identity exhausted")
		}
	}
	used := func(runtime *Engine) bool {
		for address, descriptor := range runtime.catalog.state.Descriptors() {
			if (address.ClassID != ids.LargeObjectClassID && address.ObjectID == oid) || catalog.DefinitionOIDUsed(descriptor, oid) {
				return true
			}
		}
		for _, record := range runtime.catalog.state.DefaultACLs() {
			if record != nil && record.OID == oid {
				return true
			}
		}
		return false
	}
	for oid != ^ids.OID(0) {
		reserved := used(target)
		for _, runtime := range e.cluster.targets {
			if reserved {
				break
			}
			reserved = used(runtime)
		}
		if !reserved {
			return oid, nil
		}
		oid++
	}
	return 0, fmt.Errorf("stuntgres: database identity exhausted")
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

// databaseProjectionLocked prepares all roots before installing any. Descriptor
// publications are shared, but each catalog remains rooted in its own domain.
func (e *Engine) databaseProjectionLocked(next *catalog.State) map[*Engine]*catalog.State {
	if e.cluster == nil {
		return nil
	}
	var changed []catalog.Descriptor
	for address, descriptor := range next.Descriptors() {
		if address.ClassID == ids.DatabaseClassID && e.catalog.state.Get(address) != descriptor {
			changed = append(changed, descriptor)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	roots := make(map[*Engine]*catalog.State, len(e.cluster.targets))
	for _, target := range e.cluster.targets {
		root := next
		if target != e {
			root = catalog.CloneState(target.catalog.state)
		}
		for _, descriptor := range changed {
			if old := root.Get(descriptor.Address()); old != nil {
				root.UnbindName(catalog.DescriptorNameKey(old))
			}
			root.SetDescriptor(descriptor.Address(), descriptor)
			root.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
		}
		roots[target] = root
	}
	return roots
}

func installDatabaseProjectionLocked(roots map[*Engine]*catalog.State) {
	for target, root := range roots {
		target.catalog.state = root
	}
}

func (e *Engine) publishDatabaseFrozenXIDLocked() {
	old := e.catalog.state.MustDatabase(e.catalog.DatabaseAddress())
	if old.FrozenXID == e.databaseFrozenXID {
		return
	}
	database := *old
	database.FrozenXID = e.databaseFrozenXID
	next := catalog.CloneState(e.catalog.state)
	next.SetDescriptor(database.Address(), &database)
	roots := e.databaseProjectionLocked(next)
	e.catalog.state = next
	installDatabaseProjectionLocked(roots)
}
