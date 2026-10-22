package dml

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// CheckTruncate checks one TRUNCATE target: the system catalog guard, the
// foreign wrapper's truncate support, the relation kind, the TRUNCATE
// privilege when asked and ONLY on a partitioned table, in that order.
func CheckTruncate(a access.View, env access.Environment, ref ids.ObjectRef, d *catalog.Relation, privilege, only bool) error {
	if analyze.GuardedCatalogRelation(d) {
		return GuardedCatalogMutationError(a, d, true)
	}
	if d.RelationKind == catalog.ForeignRelation {
		server, ok := a.Catalog.Foreign(d.Foreign.Server)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		wrapper, ok := a.Catalog.Foreign(server.Wrapper)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		handler, err := fdw.HandlerFor(a.Catalog, wrapper)
		if err != nil {
			return err
		}
		if _, ok := handler.Wrapper.(catalog.ForeignTruncater); !ok {
			return sqlError("0A000", fmt.Sprintf("cannot truncate foreign table %q", d.Name()))
		}
	}
	if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.ForeignRelation {
		return sqlError("42809", fmt.Sprintf("%q is not a table", d.Name()))
	}
	if privilege {
		if err := a.Check(env, access.Requirement{Object: ref, Privileges: authorization.TruncatePrivilege}); err != nil {
			return err
		}
	}
	if only && d.Partition != nil {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot truncate only a partitioned table", Hint: "Do not specify the ONLY keyword, or use TRUNCATE ONLY on the partitions directly."}
	}
	return nil
}

// GuardedCatalogMutationError is the error of a write to a guarded system
// catalog relation. Binding and frame import have completed before mutation
// admission. Recheck the coherent effective actor at execution, including
// held SET ROLE commands. System-catalog TRUNCATE has its own released
// English diagnostic.
func GuardedCatalogMutationError(a access.View, d *catalog.Relation, truncate bool) error {
	actor, err := a.Roles.Descriptor(a.State.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return sqlError("0A000", "catalog source mutation is unsupported")
	}
	if truncate {
		return sqlError("42501", fmt.Sprintf("permission denied: %q is a system catalog", d.Name()))
	}
	return sqlError("42501", "permission denied for table "+d.Name())
}
