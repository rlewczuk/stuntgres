package contrib

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqlstate"
)

func dblinkRoutineAuthority[A Authority](auth A, procedure *catalog.Procedure, ref ids.ObjectRef) error {
	view := auth.Access()
	if procedure == nil || ref.Domain() != view.Catalog.Owner().Domain || ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidateDblinkIdentity(view.Catalog, current, ref); err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
	// The retained expression initializer owns execute MAC. These current
	// declaration/ACL fences also run inside the entered body and after waits.
	return auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true})
}

// ValidateDblinkConnection keeps connection calls on the same current installed
// declaration/caller gateway as the constructors, including post-Open admission.
func ValidateDblinkConnection[A Authority](auth A, n plan.InstalledDblinkConnectionExpr) error {
	if err := dblinkRoutineAuthority(auth, n.Procedure, n.Ref); err != nil {
		return err
	}
	current, _ := auth.Access().Catalog.Procedure(n.Ref.Address)
	call := n.DblinkConnection
	call.RoutineReference = current
	return extension.ValidateDblinkConnectionCall(auth.Access().Catalog, current, call)
}

func dblinkRelation[A Authority](h Host[A], procedure *catalog.Procedure, routine ids.ObjectRef, text string) (*catalog.Relation, string, bool, func(), error) {
	if err := h.Active(); err != nil {
		return nil, "", false, nil, err
	}
	h.ReacquireOwnerEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil {
		err = dblinkRoutineAuthority(auth, procedure, routine)
	}
	var path []ids.ObjectAddress
	if err == nil {
		path, err = auth.ReferencePath()
	}
	h.ReleaseOwnerEngineLocked()
	if err != nil {
		return nil, "", false, nil, err
	}
	oid, err := h.ClassInput(auth, path).ResolveRelationOpenName(h.Operation().Context(), text)
	if err != nil {
		return nil, "", false, nil, err
	}
	epoch := auth.Access().Catalog.Epoch()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	target, ok := auth.Access().Catalog.Relation(address)
	if !ok {
		return nil, "", false, nil, ids.ErrCatalogReplaced
	}
	ref := h.CatalogDomain().Ref(epoch, address)
	var contribution lock.RelationContribution
	release := func() { h.LockManager().ReleaseRelation(&contribution) }
	admitted := false
	defer func() {
		if !admitted {
			release()
		}
	}()
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil {
		return nil, "", false, nil, err
	}

	// A lock wait can cross catalog or ACL publication. Re-admit the exact
	// incarnation and current routine/table authority before using any metadata.
	h.ReacquireOwnerEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil && (auth.Access().Catalog.Epoch() != epoch || auth.Access().Catalog.Get(address) != target) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = dblinkRoutineAuthority(auth, procedure, routine)
	}
	if err == nil {
		// table_openrv rejects these kinds before checking relation SELECT.
		// Do not import the statistics functions' broader kind restrictions.
		detail := ""
		switch target.RelationKind {
		case catalog.IndexRelation:
			detail = "This operation is not supported for indexes."
			if target.SQLIndex != nil {
				if parent, ok := auth.Access().Catalog.Relation(target.SQLIndex.Parent); ok && parent.Partition != nil {
					detail = "This operation is not supported for partitioned indexes."
				}
			}
		case catalog.CompositeRelation:
			detail = "This operation is not supported for composite types."
		}
		if detail != "" {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, target.Name()), Detail: detail}
		}
	}
	if err == nil {
		// Released get_rel_from_relname calls pg_class_aclcheck, not the
		// executor DML hook. Namespace-search MAC was checked during lookup;
		// opening metadata does not invent table/column SELECT MAC.
		acl := auth.Access()
		acl.Sepgsql = access.SepgsqlAuthority{}
		err = acl.Check(auth, access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege})
	}
	var schema string
	qualified := false
	if err == nil {
		path, err = auth.ReferencePath()
		if err == nil {
			qualified = !regobj.NamedObjectVisibleAt(auth.Access().Catalog, nil, path, target)
			if qualified {
				namespace, exists := auth.Access().Catalog.Namespace(target.Namespace())
				if !exists {
					err = ids.ErrCatalogReplaced
				} else {
					schema = namespace.Name()
				}
			}
		}
	}
	if err == nil {
		h.MarkTemporaryTargetLocked(ref)
	}
	h.ReleaseOwnerEngineLocked()
	if err != nil {
		return nil, "", false, nil, err
	}
	admitted = true
	return target, schema, qualified, release, nil
}
