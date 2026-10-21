package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DropTargets resolves the tables of DROP TABLE s, in order and without
// duplicates: a missing table or schema is a NOTICE under IF EXISTS, a
// relation of another kind is 42809 with the right DROP hint, and the
// table's or its schema's owner may drop it.
func DropTargets(h table.Resolver, auth ddl.Authority, s *parser.DropTable) ([]ids.ObjectRef, []sqlstate.Error, error) {
	view := h.AccessView(auth)
	refs := make([]ids.ObjectRef, 0, len(s.Names))
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	var notices []sqlstate.Error
	for _, name := range s.Names {
		if len(name.Parts) < 1 || len(name.Parts) > 2 {
			return nil, nil, fmt.Errorf("stuntgres: invalid relation name")
		}
		kind, missing := "table", name.Parts[len(name.Parts)-1].Text
		if len(name.Parts) == 2 {
			_, exists := view.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
			if name.Parts[0].Text == "pg_temp" {
				exists = auth.TemporaryNamespace() != (ids.ObjectAddress{})
			}
			if !exists {
				kind, missing = "schema", name.Parts[0].Text
			}
		}
		var ref ids.ObjectRef
		var err error
		if kind == "schema" {
			err = sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, missing))
		} else {
			ref, err = h.ResolveReference(auth, name)
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42P01" {
				err = sqlError("42P01", fmt.Sprintf(`table "%s" does not exist`, missing))
			}
		}
		if err != nil {
			d, ok := err.(*sqlstate.Error)
			if !s.IfExists || !ok || (d.Code != "42P01" && d.Code != "3F000") {
				return nil, nil, err
			}
			notices = append(notices, sqlstate.Error{
				Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000",
				Message: fmt.Sprintf(`%s "%s" does not exist, skipping`, kind, missing),
			})
			continue
		}
		relation := view.Catalog.MustRelation(ref.Address)
		if relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation {
			if relation.RelationKind == catalog.IndexRelation {
				return nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a table`, relation.Name()), Hint: "Use DROP INDEX to remove an index."}
			}
			hint := "Use DROP SEQUENCE to remove a sequence."
			if relation.RelationKind == catalog.ViewRelation {
				hint = "Use DROP VIEW to remove a view."
			}
			if relation.RelationKind == catalog.CompositeRelation {
				hint = "Use DROP TYPE to remove a type."
			}
			if relation.RelationKind == catalog.MaterializedViewRelation {
				hint = "Use DROP MATERIALIZED VIEW to remove a materialized view."
			}
			return nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a table`, relation.Name()), Hint: hint}
		}
		if seen[ref.Address] {
			continue
		}
		tableErr := view.Check(auth, access.Requirement{Object: ref, OwnerOnly: true})
		if tableErr != nil {
			descriptor := view.Catalog.Get(ref.Address)
			nsref := view.Owner.Domain.Ref(ref.Epoch, descriptor.Namespace())
			if err := view.Check(auth, access.Requirement{Object: nsref, OwnerOnly: true}); err != nil {
				return nil, nil, tableErr
			}
		}
		seen[ref.Address] = true
		refs = append(refs, ref)
	}
	return refs, notices, nil
}
