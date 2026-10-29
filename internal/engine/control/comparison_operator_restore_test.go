package control_test

import (
	"context"
	"encoding/base64"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Exercise the real restore state machine with declarations from genuine CREATE
// EXTENSION, not hand-built provider recipes or a seeded bound-window state.
func TestComparisonRestoreCurrentOperatorAuthority(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE SCHEMA eo_ops", "CREATE SCHEMA eo_callee", "CREATE EXTENSION hstore",
		"ALTER OPERATOR public.=(hstore,hstore) SET SCHEMA eo_ops",
		"ALTER FUNCTION public.hstore_ne(hstore,hstore) SET SCHEMA eo_callee",
	} {
		enginetest.Run(t, e, q)
	}
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	op, _ := view.Operator(ext.Installation.Members[extension.HstoreComparisonOperatorStart])
	proc := view.MustProcedure(ext.Installation.Members[extension.HstoreComparisonProcedureStart])
	admit := func(current catalog.Read, namespace string) error {
		t.Helper()
		encoded := base64.StdEncoding.EncodeToString([]byte(`{"namespace":"` + namespace + `"}`))
		source := "-- " + control.ComparisonContextBegin + "\nSET search_path = pg_catalog;\n-- stuntgres:comparison-binding " + encoded + "\nSET search_path = pg_catalog, " + namespace + ";\n"
		var c control.ComparisonRestore
		statements, err := parser.ParseWithOptions(source, parser.Options{Context: context.Background(), StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
			_, err := c.Decode(comment, position)
			return err
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Pair(source, statements); err != nil {
			t.Fatal(err)
		}
		for i, statement := range statements {
			if _, err := c.Before(i, statement, current); err != nil {
				return err
			}
		}
		return nil
	}
	if err := admit(view, "eo_ops"); err != nil {
		t.Fatal("operator-only namespace rejected", err)
	}
	if err := admit(view, "eo_callee"); err == nil {
		t.Fatal("unrelated comparison procedure substituted for a current operator")
	}
	for _, corruption := range []string{"missing_operator", "copied_origin", "wrong_backer", "wrong_result", "operator_principal", "callee_principal", "callee_declaration"} {
		t.Run(corruption, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			badOperator, badProcedure := *op, *proc
			switch corruption {
			case "missing_operator":
				state.RemoveDescriptor(op.Address())
			case "copied_origin":
				installation := *op.Origin.Installation
				origin := *op.Origin
				origin.Installation = &installation
				badOperator.Origin = &origin
			case "wrong_backer":
				badOperator.Function = ext.Installation.Members[extension.HstoreComparisonProcedureStart+1].ObjectID
			case "wrong_result":
				badOperator.Result = 23
			case "operator_principal":
				badOperator.Owner = engine.New().CatalogRead().Owner().Principals.Principal(op.Owner.ID())
			case "callee_principal":
				badProcedure.Owner = engine.New().CatalogRead().Owner().Principals.Principal(proc.Owner.ID())
			case "callee_declaration":
				badProcedure.Display.Source = "not_the_released_declaration"
			}
			if corruption != "missing_operator" {
				state.SetDescriptor(op.Address(), &badOperator)
			}
			state.SetDescriptor(proc.Address(), &badProcedure)
			if err := admit(view.WithState(state), "eo_ops"); err == nil {
				t.Fatal("corrupt current operator/backer authenticated restore")
			}
		})
	}
}
