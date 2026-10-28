package tablecmds

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// This interpreter is reachable only inside the isolated Restore engine.
func RestoreIndexStorage(e *core.Engine, op *session.Operation, raw *parser.Update) error {
	name, attribute, properties, err := table.RestoreIndexStorageSyntax(raw)
	if err != nil { return err }
	if err := op.Context().Err(); err != nil { return err }
	auth, err := core.OperationAuthorization(op)
	if err != nil { return err }
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil { return err }
	if !actor.Superuser { return core.SQLError("42501", "permission denied for table pg_attribute") }
	e.Mu.Lock()
	if e.ImportTargets == nil || auth.Owner != &e.Catalog.Owner || auth.Session != op.Session || auth.RoleRecord != e.RoleStore.Published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.Catalog.Domain || auth.Catalog.State() != e.Catalog.State || auth.Catalog.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore index storage authority")
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser { e.Mu.Unlock(); return fmt.Errorf("stuntgres: invalid trusted restore index storage authority") }
	ref, err := e.ResolveRelationReference(auth, name, false)
	if err != nil { e.Mu.Unlock(); return err }
	old := auth.Catalog.MustRelation(ref.Address)
	slot := old.IndexForAttribute(attribute)
	if old.SQLIndex == nil || slot < 0 { e.Mu.Unlock(); return core.SQLError("0A000", table.RestoreIndexStorageBoundary) }
	next := old
	if properties.SetStorage {
		next = old.WithColumnStorage(slot, properties.ColumnStorage)
	}
	if properties.Rename && old.Columns[slot].Name != properties.Name {
		if other, exists := old.Index[properties.Name]; exists && other != slot {
			e.Mu.Unlock()
			return core.SQLError("42701", fmt.Sprintf(`column "%s" of relation "%s" already exists`, properties.Name, old.Name()))
		}
		copy := *next
		next = &copy
		next.Columns = slices.Clone(old.Columns)
		next.Index = maps.Clone(old.Index)
		delete(next.Index, old.Columns[slot].Name)
		next.Columns[slot].Name = properties.Name
		next.Index[properties.Name] = slot
	}
	if next == old { e.Mu.Unlock(); return nil }
	if err := table.BumpAlterRevision(old, next); err != nil { e.Mu.Unlock(); return err }
	return PublishRestoreRepairLocked(e, op, auth, next, ref.Epoch)
}
