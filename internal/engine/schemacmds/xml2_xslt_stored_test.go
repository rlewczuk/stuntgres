package schemacmds_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestXML2XSLTStoredControls(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, args, signature string
		slot                  int
		volatility            expr.Volatility
	}{
		{"two", "doc,style", "25,25", 11, expr.Immutable},
		{"three", "doc,style,params", "25,25,25", 10, expr.Volatile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, ext := xml2UnitInstalled(t)
			enginetest.Run(t, source, "CREATE TABLE xslt_guard(id integer,doc text,style text,params text,CONSTRAINT xslt_guard_check CHECK(xslt_process("+tc.args+") <> ''))")
			view := source.CatalogRead()
			owner := view.MustRelation(enginetest.RoleTableRef(t, source, "xslt_guard").Address)
			definition := owner.Constraints.Check[0]
			predicate := definition.Expression.(plan.ComparisonExpr)
			call := predicate.Left.(plan.InstalledXML2Expr)
			if call.Procedure != view.MustProcedure(ext.Members[tc.slot]) || call.RoutineReference != call.Procedure || call.Procedure.Signature != catalog.ProcedureSignature(tc.signature) || call.Operation != functions.XML2XSLT || call.Properties().Volatility != tc.volatility || !call.Properties().RowDependent || !analyze.ValidInstalledXML2Shape(call) || !analyze.ClosedPgstattupleCheck(predicate, view) || analyze.ClosedPgstattupleDefault(predicate, view) {
				t.Fatal("stored row-bound selected overload", call)
			}
			// Owner-bound CHECK permission must not become DEFAULT permission.
			if _, err := stored.DefaultSQL(ctx, view, call); err == nil {
				t.Fatal("row-bound XSLT rendered without its relation owner")
			}
			for i, arg := range call.Args {
				column := arg.(plan.ColumnExpr)
				if column.Slot != i+1 || column.Origin.Address.SubID != owner.Attributes[i+1] || column.Usage != owner.Columns[i+1].Usage {
					t.Fatal("ordered stored operand lost owner", i, arg)
				}
			}
			dependencies := slices.Clone(definition.Dependencies)
			backing := &call.Args[0]
			render := func(v catalog.Read, relation *catalog.Relation, expression expr.Expr, captured []catalog.ExpressionDependency) (string, error) {
				return (stored.ScalarRenderer{Owner: relation, CapturedChecks: captured}).SQL(ctx, v, expression)
			}
			original, err := render(view, owner, predicate, dependencies)
			if err != nil || !strings.Contains(original, `"public"."xslt_process"`) {
				t.Fatal("render exact overload", original, err)
			}
			bad := call
			bad.Operation = functions.XML2String
			forged := predicate
			forged.Left = bad
			if _, err := render(view, owner, forged, dependencies); err == nil {
				t.Fatal("forged selected signature rendered")
			}
			formals := "text,text"
			if tc.slot == 10 {
				formals += ",text"
			}
			for _, q := range []string{
				"ALTER FUNCTION xslt_process(" + formals + ") RENAME TO xslt_kept",
				"ALTER EXTENSION xml2 DROP FUNCTION xslt_kept(" + formals + ")",
			} {
				enginetest.Run(t, source, q)
			}
			current := source.CatalogRead()
			text, err := render(current, owner, predicate, dependencies)
			if err != nil || !strings.Contains(text, `"public"."xslt_kept"`) || strings.Contains(text, `"xslt_process"`) {
				t.Fatal("current-name detached overload", text, err)
			}
			target := engine.New()
			// Occupy every original member coordinate, including the two late
			// XSLT slots; an earlier, smaller provider cannot force their remap.
			xml2UnitRetainTemporaryCollisions(t, target, ext.Members)
			if _, err := target.ReplaceCatalog(ctx, source, current); err != nil {
				t.Fatal("genuine replacement", err)
			}
			destination := target.CatalogRead()
			imported := destination.MustRelation(destination.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "xslt_guard"}))
			check := imported.Constraints.Check[0]
			mapped := check.Expression.(plan.ComparisonExpr).Left.(plan.InstalledXML2Expr)
			if mapped.Ref.Domain() != destination.Domain() || mapped.Ref.Epoch != destination.Epoch() || mapped.Ref.Address == call.Ref.Address || mapped.Procedure == call.Procedure || mapped.RoutineReference != mapped.Procedure || mapped.Procedure.Signature != catalog.ProcedureSignature(tc.signature) || mapped.Operation != functions.XML2XSLT || len(mapped.Args) != len(call.Args) || mapped.IntrinsicVolatility() != tc.volatility || !analyze.ValidInstalledXML2Shape(mapped) || stored.ValidateInstalledXML2(mapped, destination) != nil {
				t.Fatal("overload identity/type/volatility remap", mapped)
			}
			for _, dependency := range check.Dependencies {
				if dependency.Ref.Domain() != destination.Domain() || dependency.Ref.Epoch != destination.Epoch() {
					t.Fatal("source dependency escaped replacement")
				}
			}
			text, err = render(destination, imported, check.Expression, check.Dependencies)
			if err != nil || !strings.Contains(text, `"public"."xslt_kept"`) {
				t.Fatal("destination current-name rendering", text, err)
			}
			if _, err := render(destination, imported, predicate, dependencies); err == nil {
				t.Fatal("destination accepted source authority")
			}
			if view.Get(call.Ref.Address) != call.Procedure || &call.Args[0] != backing || !slices.Equal(definition.Dependencies, dependencies) {
				t.Fatal("reconstruction mutated original immutable definition")
			}
			if text, err := render(view, owner, predicate, dependencies); err != nil || text != original {
				t.Fatal("original retained definition changed", text, err)
			}
		})
	}
}
