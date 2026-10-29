package schemacmds_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestHstoreComparisonRemap(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	source, sourceExtension, sourceType := hstoreUnitInstalled(t)
	sourceView := source.CatalogRead()
	value, err := sourceType.SQLType.Input(ctx, "a=>1,b=>2", -1)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := sqltype.BindComparison(sourceType.SQLType, sourceType.SQLType, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Use the established genuine retained TEMP-provider import fixture. The
	// public Controls test separately forces all18 selected member collisions.
	target := engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	t.Cleanup(closeOwner)
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(ctx, source, sourceView); err != nil {
		t.Fatal(err)
	}
	view := target.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	imported := view.MustType(installation.Members[0])
	if imported.SQLType.ID() == sourceType.SQLType.ID() {
		t.Fatal("fixture did not remap the hstore signature type")
	}
	published := view.State()
	if len(published.ScalarImports()) != 0 {
		t.Fatal("publication retained candidate-only scalar import authority")
	}
	// ReplaceCatalog has already published and cleared its temporary scalar
	// translations. Retagging an independent source literal requires the same
	// explicit source/destination authority on an unpublished candidate.
	candidate := catalog.CloneState(published)
	candidate.SetScalarImport(sourceType.SQLType, imported.SQLType)
	view = view.WithState(candidate)
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		if old == sourceType.SQLType {
			return imported.SQLType
		}
		current, _ := view.LookupType(old.ID())
		return current
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{sourceExtension.Address(): installation.Address()}
	for i, address := range sourceExtension.Members {
		mapping[address] = installation.Members[i]
	}
	mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sourceType.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(imported.Array)}
	mapper := stored.ViewQueryRemapper{
		Catalog: view, Type: mapType,
		Ref: func(ref ids.ObjectRef) ids.ObjectRef {
			address := ref.Address
			if mapped, ok := mapping[address]; ok {
				address = mapped
			}
			return view.Owner().Domain.Ref(view.Epoch(), address)
		},
		Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), replacementProcedurePolicyFixture(source, target), nil),
	}
	mapOriginal := func(node expr.Expr) (expr.Expr, error) {
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{node}}})
		if err != nil {
			return nil, err
		}
		return query.(*plan.ValuesNode).Rows[0][0], nil
	}
	for kind, name := range extension.HstoreComparisonNames {
		t.Run(name, func(t *testing.T) {
			captured := sourceView.MustProcedure(sourceExtension.Members[extension.HstoreComparisonProcedureStart+kind])
			current := view.MustProcedure(installation.Members[extension.HstoreComparisonProcedureStart+kind])
			if captured.Name() != name || current.Name() != name {
				t.Fatal("wrong selected member", name, captured.Name(), current.Name())
			}
			result, _ := sourceView.LookupType(sqltype.ID(captured.Result))
			literal := plan.LiteralExpr{Info: expr.Info{Usage: sourceType.SQLType.Usage()}, Value: value}
			args := []expr.Expr{literal, literal}
			if kind == 7 {
				args = args[:1]
			}
			if kind == 8 {
				int8Type, _ := sourceView.LookupType(sqltype.Int8)
				seed, err := sqltype.Integer(int8Type, 1)
				if err != nil {
					t.Fatal(err)
				}
				args[1] = plan.LiteralExpr{Info: expr.Info{Usage: int8Type.Usage()}, Value: seed}
			}
			original := plan.HstoreComparisonExpr{
				Info: expr.Info{Usage: result.Usage(), RoutineReference: captured, Property: expr.Properties{Volatility: expr.Immutable}},
				Ref:  sourceView.Owner().Domain.Ref(sourceView.Epoch(), captured.Address()),
				Args: args, Comparison: comparison, Kind: uint8(kind), Namespace: "public",
			}
			mappedRef := view.Owner().Domain.Ref(view.Epoch(), current.Address())
			relocated, err := stored.RemapExpression(original, func(ref ids.ObjectRef) ids.ObjectRef {
				if ref != original.Ref {
					t.Fatal("unexpected routine reference", ref)
				}
				return mappedRef
			}, nil)
			if err != nil || relocated.(plan.HstoreComparisonExpr).Ref != mappedRef {
				t.Fatal("selected routine reference relocation", err)
			}
			if _, _, err := stored.RetagTypes(view.WithState(published), relocated, mapType); err == nil {
				t.Fatal("descriptor mapping alone authorized a cross-ID source literal")
			}
			rebound, err := mapOriginal(original)
			if err != nil {
				t.Fatal("mapped hstore declaration and signature", err)
			}
			mapped := rebound.(plan.HstoreComparisonExpr)
			mappedRoutine, ok := mapped.RoutineReference.(*catalog.Procedure)
			if !ok || mapped.Ref != mappedRef || !exec.SameRoutineDeclaration(current, mappedRoutine) || mappedRoutine.Revision() != captured.Revision() || mapped.Kind != original.Kind || mapped.Properties() != original.Properties() || mapped.SourcePosition() != original.SourcePosition() {
				t.Fatal("lost selected routine capture", mapped)
			}
			for i, argument := range mapped.Args {
				want := imported.SQLType
				if kind == 8 && i == 1 {
					want, _ = view.LookupType(sqltype.Int8)
				}
				if argument.ResultUsage().Type != want || argument.(plan.LiteralExpr).Value.Type() != want {
					t.Fatal("retained a source argument type", argument)
				}
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}}
			resultValue, err := mapped.Eval(&evaluation)
			if err != nil || resultValue.Type() != result {
				t.Fatal("remapped scalar execution", resultValue, err)
			}
			if kind == 6 {
				if n, err := resultValue.Integer(); err != nil || n != 0 {
					t.Fatal("remapped comparator", n, err)
				}
			}
			if kind == 7 {
				if n, err := resultValue.Integer(); err != nil || n != -1077040027 {
					t.Fatal("remapped hash", n, err)
				}
			}
			if kind == 8 {
				if n, err := resultValue.Integer(); err != nil || n != 7681866774737967775 {
					t.Fatal("remapped seeded hash", n, err)
				}
			}
			_, originalSignature, _ := extension.HstoreComparisonDeclaration(kind, sourceType.SQLType.ID())
			if original.Ref == mappedRef || original.RoutineReference != captured || original.Args[0].ResultUsage().Type != sourceType.SQLType || original.Args[0].(plan.LiteralExpr).Value != value || captured.Signature != originalSignature {
				t.Fatal("remapping changed the captured source")
			}
			missing := relocated.(plan.HstoreComparisonExpr)
			missing.Ref = ids.ObjectRef{}
			if _, _, err := stored.RetagTypes(view, missing, mapType); err == nil {
				t.Fatal("recovered a missing mapped identity from its name or source")
			}
			if _, _, err := stored.RetagTypes(catalog.NewRead(nil, view.State()), relocated, mapType); err == nil {
				t.Fatal("admitted a routine without registered catalog authority")
			}
			for _, signature := range []catalog.ProcedureSignature{
				extension.HstoreSignature(imported.SQLType.ID()) + ",25",
				"25," + extension.HstoreSignature(imported.SQLType.ID()),
			} {
				candidate := catalog.CloneState(view.State())
				wrong := *current
				wrong.Signature = signature
				candidate.SetDescriptor(wrong.Address(), &wrong)
				badMapper := mapper
				badMapper.Catalog = view.WithState(candidate)
				if _, err := badMapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{original}}}); err == nil {
					t.Fatal("admitted an incorrect mapped signature slot", signature)
				}
			}
		})
	}
	boolean, _ := sourceView.LookupType(sqltype.Bool)
	equality := sourceView.MustProcedure(sourceExtension.Members[extension.HstoreComparisonProcedureStart])
	inequality := sourceView.MustProcedure(sourceExtension.Members[extension.HstoreComparisonProcedureStart+1])
	literal := plan.LiteralExpr{Info: expr.Info{Usage: sourceType.SQLType.Usage()}, Value: value}
	null := plan.LiteralExpr{Info: literal.Info, Value: sqltype.Null(sourceType.SQLType)}
	trueValue := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	falseValue := plan.LiteralExpr{Info: trueValue.Info, Value: sqltype.Boolean(boolean, false)}
	equalInfo := expr.Info{Usage: boolean.Usage(), RoutineReference: equality}
	notEqualInfo := expr.Info{Usage: boolean.Usage(), RoutineReference: inequality}
	equalRef := sourceView.Owner().Domain.Ref(sourceView.Epoch(), equality.Address())
	notEqualRef := sourceView.Owner().Domain.Ref(sourceView.Epoch(), inequality.Address())
	equalPredicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, sourceType.SQLType, sourceType.SQLType, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	notEqualPredicate, err := sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, sourceType.SQLType, sourceType.SQLType, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	pair := plan.ComparisonExpr{Info: equalInfo, RoutineRef: equalRef, Left: literal, Right: literal, Predicate: equalPredicate, Operator: "="}
	for _, tc := range []struct {
		name string
		node expr.Expr
		want bool
	}{
		{"shared_in", plan.ScalarArrayComparisonExpr{Info: equalInfo, RoutineRef: equalRef, Left: literal, Items: []expr.Expr{literal, null}, Predicate: equalPredicate, Operator: "="}, true},
		{"shared_not_in", plan.ScalarArrayComparisonExpr{Info: notEqualInfo, RoutineRef: notEqualRef, Left: literal, Items: []expr.Expr{literal, null}, Predicate: notEqualPredicate, Operator: "<>", All: true}, false},
		{"shared_nullif", plan.ConditionalExpr{Info: expr.Info{Usage: sourceType.SQLType.Usage(), RoutineReference: equality}, RoutineRef: equalRef, Kind: parser.NullIf, Args: []expr.Expr{literal, literal}, Comparison: comparison}, false},
		{"shared_case", plan.CaseExpr{Info: trueValue.Info, Test: literal, Arms: []plan.CaseBranch{{Test: plan.CaseTestExpr{Info: literal.Info}, TestUsage: literal.ResultUsage(), Condition: literal, Result: trueValue, Equality: comparison, Routine: equality, RoutineRef: equalRef}}, Otherwise: falseValue}, true},
		{"shared_comparison", pair, true},
		{"shared_distinct", plan.DistinctExpr{Info: equalInfo, RoutineRef: equalRef, Left: literal, Right: literal, Equality: equalPredicate}, false},
		{"shared_row", plan.RowComparisonExpr{Info: trueValue.Info, Comparisons: []plan.ComparisonExpr{pair, pair}, Operator: "="}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			procedure := equality
			member := extension.HstoreComparisonProcedureStart
			if tc.name == "shared_not_in" {
				procedure, member = inequality, extension.HstoreComparisonProcedureStart+1
			}
			current := view.MustProcedure(installation.Members[member])
			originalSQL, err := (stored.ScalarRenderer{}).SQL(ctx, sourceView, tc.node)
			if err != nil {
				t.Fatal("source rendering", err)
			}
			if tc.name == "shared_in" || tc.name == "shared_not_in" {
				if strings.Contains(originalSQL, "ARRAY[") || !strings.Contains(originalSQL, " IN (") {
					t.Fatal("scalar membership rendered as an unsupported public array", originalSQL)
				}
			}
			relocated, err := stored.RemapExpression(tc.node, func(ref ids.ObjectRef) ids.ObjectRef {
				if ref.Address != procedure.Address() {
					t.Fatal("unexpected selected reference", ref)
				}
				return view.Owner().Domain.Ref(view.Epoch(), current.Address())
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			mapped, err := mapOriginal(tc.node)
			if err != nil {
				t.Fatal("shared carrier relocation", err)
			}
			plan.WalkExpression(mapped, func(node expr.Expr) {
				if ref := expr.InfoOf(node).RoutineReference; ref != nil {
					captured, ok := ref.(*catalog.Procedure)
					if !ok || !exec.SameRoutineDeclaration(current, captured) || captured.Revision() != procedure.Revision() {
						t.Fatal("incoherent original routine", ref)
					}
				}
				if node.ResultUsage().Type == sourceType.SQLType {
					t.Fatal("source operand type retained")
				}
				if branch, ok := node.(plan.CaseExpr); ok {
					for _, arm := range branch.Arms {
						if !exec.SameRoutineDeclaration(current, arm.Routine) || arm.Routine.Revision() != procedure.Revision() || arm.RoutineRef != view.Owner().Domain.Ref(view.Epoch(), current.Address()) {
							t.Fatal("source CASE branch capture retained")
						}
					}
				}
			})
			collector := stored.DependRefs{Context: ctx, Catalog: view}
			collector.Expression(mapped)
			if _, found := collector.Addresses[current.Address()]; collector.Err != nil || !found {
				t.Fatal("missing selected procedure dependency", collector.Err)
			}
			if row, ok := mapped.(plan.RowComparisonExpr); ok {
				for _, pair := range row.Comparisons {
					captured, ok := pair.RoutineReference.(*catalog.Procedure)
					if !ok || !exec.SameRoutineDeclaration(current, captured) || captured.Revision() != procedure.Revision() || pair.RoutineRef != view.Owner().Domain.Ref(view.Epoch(), current.Address()) {
						t.Fatal("source row-pair routine capture retained")
					}
				}
				subquery := plan.SubqueryExpr{Comparisons: row.Comparisons, QueryPlan: &plan.Query{Root: &plan.SingletonNode{}}}
				queryDependencies := stored.DependRefs{Context: ctx, Catalog: view}
				queryDependencies.Expression(subquery)
				if _, found := queryDependencies.Addresses[current.Address()]; queryDependencies.Err != nil || !found {
					t.Fatal("missing selected subquery-pair procedure dependency", queryDependencies.Err)
				}
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}}
			result, err := mapped.Eval(&evaluation)
			if err != nil {
				t.Fatal("mapped execution", err)
			}
			if tc.name == "shared_nullif" {
				if !result.IsNull() || result.Type() != imported.SQLType {
					t.Fatal("NULLIF lost hstore result while equality returns BOOL", result)
				}
			} else if truth, err := result.Boolean(); err != nil || result.IsNull() || truth != tc.want {
				t.Fatal("mapped truth", result, err)
			}
			if after, err := (stored.ScalarRenderer{}).SQL(ctx, sourceView, tc.node); err != nil || after != originalSQL || procedure.Signature != hstoreUnitComparisonSignature(sourceType.SQLType.ID()) {
				t.Fatal("source capture mutated", after, err)
			}
			missing, err := stored.RemapExpression(tc.node, func(ids.ObjectRef) ids.ObjectRef { return ids.ObjectRef{} }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := stored.RetagTypes(view, missing, mapType); err == nil {
				t.Fatal("recovered a missing mapped shared identity")
			}
			if _, _, err := stored.RetagTypes(catalog.NewRead(nil, view.State()), relocated, mapType); err == nil {
				t.Fatal("shared carrier admitted ownerless authority")
			}
			if _, _, err := stored.RetagTypes(view.WithState(published), relocated, mapType); err == nil {
				t.Fatal("shared source literals admitted without scalar import authority")
			}
			foreign, err := stored.RemapExpression(tc.node, func(ids.ObjectRef) ids.ObjectRef {
				return sourceView.Owner().Domain.Ref(view.Epoch(), current.Address())
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := stored.RetagTypes(view, foreign, mapType); err == nil {
				t.Fatal("shared carrier admitted a foreign mapped reference domain")
			}
			stale, err := stored.RemapExpression(tc.node, func(ids.ObjectRef) ids.ObjectRef {
				return view.Owner().Domain.Ref(view.Epoch()+1, current.Address())
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := stored.RetagTypes(view, stale, mapType); err == nil {
				t.Fatal("shared carrier admitted a stale mapped reference epoch")
			}
			invalidManifest := catalog.CloneState(view.State())
			invalidInstallation := *installation
			invalidInstallation.Version = "invalid"
			invalidManifest.SetDescriptor(installation.Address(), &invalidInstallation)
			if _, _, err := stored.RetagTypes(view.WithState(invalidManifest), relocated, mapType); err == nil {
				t.Fatal("shared carrier admitted an invalid stock manifest")
			}
			for _, corrupt := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Signature = extension.HstoreSignature(imported.SQLType.ID()) + ",25" },
				func(p *catalog.Procedure) { p.Signature = "25," + extension.HstoreSignature(imported.SQLType.ID()) },
				func(p *catalog.Procedure) { p.Result = 23 },
				func(p *catalog.Procedure) { p.ObjectName = "hstore_lt" },
				func(p *catalog.Procedure) { p.Implementation = extension.StockCitext },
			} {
				bad := catalog.CloneState(view.State())
				p := *current
				corrupt(&p)
				bad.SetDescriptor(p.Address(), &p)
				if _, _, err := stored.RetagTypes(view.WithState(bad), relocated, mapType); err == nil {
					t.Fatal("shared carrier admitted a corrupt selected declaration")
				}
			}
		})
	}

}

func hstoreUnitComparisonSignature(id sqltype.ID) catalog.ProcedureSignature {
	s := extension.HstoreSignature(id)
	return s + "," + s
}
