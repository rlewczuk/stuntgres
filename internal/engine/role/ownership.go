package role

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Ownership changes retain immutable type, row, index and sequence generator
// roots. Only descriptor/ACL payloads change. All reciprocal members are staged
// before the ordinary publication validator sees any of them.
func StageOwnership(stage OwnerStage, d catalog.Descriptor, target authorization.PrincipalRef) error {
	if next := stage.Get(d.Address()); next != nil {
		d = next
	}
	owner, _, _ := access.DescriptorACL(d)
	if owner == target {
		return nil
	}
	revision, err := stage.CascadeRevision(d.Revision())
	if err != nil {
		return err
	}
	switch old := d.(type) {
	case *catalog.Database:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.ForeignObject:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.Extension:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Procedure:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.TSDictionary:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Namespace:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.Collation:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		return stage.Replace(&next)
	case *catalog.Conversion:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.OperatorFamily:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.OperatorClass:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Operator:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Type:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.Array != 0 {
			array, ok := stage.BaseCatalog().Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
			if !ok || !array.SQLType.IsArray() || array.SQLType.Element().ID() != old.SQLType.ID() {
				return catalog.Conflict{}
			}
			if err = StageOwnership(stage, array, target); err != nil {
				return err
			}
		}
		if old.OwningRelation != (ids.ObjectAddress{}) && !old.SQLType.IsArray() {
			r, ok := stage.BaseCatalog().Relation(old.OwningRelation)
			if !ok || r.RowType != old.SQLType.ID() {
				return catalog.Conflict{}
			}
			return StageOwnership(stage, r, target)
		}
	case *catalog.Relation:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.Security.ACL = authorization.SchemaACLOwner(old.Security.ACL, old.Owner, target)
		if old.Sequence != nil {
			next.SequencePlanStamp = revision
		}
		if len(old.Security.ColumnACL) != 0 {
			next.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(old.Security.ColumnACL))
			for sub, acl := range old.Security.ColumnACL {
				next.Security.ColumnACL[sub] = authorization.SchemaACLOwner(acl, old.Owner, target)
			}
		}
		// Replace synchronizes the row type and its array against this owner.
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.RelationKind == catalog.TableRelation || old.RelationKind == catalog.PartitionedRelation || old.RelationKind == catalog.MaterializedViewRelation {
			for _, associated := range stage.BaseCatalog().Descriptors() {
				r, ok := associated.(*catalog.Relation)
				if !ok {
					continue
				}
				linked := r.SQLIndex != nil && r.SQLIndex.Parent == old.Address()
				if r.Sequence != nil {
					parent := r.Sequence.Owned
					parent.SubID = 0
					linked = linked || parent == old.Address()
				}
				if linked {
					if err = StageOwnership(stage, r, target); err != nil {
						return err
					}
				}
			}
		}
	default:
		return fmt.Errorf("stuntgres: object has no ownership handler")
	}
	return nil
}

// OwnerChange is a decided ALTER ... OWNER: Object gets Target as its new
// owner. A nil Object means the owner is unchanged.
type OwnerChange struct {
	Object catalog.Descriptor
	Target authorization.PrincipalRef
}

// PlanTypeOwnership decides ALTER TYPE or ALTER DOMAIN ... OWNER TO name.
func PlanTypeOwnership(h Host, auth ddl.Authority, s *parser.AlterOwner, name string, identity bool) (OwnerChange, error) {
	view := h.AccessView(auth)
	path, err := h.SearchNamespaces(auth, s.Name, ids.TypeClassID)
	if err != nil {
		return OwnerChange{}, err
	}
	var old *catalog.Type
	for _, namespace := range path {
		a := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
		if a != (ids.ObjectAddress{}) {
			old = view.Catalog.MustType(a)
			break
		}
	}
	if old == nil {
		return OwnerChange{}, sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(s.Name)))
	}
	diagnosticPath, _ := auth.ReferencePath()
	display := catalog.TypeDiagnosticNameAt(view.Catalog, old, diagnosticPath)
	if s.Kind == "domain" && !old.SQLType.IsDomain() {
		return OwnerChange{}, sqlError("42809", display+" is not a domain")
	}
	if old.SQLType.IsArray() {
		element := view.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.SQLType.Element().ID())})
		elementName := catalog.TypeDiagnosticNameAt(view.Catalog, element, diagnosticPath)
		return OwnerChange{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot alter array type " + display, Hint: "You can alter type " + elementName + ", which will alter the array type as well."}
	}
	if old.OwningRelation != (ids.ObjectAddress{}) {
		r := view.Catalog.MustRelation(old.OwningRelation)
		if r.RelationKind != catalog.CompositeRelation {
			return OwnerChange{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: display + " is a table's row type", Hint: "Use ALTER TABLE instead."}
		}
	}
	target, err := access.SchemaOwner(view, name, identity)
	if err != nil {
		return OwnerChange{}, err
	}
	if !old.SQLType.IsUserDefined() {
		return OwnerChange{}, sqlError("0A000", "ownership of builtin types is unsupported")
	}
	if target == old.Owner {
		return OwnerChange{}, nil
	}
	if err = view.Check(auth, access.Requirement{Object: view.Ref(old.Address()), OwnerOnly: true, OwnerNoun: "type"}); err != nil {
		return OwnerChange{}, err
	}
	if err = access.OwnershipEligibility(view, auth, target, old.Namespace()); err != nil {
		return OwnerChange{}, err
	}
	return OwnerChange{Object: old, Target: target}, nil
}

// PlanDatabaseOwnership decides ALTER DATABASE ... OWNER TO name.
func PlanDatabaseOwnership(view access.View, env access.Environment, s *parser.AlterOwner, name string, identity bool) (OwnerChange, error) {
	target, err := access.SchemaOwner(view, name, identity)
	if err != nil {
		return OwnerChange{}, err
	}
	address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
	database, ok := view.Catalog.Database(address)
	if len(s.Name.Parts) != 1 || !ok {
		return OwnerChange{}, sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, s.Name.Parts[len(s.Name.Parts)-1].Text))
	}
	if target == database.Owner {
		return OwnerChange{}, nil
	}
	if err = view.Check(env, access.Requirement{Object: view.Ref(database.Address()), OwnerOnly: true}); err != nil {
		return OwnerChange{}, err
	}
	if err = access.OwnershipSuccessor(view, target); err != nil {
		return OwnerChange{}, err
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return OwnerChange{}, err
	}
	if !actor.Superuser && !actor.CreateDB {
		return OwnerChange{}, sqlError("42501", "permission denied to change owner of database")
	}
	return OwnerChange{Object: database, Target: target}, nil
}

// PlanRelationOwnership decides ALTER TABLE, SEQUENCE, VIEW, MATERIALIZED
// VIEW or FOREIGN TABLE ... OWNER TO name for old, resolved as ref. The
// engine checks materialized view authority before it.
func PlanRelationOwnership(view access.View, env access.Environment, old *catalog.Relation, ref ids.ObjectRef, kind, name string, identity bool) (OwnerChange, error) {
	if kind == "view" && old.RelationKind != catalog.ViewRelation {
		return OwnerChange{}, sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, old.Name(), kind))
	}
	if old.RelationKind == catalog.CatalogRelation || old.RelationKind == catalog.IndexRelation || old.RelationKind == catalog.CompositeRelation {
		return OwnerChange{}, analyze.TableKindError(old, "change")
	}
	if kind != "materialized view" {
		if err := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return OwnerChange{}, err
		}
	}
	target, err := access.SchemaOwner(view, name, identity)
	if err != nil {
		return OwnerChange{}, err
	}
	if target == old.Owner {
		return OwnerChange{}, nil
	}
	if old.Sequence != nil && old.Sequence.Owned != (ids.ObjectAddress{}) {
		parent := old.Sequence.Owned
		parent.SubID = 0
		return OwnerChange{}, sequence.LinkedError(old, view.Catalog.MustRelation(parent), false)
	}
	if err = access.OwnershipEligibility(view, env, target, old.Namespace()); err != nil {
		return OwnerChange{}, err
	}
	return OwnerChange{Object: old, Target: target}, nil
}
