package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) SizeInquiry(kind functions.SizeKind, arg sqltype.Value, fork string, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	ctx := s.Op.Context()
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	e := s.Engine()
	e.Mu.Lock()
	captures, present, err := h.captureSizeLocked(ctx, kind, arg, fork)
	e.Mu.Unlock()
	if err != nil { return sqltype.Value{}, err }
	if !present { return sqltype.Null(result), nil }
	var sum int64
	for _, capture := range captures {
		components, err := capture.Forks(ctx)
		if err != nil { return sqltype.Value{}, err }
		var n int64
		if kind == functions.RelationSize {
			switch fork { case "main": n = components.Main; case "fsm": n = components.FSM; case "vm": n = components.VM; case "init": n = components.Init }
		} else {
			n, err = components.Total()
			if err != nil { return sqltype.Value{}, err }
		}
		sum, err = storage.AddSize(sum, n)
		if err != nil { return sqltype.Value{}, err }
	}
	return sqltype.Integer(result, sum)
}

// captureSizeLocked captures only headers, descriptors and immutable roots. The
// shared cluster mutex owns all runtime maps; arithmetic runs after its release.
func (h *statementHost) captureSizeLocked(ctx context.Context, kind functions.SizeKind, arg sqltype.Value, fork string) ([]storage.Allocation, bool, error) {
	s, e := h.scope(), h.scope().Engine()
	retained, err := h.catalogInquiryCatalog()
	if err != nil { return nil, false, err }
	if retained.Epoch() != e.Catalog.State.Epoch() { return nil, false, ids.ErrCatalogReplaced }
	view := e.PrivateCatalogLocked(s.PendingCatalog())
	if kind == functions.DatabaseSize || kind == functions.TablespaceSize {
		return h.captureAggregateSizeLocked(ctx, kind, arg, view)
	}
	oid, err := arg.ClassOID()
	if err != nil { return nil, false, err }
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	relation, found := view.Relation(address)
	if !found { return nil, false, nil }
	if kind == functions.RelationSize && fork != "main" && fork != "fsm" && fork != "vm" && fork != "init" {
		return nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid fork name", Hint: `Valid fork names are "main", "fsm", "vm", and "init".`}
	}
	var captures []storage.Allocation
	if kind != functions.IndexesSize {
		captures = append(captures, captureAllocationLocked(e, s, view, relation))
	}
	if kind == functions.IndexesSize || kind == functions.TotalRelationSize {
		for _, descriptor := range view.Descriptors() {
			if err := ctx.Err(); err != nil { return nil, false, err }
			idx, ok := descriptor.(*catalog.Relation)
			if ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == address {
				captures = append(captures, captureAllocationLocked(e, s, view, idx))
			}
		}
	}
	return captures, true, nil
}

func captureAllocationLocked(e *Engine, scope *StatementScope, view catalog.Read, relation *catalog.Relation) storage.Allocation {
	a := storage.Allocation{Relation: relation}
	var pending *session.Journal
	if scope != nil { pending = scope.PendingCatalog() }
	if relation.SQLIndex != nil {
		a.Index = index.EffectiveRoot(e.Storage.IndexRoots, pending, relation.Address())
		a.Allocated = a.Index != nil
		a.Parent, _ = view.Relation(relation.SQLIndex.Parent)
		if stored := e.PrivateStorageLocked(pending, relation.SQLIndex.Parent); stored != nil { a.ParentVersions = stored.Versions }
		if scope != nil {
			if scope.ProjectedRoots { a.ParentOwn = scope.Stage.Rows[relation.SQLIndex.Parent] } else {
				a.ParentOwn = scope.OwnWrites[relation.SQLIndex.Parent]
				a.ParentStaged = scope.Stage.Rows[relation.SQLIndex.Parent]
				a.ReplaceOwn = scope.Stage.Replacing
			}
		}
		return a
	}
	if relation.Sequence != nil {
		if scope != nil { a.Allocated = e.SequenceGeneratorLocked(scope, relation.Address()) != nil } else { a.Allocated = e.Storage.Sequences[relation.Address()] != nil }
		return a
	}
	if stored := e.PrivateStorageLocked(pending, relation.Address()); stored != nil {
		a.Allocated, a.Versions = true, stored.Versions
		if scope != nil {
			if scope.ProjectedRoots { a.Own = scope.Stage.Rows[relation.Address()] } else {
				a.Own = scope.OwnWrites[relation.Address()]
				a.Staged = scope.Stage.Rows[relation.Address()]
				a.ReplaceOwn = scope.Stage.Replacing
			}
		}
	}
	return a
}

func (h *statementHost) captureAggregateSizeLocked(ctx context.Context, kind functions.SizeKind, arg sqltype.Value, view catalog.Read) ([]storage.Allocation, bool, error) {
	s, e := h.scope(), h.scope().Engine()
	class, label, privilege := ids.DatabaseClassID, "database", authorization.ConnectPrivilege
	if kind == functions.TablespaceSize { class, label, privilege = ids.TablespaceClassID, "tablespace", authorization.CreatePrivilege }
	var descriptor catalog.Descriptor
	if arg.Type().ID() == sqltype.Name {
		name, err := arg.Text()
		if err != nil { return nil, false, err }
		address := view.NameAddress(ids.NameKey{ClassID: class, Name: name})
		descriptor = view.Get(address)
		if descriptor == nil {
			code := "42704"
			if kind == functions.DatabaseSize { code = "3D000" }
			return nil, false, SQLError(code, fmt.Sprintf(`%s "%s" does not exist`, label, name))
		}
	} else {
		oid, err := arg.OID()
		if err != nil { return nil, false, err }
		descriptor = view.Get(ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(oid)})
		if descriptor == nil { return nil, false, SQLError("42704", fmt.Sprintf("%s with OID %d does not exist", label, oid)) }
	}
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil { return nil, false, err }
	auth.Catalog = view
	auth.Recapture(view)
	allowed := kind == functions.TablespaceSize && descriptor.Address().ObjectID == catalog.DefaultTablespaceOID
	if !allowed {
		allowed, _, err = auth.ObjectPrivilege(ctx, auth, auth.State.CurrentUser.ID(), descriptor.Address(), privilege, 0)
		if err != nil { return nil, false, err }
	}
	if !allowed {
		stats, found := auth.Roles.Lookup("pg_read_all_stats")
		if !found { return nil, false, fmt.Errorf("stuntgres: missing pg_read_all_stats role") }
		allowed, err = authorization.HasRole(ctx, auth.Roles, &e.RoleStore.Domain, auth.State.CurrentUser.ID(), stats.Ref.ID(), authorization.RoleUsage)
		if err != nil { return nil, false, err }
	}
	if !allowed { return nil, false, SQLError("42501", "permission denied for " + label + " " + descriptor.Name()) }
	if kind == functions.DatabaseSize && e.Cluster.Targets[descriptor.Name()] == nil {
		return nil, false, fmt.Errorf("stuntgres: database allocation runtime is not registered")
	}
	var captures []storage.Allocation
	for name, target := range e.Cluster.Targets {
		if err := ctx.Err(); err != nil { return nil, false, err }
		if kind == functions.DatabaseSize && name != descriptor.Name() { continue }
		targetView := target.Catalog.Current()
		var scope *StatementScope
		if target == e { scope, targetView = s, view }
		if kind == functions.DatabaseSize && targetView.Owner().DatabaseAddress() != descriptor.Address() {
			return nil, false, ids.ErrCatalogReplaced
		}
		for _, d := range targetView.Descriptors() {
			if err := ctx.Err(); err != nil { return nil, false, err }
			relation, ok := d.(*catalog.Relation)
			if !ok { continue }
			placement := catalog.DefaultTablespaceOID
			if catalog.SharedRelation(relation) { placement = catalog.GlobalTablespaceOID }
			if kind == functions.DatabaseSize && placement == catalog.GlobalTablespaceOID || kind == functions.TablespaceSize && placement != descriptor.Address().ObjectID { continue }
			a := captureAllocationLocked(target, scope, targetView, relation)
			if a.Allocated { captures = append(captures, a) }
		}
	}
	return captures, true, nil
}
