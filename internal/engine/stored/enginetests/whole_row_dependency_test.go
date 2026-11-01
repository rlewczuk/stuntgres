package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestWholeRowSQLDependencySets(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE whole_dep_owner(a integer,b integer)")
	view := e.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "whole_dep_owner"}))
	rowType := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)}
	attribute := func(slot int) ids.ObjectAddress {
		address := relation.Address()
		address.SubID = relation.Attributes[slot]
		return address
	}
	for _, tc := range []struct {
		name, projection string
		want             []ids.ObjectAddress
		whole            bool
	}{
		{"whole", "whole_dep_owner", []ids.ObjectAddress{relation.Address(), rowType}, true},
		{"whole_alias", "q", []ids.ObjectAddress{relation.Address(), rowType}, true},
		{"explicit_row", "ROW(a,b)", []ids.ObjectAddress{attribute(0), attribute(1)}, false},
		{"expanded_row", "ROW(whole_dep_owner.*)", []ids.ObjectAddress{attribute(0), attribute(1)}, false},
		{"star", "*", []ids.ObjectAddress{attribute(0), attribute(1)}, false},
		{"selected_field", "(whole_dep_owner).a", []ids.ObjectAddress{attribute(0)}, false},
		{"selected_second_field", "(whole_dep_owner).b", []ids.ObjectAddress{attribute(1)}, false},
		{"selected_alias_field", "(q).b", []ids.ObjectAddress{attribute(1)}, false},
		{"explicit_row_field", "(ROW(a,b)).f1", []ids.ObjectAddress{attribute(0), attribute(1)}, false},
		{"nested_whole", "ROW(whole_dep_owner)", []ids.ObjectAddress{relation.Address(), rowType}, false},
		{"whole_and_column", "whole_dep_owner,a", []ids.ObjectAddress{rowType, attribute(0)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from := "whole_dep_owner"
			if tc.name == "whole_alias" || tc.name == "selected_alias_field" {
				from += " q"
			}
			binding := selectedDependencyBinding(t, e, s, "SELECT "+tc.projection+" FROM "+from)
			project := binding.Query.Root.(*plan.ProjectNode)
			if tc.whole {
				row, ok := project.Expressions[0].(plan.RowExpr)
				if !ok || row.WholeName == "" || len(row.Fields) != 2 {
					t.Fatal("missing actual whole-row projection", project.Expressions[0])
				}
			}
			manifest := append([]catalog.ExpressionDependency(nil), binding.Dependencies...)
			got, err := stored.QueryDependRefs(t.Context(), view, nil, binding.Query)
			selectedDependencySet(t, got, err, tc.want...)
			if !reflect.DeepEqual(manifest, binding.Dependencies) {
				t.Fatal("SQL edge projection mutated executable manifest")
			}
			if tc.name == "whole" {
				got, err = stored.ExpressionDependRefs(t.Context(), view, project.Expressions[0])
				selectedDependencySet(t, got, err, relation.Address(), rowType)
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if refs, err := stored.ExpressionDependRefs(ctx, view, project.Expressions[0]); refs != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("canceled whole-row dependency prefix", refs, err)
				}
			}
		})
	}
	// Empty whole rows still have relation/type identity, without relying on
	// a first synthesized field from which to guess their owning relation.
	enginetest.SessionSQL(t, s, "CREATE TABLE whole_dep_empty()")
	view = e.CatalogRead()
	empty := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "whole_dep_empty"}))
	binding := selectedDependencyBinding(t, e, s, "SELECT whole_dep_empty FROM whole_dep_empty")
	got, err := stored.QueryDependRefs(t.Context(), view, nil, binding.Query)
	selectedDependencySet(t, got, err, empty.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(empty.RowType)})
}
