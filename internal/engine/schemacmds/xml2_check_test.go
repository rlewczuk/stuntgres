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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestXML2CheckOwnership(t *testing.T) {
	ctx := context.Background()
	source, ext := xml2UnitInstalled(t)
	enginetest.Run(t, source, "CREATE TABLE xml2_guard(id integer,doc text,path text,CONSTRAINT xml2_guard_xpath CHECK(xpath_string(doc,path) <> ''))")
	view := source.CatalogRead()
	owner := view.MustRelation(enginetest.RoleTableRef(t, source, "xml2_guard").Address)
	if len(owner.Constraints.Check) != 1 || owner.Constraints.Check[0].Name != "xml2_guard_xpath" {
		t.Fatal("missing genuine named CHECK", owner.Constraints.Check)
	}
	definition := owner.Constraints.Check[0]
	predicate, ok := definition.Expression.(plan.ComparisonExpr)
	if !ok || predicate.Usage.Type.ID() != sqltype.Bool || predicate.Operator != "<>" {
		t.Fatal("CHECK lost ordinary Boolean comparison", definition.Expression)
	}
	call, ok := predicate.Left.(plan.InstalledXML2Expr)
	if !ok || call.Procedure != view.MustProcedure(ext.Members[1]) || call.RoutineReference != call.Procedure || call.Ref.Address != ext.Members[1] || call.Procedure.Origin.Installation != ext.Installation || call.Procedure.Origin.Declaration != 1 || !analyze.ValidInstalledXML2Shape(call) {
		t.Fatal("CHECK lost authentic installed String recipe", predicate.Left)
	}
	if !call.Properties().RowDependent || call.Properties().ParameterDependent || call.Properties().StageDependent || !predicate.Properties().RowDependent || predicate.Properties().ParameterDependent || predicate.Properties().StageDependent {
		t.Fatal("CHECK did not retain real owner-row dependence", call.Properties(), predicate.Properties())
	}
	if !analyze.ClosedPgstattupleCheck(definition.Expression, view) || analyze.ClosedPgstattupleDefault(definition.Expression, view) {
		t.Fatal("row-bound CHECK/default closure boundary")
	}
	want := []ids.ObjectRef{call.Ref}
	for i, arg := range call.Args {
		column, ok := arg.(plan.ColumnExpr)
		ref := view.Domain().Ref(view.Epoch(), owner.Address())
		ref.Address.SubID = owner.Attributes[i+1]
		if !ok || column.Origin != ref || column.Slot != i+1 || column.Usage != owner.Columns[i+1].Usage || column.Usage.Type.ID() != sqltype.Text || owner.Columns[i+1].Name != []string{"doc", "path"}[i] {
			t.Fatal("CHECK formal lost ordered owner attribute", i, arg)
		}
		want = append(want, ref)
	}
	for _, typ := range []sqltype.ID{sqltype.Text, sqltype.Bool} {
		want = append(want, view.Domain().Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ)}))
	}
	captured := make(map[ids.ObjectRef]catalog.ExpressionDependency)
	for _, dependency := range definition.Dependencies {
		root := dependency.Ref.Address
		root.SubID = 0
		descriptor := view.Get(root)
		if dependency.Ref.Domain() != view.Domain() || dependency.Ref.Epoch != view.Epoch() || dependency.Purpose != catalog.DefinitionDependency || descriptor == nil || dependency.Revision != descriptor.Revision() {
			t.Fatal("CHECK captured foreign/stale dependency authority", dependency)
		}
		captured[dependency.Ref] = dependency
	}
	for _, ref := range want {
		if _, ok := captured[ref]; !ok {
			t.Fatal("CHECK omitted routine/type/attribute capture", ref, definition.Dependencies)
		}
	}
	originalDependencies := slices.Clone(definition.Dependencies)
	originalArgs := &call.Args[0]
	var renderedRefs []ids.ObjectRef
	text, err := (stored.ScalarRenderer{Owner: owner, CapturedChecks: definition.Dependencies, Dependencies: &renderedRefs}).SQL(ctx, view, definition.Expression)
	if err != nil || !strings.Contains(text, `"public"."xpath_string"`) || !strings.Contains(text, `"doc"`) || !strings.Contains(text, `"path"`) {
		t.Fatal("complete owner-bound CHECK rendering", text, err)
	}
	originalSQL := text
	for _, ref := range want[:3] {
		if !slices.Contains(renderedRefs, ref) {
			t.Fatal("complete CHECK renderer lost captured executable/attribute identity", ref, renderedRefs)
		}
	}
	render := func(v catalog.Read, relation *catalog.Relation, node expr.Expr, dependencies []catalog.ExpressionDependency) (string, error) {
		return (stored.ScalarRenderer{Owner: relation, CapturedChecks: dependencies}).SQL(ctx, v, node)
	}
	foreign := engine.New()
	foreignText, _ := foreign.CatalogRead().LookupType(sqltype.Text)
	for _, tc := range []struct {
		name   string
		mutate func(*plan.InstalledXML2Expr)
	}{
		{"missing_carrier", func(n *plan.InstalledXML2Expr) { n.RoutineReference = nil }},
		{"mismatched_carrier", func(n *plan.InstalledXML2Expr) { n.RoutineReference = view.MustProcedure(ext.Members[3]) }},
		{"selector", func(n *plan.InstalledXML2Expr) { n.Name = "xpath_nodeset" }},
		{"slot", func(n *plan.InstalledXML2Expr) {
			p := *n.Procedure
			origin := *p.Origin
			origin.Declaration = 3
			p.Origin = &origin
			n.Procedure, n.RoutineReference = &p, &p
		}},
		{"origin", func(n *plan.InstalledXML2Expr) {
			p := *n.Procedure
			origin := *p.Origin
			origin.Installation = nil
			p.Origin = &origin
			n.Procedure, n.RoutineReference = &p, &p
		}},
		{"foreign_ref", func(n *plan.InstalledXML2Expr) { n.Ref = new(ids.Domain).Ref(n.Ref.Epoch, n.Ref.Address) }},
		{"zero_epoch", func(n *plan.InstalledXML2Expr) { n.Ref.Epoch = 0 }},
		{"future_epoch", func(n *plan.InstalledXML2Expr) { n.Ref.Epoch++ }},
		{"foreign_result_generation", func(n *plan.InstalledXML2Expr) { n.Usage = foreignText.Usage() }},
		{"foreign_operand_generation", func(n *plan.InstalledXML2Expr) {
			n.Args = slices.Clone(n.Args)
			column := n.Args[0].(plan.ColumnExpr)
			column.Usage = foreignText.Usage()
			n.Args[0] = column
		}},
		{"wrong_owner_attribute", func(n *plan.InstalledXML2Expr) {
			n.Args = slices.Clone(n.Args)
			column := n.Args[0].(plan.ColumnExpr)
			column.Origin.Address.SubID = owner.Attributes[0]
			n.Args[0] = column
		}},
		{"stage_child", func(n *plan.InstalledXML2Expr) {
			n.Args = slices.Clone(n.Args)
			column := n.Args[0].(plan.ColumnExpr)
			column.Property.StageDependent = true
			n.Args[0] = column
			n.Property.StageDependent = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := call
			tc.mutate(&bad)
			candidate := predicate
			candidate.Left = bad
			if _, err := render(view, owner, candidate, definition.Dependencies); err == nil {
				t.Fatal("invalid owner-bound CHECK recipe rendered", tc.name)
			}
			switch tc.name {
			case "missing_carrier", "mismatched_carrier", "selector", "slot", "origin", "zero_epoch", "stage_child":
				if analyze.ClosedPgstattupleCheck(candidate, view) {
					t.Fatal("CHECK closure admitted invalid installed recipe", tc.name)
				}
			}
		})
	}
	binder := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	parameterized, err := binder.BindFunctionProvider(enginetest.Statement(t, "SELECT xpath_string($1::text,$2::text)").(*parser.Select).Targets[0].(parser.FunctionExpr))
	if err != nil || !parameterized.Properties().ParameterDependent || analyze.ClosedPgstattupleCheck(parameterized, view) {
		t.Fatal("CHECK closure admitted genuine typed parameter operands", parameterized, err)
	}
	if _, err := render(view, owner, parameterized, definition.Dependencies); err == nil {
		t.Fatal("CHECK renderer admitted genuine parameter operands")
	}
	for _, reason := range []string{"missing", "stale_revision", "foreign_epoch", "operand_purpose"} {
		t.Run(reason+"_manifest", func(t *testing.T) {
			dependencies := slices.Clone(definition.Dependencies)
			for i := range dependencies {
				if dependencies[i].Ref != call.Ref {
					continue
				}
				switch reason {
				case "missing":
					dependencies = slices.Delete(dependencies, i, i+1)
				case "stale_revision":
					dependencies[i].Revision++
				case "foreign_epoch":
					dependencies[i].Ref.Epoch++
				case "operand_purpose":
					dependencies[i].Purpose = catalog.OperandDependency
				}
				break
			}
			if _, err := render(view, owner, definition.Expression, dependencies); err == nil {
				t.Fatal("invalid CHECK routine manifest rendered", reason)
			}
		})
	}
	if source.CatalogRead().State() != view.State() || !slices.Equal(definition.Dependencies, originalDependencies) || &call.Args[0] != originalArgs {
		t.Fatal("rejected captures published or mutated the accepted CHECK")
	}
	for _, q := range []string{"ALTER FUNCTION xpath_string(text,text) RENAME TO xml2_check_text", "ALTER EXTENSION xml2 DROP FUNCTION xml2_check_text(text,text)"} {
		enginetest.Run(t, source, q)
	}
	current := source.CatalogRead()
	text, err = render(current, owner, definition.Expression, definition.Dependencies)
	if err != nil || !strings.Contains(text, `"public"."xml2_check_text"`) || strings.Contains(text, `"xpath_string"`) {
		t.Fatal("retained CHECK failed current-name/detached rendering", text, err)
	}
	// Occupy source allocator coordinates with a genuine retained TEMP provider,
	// forcing the imported CHECK to remap callable/owner authority, not just SQL.
	target := engine.New()
	temporaryOwner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	t.Cleanup(closeOwner)
	namespace := intarrayUnitTemporaryNamespace(t, target, temporaryOwner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, citextUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(ctx, source, current); err != nil {
		t.Fatal("genuine CHECK destination replacement", err)
	}
	destination := target.CatalogRead()
	imported := destination.MustRelation(destination.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "xml2_guard"}))
	check := imported.Constraints.Check[0]
	mapped := check.Expression.(plan.ComparisonExpr).Left.(plan.InstalledXML2Expr)
	result, _ := destination.LookupType(sqltype.Text)
	if mapped.Ref.Domain() != destination.Domain() || mapped.Ref.Epoch != destination.Epoch() || mapped.Ref.Address == call.Ref.Address || mapped.Procedure == call.Procedure || mapped.RoutineReference != mapped.Procedure || mapped.Usage != result.Usage() || !analyze.ClosedPgstattupleCheck(check.Expression, destination) || analyze.ClosedPgstattupleDefault(check.Expression, destination) {
		t.Fatal("CHECK replacement lost genuine callable/type/closure authority", mapped)
	}
	for _, dependency := range check.Dependencies {
		if dependency.Ref.Domain() != destination.Domain() || dependency.Ref.Epoch != destination.Epoch() {
			t.Fatal("CHECK manifest leaked source lifetime", dependency)
		}
	}
	text, err = render(destination, imported, check.Expression, check.Dependencies)
	if err != nil || !strings.Contains(text, `"public"."xml2_check_text"`) || strings.Contains(text, `"xpath_string"`) {
		t.Fatal("remapped CHECK failed current-name rendering", text, err)
	}
	if _, err := render(destination, imported, definition.Expression, definition.Dependencies); err == nil {
		t.Fatal("destination accepted source CHECK lifetime")
	}
	if view.Get(owner.Address()) != owner || view.Get(call.Ref.Address) != call.Procedure || !slices.Equal(definition.Dependencies, originalDependencies) || &call.Args[0] != originalArgs {
		t.Fatal("current-name/remap mutated the immutable original CHECK")
	}
	if original, err := render(view, owner, definition.Expression, definition.Dependencies); err != nil || original != originalSQL {
		t.Fatal("immutable original CHECK changed across rejected captures/current-name/remap", original, err)
	}
}
