package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Resolved identities survive waits. The descriptor observations are refreshed
// at grant time, then fenced during execution/publication; names are not retried.
type indexMaintenanceGrant struct {
	Targets     []ids.ObjectRef
	Parents     []ids.ObjectRef
	Indexes     []ids.ObjectRef
	Descriptors map[ids.ObjectAddress]*catalog.Relation
	IndexSets   map[ids.ObjectAddress]int
	Notices     []sqlstate.Error
	Attach      ids.ObjectRef
	Partition   bool
}

func (g *indexMaintenanceGrant) Check(view access.View, env access.Environment, stmt parser.Statement, strict bool) error {
	for address, observed := range g.Descriptors {
		current, ok := view.Catalog.Relation(address)
		if !ok || strict && current != observed {
			return ids.ErrCatalogReplaced
		}
	}
	for _, ref := range g.Targets {
		if ref.Domain() != view.Owner.Domain || ref.Epoch != view.Catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		d := view.Catalog.MustRelation(ref.Address)
		switch s := stmt.(type) {
		case *parser.DropIndex:
			if d.RelationKind != catalog.IndexRelation {
				return WrongKind(d, true)
			}
			if err := RelationDropOwner(view, env, ref, d); err != nil {
				return err
			}
			if err := PartitionIndexDropDependency(view.Catalog, d); err != nil {
				return err
			}
			if d.SQLIndex.Constraint != nil {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because constraint %s on table %s requires it", d.Name(), d.SQLIndex.Constraint.Name, view.Catalog.Get(d.SQLIndex.Parent).Name()), Hint: "You can drop constraint " + d.SQLIndex.Constraint.Name + " on table " + view.Catalog.Get(d.SQLIndex.Parent).Name() + " instead."}
			}
		case *parser.AlterIndex:
			if s.Action != "rename" && d.RelationKind != catalog.IndexRelation {
				return WrongKind(d, false)
			}
			if err := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
				return err
			}
			if s.Action == "rename" {
				if err := view.CheckCreate(env, ref.Domain().Ref(ref.Epoch, d.Namespace())); err != nil {
					return err
				}
			}
		case *parser.Reindex:
			parent := d
			if s.Kind.Text == "index" {
				if d.RelationKind != catalog.IndexRelation {
					return WrongKind(d, false)
				}
				parent = view.Catalog.MustRelation(d.SQLIndex.Parent)
			} else if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation {
				return sqlError("42809", fmt.Sprintf(`"%s" is not a table or materialized view`, d.Name()))
			}
			rights, err := view.Effective(env, ref.Domain().Ref(ref.Epoch, parent.Address()), false)
			if err != nil {
				return err
			}
			if rights&authorization.MaintainPrivilege == 0 {
				return sqlError("42501", fmt.Sprintf("permission denied for %s %s", s.Kind.Text, d.Name()))
			}
			if parent.Partition != nil {
				return sqlError("0A000", "partitioned indexes are unsupported")
			}
		}
	}
	for _, ref := range g.Indexes {
		d := view.Catalog.MustRelation(ref.Address)
		if d.SQLIndex == nil || g.Descriptors[d.SQLIndex.Parent] == nil {
			return ids.ErrCatalogReplaced
		}
	}
	for parent, count := range g.IndexSets {
		actual := 0
		for address, descriptor := range view.Catalog.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || d.SQLIndex == nil || d.SQLIndex.Parent != parent {
				continue
			}
			if g.Descriptors[address] == nil {
				return ids.ErrCatalogReplaced
			}
			actual++
		}
		if actual != count {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

func WrongKind(d *catalog.Relation, drop bool) error {
	diagnostic := sqlError("42809", fmt.Sprintf(`"%s" is not an index`, d.Name())).(*sqlstate.Error)
	if drop {
		switch d.RelationKind {
		case catalog.TableRelation, catalog.PartitionedRelation:
			diagnostic.Hint = "Use DROP TABLE to remove a table."
		case catalog.SequenceRelation:
			diagnostic.Hint = "Use DROP SEQUENCE to remove a sequence."
		case catalog.ViewRelation:
			diagnostic.Hint = "Use DROP VIEW to remove a view."
		}
	}
	return diagnostic
}

func RelationDropOwner(view access.View, env access.Environment, ref ids.ObjectRef, d *catalog.Relation) error {
	err := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true})
	if err == nil {
		return nil
	}
	if _, validationErr := view.Object(ref); validationErr != nil {
		return validationErr
	}
	ns, ok := view.Catalog.Namespace(d.Namespace())
	if !ok {
		return fmt.Errorf("stuntgres: invalid sequence namespace")
	}
	namespace := ref.Domain().Ref(ref.Epoch, ns.Address())
	if view.Check(env, access.Requirement{Object: namespace, OwnerOnly: true}) == nil {
		return nil
	}
	return err
}
