package contrib

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func pgFreespaceAuthority[A Authority](auth A, n plan.InstalledPgFreespaceExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidatePgFreespaceIdentity(view.Catalog, current, n.Ref); err != nil {
		return err
	}
	call := n.PgFreespace
	call.RoutineReference = current
	if err := extension.ValidatePgFreespaceCall(view.Catalog, current, call); err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege}); err != nil {
		return err
	}
	if !n.Kind.Set() {
		return nil
	}
	scalar, err := extension.PgFreespaceScalar(view.Catalog, current)
	if err != nil {
		return err
	}
	err = auth.Check(access.Requirement{Object: view.Ref(scalar.Address()), Privileges: authorization.ExecutePrivilege})
	if err != nil {
		return exec.SQLRoutineStatementContext(err, current.Name(), 1)
	}
	return err
}

// ValidatePgFreespace admits the preserved SQL-body call even when its
// parameter is NULL and the wrapper produces no relation pages.
func ValidatePgFreespace[A Authority](h Host[A], n plan.InstalledPgFreespaceExpr) error {
	if err := h.Active(); err != nil {
		return err
	}
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	auth, err := h.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	return pgFreespaceAuthority(auth, n)
}

// PgFreespaceTargetCheckable dispatches genuine descriptor kinds, before block
// bounds. Unlike visibility inquiries this operation reads no foreign buffers.
func PgFreespaceTargetCheckable(v catalog.Read, target *catalog.Relation) error {
	label := ""
	if target.RelationKind == catalog.CatalogRelation && target.CatalogView {
		label = "views"
	} else {
		switch target.RelationKind {
		case catalog.TableRelation, catalog.MaterializedViewRelation, catalog.CatalogRelation, catalog.SequenceRelation:
		case catalog.IndexRelation:
			if target.SQLIndex == nil {
				return ids.ErrCatalogReplaced
			}
			if parent, ok := v.Relation(target.SQLIndex.Parent); ok && parent.Partition != nil {
				label = "partitioned indexes"
			}
		case catalog.ViewRelation:
			label = "views"
		case catalog.ForeignRelation:
			label = "foreign tables"
		case catalog.PartitionedRelation:
			label = "partitioned tables"
		default:
			label = "composite types"
		}
	}
	if label == "" {
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf("relation %q does not have storage", target.Name()), Detail: "This operation is not supported for " + label + "."}
}

// PgFreespace captures only the caller's visible population, never the physical
// statistics owner cohort. Entry and exit are unlocked; owner precedes Engine
// when borrowing a view. Sizing and producer iteration run outside both locks.
func PgFreespace[A Authority](h Host[A], n plan.InstalledPgFreespaceExpr, oid uint32, block int64) (storage.FreespaceLayout, error) {
	if err := h.Active(); err != nil {
		return storage.FreespaceLayout{}, err
	}
	h.ReacquireEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgFreespaceAuthority(auth, n)
	}
	var target, parent *catalog.Relation
	var store *storage.Relation
	var generator *storage.SequenceGenerator
	if err == nil {
		// The released SQL wrapper's size function returns NULL for absent OIDs.
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
		if _, ok := auth.Access().Catalog.Relation(address); !ok && n.Kind.Set() {
			h.ReleaseEngineLocked()
			return storage.FreespaceLayout{}, nil
		}
		target, parent, err = amcheckTargetIdentity(auth.Access().Catalog, oid)
	}
	address := ids.ObjectAddress{}
	if err == nil {
		address = target.Address()
		if parent != nil {
			address = parent.Address()
		}
		store = h.PrivateStorageLocked(address)
		if target.Sequence != nil {
			generator = h.SequenceGeneratorLocked(target.Address())
		}
	}
	epoch := h.CatalogEpochLocked()
	h.ReleaseEngineLocked()
	if err != nil {
		return storage.FreespaceLayout{}, err
	}
	ref := h.CatalogDomain().Ref(epoch, target.Address())
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil {
		return storage.FreespaceLayout{}, err
	}
	h.ReacquireOwnerEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgFreespaceAuthority(auth, n)
	}
	valid := err == nil && auth.Access().Catalog.Epoch() == epoch && auth.Access().Catalog.Get(target.Address()) == target && h.PrivateStorageLocked(address) == store && (parent == nil || auth.Access().Catalog.Get(parent.Address()) == parent)
	if err == nil && !valid {
		err = ids.ErrCatalogReplaced
	}
	noStorage := false
	if err == nil {
		h.MarkTemporaryTargetLocked(ref)
		if kindErr := PgFreespaceTargetCheckable(auth.Access().Catalog, target); kindErr != nil {
			var diagnostic *sqlstate.Error
			if n.Kind.Set() && errors.As(kindErr, &diagnostic) && diagnostic.Code == "42809" {
				noStorage = true
			} else {
				err = kindErr
			}
		}
	}
	if err == nil && n.Kind.Block() && (block < 0 || block > 4294967294) {
		err = sqlError("22023", "invalid block number")
	}
	var view *visibility.View
	local := false
	var allocation storage.Allocation
	if err == nil && !noStorage {
		switch {
		case target.Sequence != nil:
			if generator == nil || h.SequenceGeneratorLocked(target.Address()) != generator {
				err = ids.ErrCatalogReplaced
			}
		case target.SQLIndex != nil:
			allocation = h.RelationAllocationLocked(auth.Access().Catalog, target)
			if !allocation.Allocated || allocation.Index == nil || allocation.Parent != parent || store == nil {
				err = ids.ErrCatalogReplaced
			}
		case target.RelationKind == catalog.CatalogRelation:
			// Registered catalog producer capture follows below with no mutex held.
		default:
			if store == nil {
				err = ids.ErrCatalogReplaced
			} else {
				view, local, err = h.RelationDataViewLocked(ref)
			}
		}
	}
	h.ReleaseOwnerEngineLocked()
	if local && view != nil {
		defer view.Release()
	}
	if err != nil {
		return storage.FreespaceLayout{}, err
	}
	var result storage.FreespaceLayout
	ctx := h.Operation().Context()
	switch {
	case noStorage:
	case target.Sequence != nil:
		result, err = storage.FreespacePayload(ctx, 52)
	case target.SQLIndex != nil:
		forks, failure := allocation.Forks(ctx)
		err = failure
		if err == nil {
			result.Blocks = forks.Main / 8192
			if forks.Main%8192 != 0 {
				result.Blocks++
			}
		}
	case target.RelationKind == catalog.CatalogRelation:
		payload, failure := h.PgvisibilityCatalogBytes(ref, target)
		err = failure
		if err == nil {
			result, err = storage.FreespacePayload(ctx, payload)
		}
	default:
		source, failure := exec.CaptureSource(ctx, view, ref)
		err = failure
		if err == nil {
			var payload int64
			for i := range source.Count() {
				if err = ctx.Err(); err != nil {
					break
				}
				var weight int64
				weight, err = storage.RowAllocation(ctx, source.Row(i))
				if err != nil {
					break
				}
				weight, err = storage.AddSize(weight, 4)
				if err != nil {
					break
				}
				payload, err = storage.AddSize(payload, weight)
				if err != nil {
					break
				}
			}
			source.Close()
			if err == nil {
				result, err = storage.FreespacePayload(ctx, payload)
			}
		}
	}
	if err != nil {
		return storage.FreespaceLayout{}, err
	}
	h.ReacquireEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgFreespaceAuthority(auth, n)
	}
	valid = err == nil && h.CatalogEpochLocked() == epoch && auth.Access().Catalog.Get(target.Address()) == target && h.PrivateStorageLocked(address) == store && (parent == nil || auth.Access().Catalog.Get(parent.Address()) == parent)
	if target.Sequence != nil {
		valid = valid && h.SequenceGeneratorLocked(target.Address()) == generator
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return storage.FreespaceLayout{}, err
	}
	if !valid {
		return storage.FreespaceLayout{}, ids.ErrCatalogReplaced
	}
	if err := ctx.Err(); err != nil {
		return storage.FreespaceLayout{}, err
	}
	return result, nil
}
