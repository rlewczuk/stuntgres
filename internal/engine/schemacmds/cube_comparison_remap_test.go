package schemacmds_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func cubeComparisonExpressionRemap(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	source, sourceExtension, sourceType := cubeUnitInstalled(t)
	sourceView := source.CatalogRead()
	value, err := sourceType.SQLType.Input(ctx, "(3,1),(1,4)", -1)
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
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	imported := view.MustType(installation.Members[0])
	if imported.SQLType.ID() == sourceType.SQLType.ID() {
		t.Fatal("fixture did not remap the cube signature type")
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
	for kind, name := range []string{"cube_eq", "cube_ne", "cube_lt", "cube_le", "cube_gt", "cube_ge", "cube_cmp"} {
		t.Run(name, func(t *testing.T) {
			captured := sourceView.MustProcedure(sourceExtension.Members[3+kind])
			current := view.MustProcedure(installation.Members[3+kind])
			if captured.Name() != name || current.Name() != name {
				t.Fatal("wrong selected member", name, captured.Name(), current.Name())
			}
			result, _ := sourceView.LookupType(sqltype.ID(captured.Result))
			literal := plan.LiteralExpr{Info: expr.Info{Usage: sourceType.SQLType.Usage()}, Value: value}
			original := plan.CubeComparisonExpr{
				Info: expr.Info{Usage: result.Usage(), RoutineReference: captured, Property: expr.Properties{Volatility: expr.Immutable}},
				Ref:  sourceView.Owner().Domain.Ref(sourceView.Epoch(), captured.Address()),
				Args: [2]expr.Expr{literal, literal}, Comparison: comparison, Kind: uint8(kind), Namespace: "public",
			}
			mappedRef := view.Owner().Domain.Ref(view.Epoch(), current.Address())
			relocated, err := stored.RemapExpression(original, func(ref ids.ObjectRef) ids.ObjectRef {
				if ref != original.Ref {
					t.Fatal("unexpected routine reference", ref)
				}
				return mappedRef
			}, nil)
			if err != nil || relocated.(plan.CubeComparisonExpr).Ref != mappedRef {
				t.Fatal("selected routine reference relocation", err)
			}
			if _, _, err := stored.RetagTypes(view.WithState(published), relocated, mapType); err == nil {
				t.Fatal("descriptor mapping alone authorized a cross-ID source literal")
			}
			rebound, err := mapOriginal(original)
			if err != nil {
				t.Fatal("mapped cube declaration and signature", err)
			}
			mapped := rebound.(plan.CubeComparisonExpr)
			mappedRoutine, ok := mapped.RoutineReference.(*catalog.Procedure)
			if !ok || mapped.Ref != mappedRef || !exec.SameRoutineDeclaration(current, mappedRoutine) || mappedRoutine.Revision() != captured.Revision() || mapped.Kind != original.Kind || mapped.Properties() != original.Properties() || mapped.SourcePosition() != original.SourcePosition() {
				t.Fatal("lost selected routine capture", mapped)
			}
			for _, argument := range mapped.Args {
				if argument.ResultUsage().Type != imported.SQLType || argument.(plan.LiteralExpr).Value.Type() != imported.SQLType {
					t.Fatal("retained a source argument type", argument)
				}
			}
			if n, err := mapped.Comparison.Compare(ctx, mapped.Args[0].(plan.LiteralExpr).Value, mapped.Args[1].(plan.LiteralExpr).Value, sqltype.InputEnvironment{}); err != nil || n != 0 {
				t.Fatal("remapped typed comparison", n, err)
			}
			if original.Ref == mappedRef || original.RoutineReference != captured || original.Args[0].ResultUsage().Type != sourceType.SQLType || original.Args[0].(plan.LiteralExpr).Value != value || captured.Signature != extension.CubeComparisonSignature(sourceType.SQLType.ID()) {
				t.Fatal("remapping changed the captured source")
			}
			missing := relocated.(plan.CubeComparisonExpr)
			missing.Ref = ids.ObjectRef{}
			if _, _, err := stored.RetagTypes(view, missing, mapType); err == nil {
				t.Fatal("recovered a missing mapped identity from its name or source")
			}
			if _, _, err := stored.RetagTypes(catalog.NewRead(nil, view.State()), relocated, mapType); err == nil {
				t.Fatal("admitted a routine without registered catalog authority")
			}
			for _, signature := range []catalog.ProcedureSignature{
				extension.CubeSignature(imported.SQLType.ID()) + ",25",
				"25," + extension.CubeSignature(imported.SQLType.ID()),
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
	equality := sourceView.MustProcedure(sourceExtension.Members[3])
	inequality := sourceView.MustProcedure(sourceExtension.Members[4])
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
			member := 3
			if tc.name == "shared_not_in" {
				procedure, member = inequality, 4
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
					t.Fatal("NULLIF lost cube result while equality returns BOOL", result)
				}
			} else if truth, err := result.Boolean(); err != nil || result.IsNull() || truth != tc.want {
				t.Fatal("mapped truth", result, err)
			}
			if after, err := (stored.ScalarRenderer{}).SQL(ctx, sourceView, tc.node); err != nil || after != originalSQL || procedure.Signature != extension.CubeComparisonSignature(sourceType.SQLType.ID()) {
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
				func(p *catalog.Procedure) { p.Signature = extension.CubeSignature(imported.SQLType.ID()) + ",25" },
				func(p *catalog.Procedure) { p.Signature = "25," + extension.CubeSignature(imported.SQLType.ID()) },
				func(p *catalog.Procedure) { p.Result = 23 },
				func(p *catalog.Procedure) { p.ObjectName = "cube_lt" },
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
	t.Run("scalar_membership_recipe", func(t *testing.T) {
		op, done := enginetest.RoleOperation(t, source, "postgres")
		defer done()
		for _, text := range []string{"'1'::cube IN ('1'::cube,'2'::cube)", "'1'::cube NOT IN ('1'::cube,'2'::cube)"} {
			raw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0]
			source.Mu.Lock()
			auth, failure := source.AuthorizationLocked(op)
			if failure != nil {
				source.Mu.Unlock()
				t.Fatal(failure)
			}
			b := core.NewExpressionBinder(ctx, source, sqltype.InputEnvironment{}, op, nil)
			b.Query.Auth = &auth
			b.Params.NoParameters = true
			bound, failure := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if failure == nil {
				failure = b.CaptureViewLiterals(raw, bound, analyze.ViewTargetLiteral, 0)
			}
			source.Mu.Unlock()
			if failure != nil {
				t.Fatal("actual membership capture", text, failure)
			}
			batch, ok := bound.(plan.ScalarArrayComparisonExpr)
			if !ok || batch.Array != nil || len(batch.Items) != 2 || batch.RoutineRef == (ids.ObjectRef{}) {
				t.Fatal("lost selected scalar batch", bound)
			}
			frozen := b.NormalizeStoredExpression(raw)
			if _, ok := frozen.(parser.InListExpr); !ok {
				t.Fatal("scalar batch acquired ARRAY syntax", frozen)
			}
			r := stored.QueryRenderer{View: stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}), Frame: b.FrameID()}
			sql, failure := r.ExpressionSQL(ctx, sourceView, frozen, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
			if failure != nil || strings.Contains(sql, "ARRAY[") || !strings.Contains(sql, " IN (") {
				t.Fatal("frozen membership rendering", sql, failure)
			}
		}
	})
}

func TestCubeDomainCheckOperatorProviders(t *testing.T) {
	e, installation, base := cubeUnitInstalled(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	_, err := e.Execute(op, enginetest.Statement(t, "CREATE DOMAIN public.cube_check_provider AS public.cube CHECK(NULLIF(VALUE,'1'::public.cube) IS NULL OR VALUE='2'::public.cube)"), 0, nil)
	done()
	if err != nil {
		t.Fatal(err)
	}
	installer, closeInstaller := enginetest.RoleOperation(t, e, "postgres")
	extensionUnitPublish(t, e, installer, cubeUnitStage(t, e, installer, "uuid-ossp", "public"))
	closeInstaller()
	view := e.CatalogRead()
	domain := view.MustType(view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: base.Namespace(), Name: "cube_check_provider"}))
	if err := stored.ValidateDomainCheckDefinitions(view, domain); err != nil {
		t.Fatal("actual selected cube operator rejected", err)
	}
	var selected ids.ObjectRef
	for _, dependency := range domain.DomainChecks[0].Dependencies {
		if dependency.Ref.Address.ClassID == ids.OperatorClassID {
			selected = dependency.Ref
		}
	}
	if selected.Address != installation.Members[10] {
		t.Fatal("missing actual selected equality dependency", selected)
	}
	for _, kind := range []string{"wrong_lifetime", "wrong_member", "subobject"} {
		t.Run(kind, func(t *testing.T) {
			wrong := *domain
			wrong.DomainChecks = append([]catalog.DomainCheckConstraint(nil), domain.DomainChecks...)
			wrong.DomainChecks[0].Dependencies = append([]catalog.ExpressionDependency(nil), domain.DomainChecks[0].Dependencies...)
			for i, dependency := range wrong.DomainChecks[0].Dependencies {
				if dependency.Ref != selected {
					continue
				}
				switch kind {
				case "wrong_lifetime":
					dependency.Ref.Epoch++
				case "wrong_member":
					dependency.Ref.Address = installation.Members[11]
				case "subobject":
					dependency.Ref.Address.SubID = 1
				}
				wrong.DomainChecks[0].Dependencies[i] = dependency
			}
			if err := stored.ValidateDomainCheckDefinitions(view, &wrong); err == nil {
				t.Fatal("accepted incorrect domain operator dependency")
			}
		})
	}
	operator, _ := view.Operator(selected.Address)
	for _, kind := range []string{"origin", "left_slot", "right_slot", "procedure", "extension", "principal", "backing_principal"} {
		t.Run(kind, func(t *testing.T) {
			candidate := catalog.CloneState(view.State())
			wrong := *operator
			switch kind {
			case "origin":
				forged := *wrong.Origin.Installation
				wrong.Origin = &catalog.InstalledOrigin{Installation: &forged, Declaration: wrong.Origin.Declaration}
			case "left_slot":
				wrong.Left = ids.OID(sqltype.Text)
			case "right_slot":
				wrong.Right = ids.OID(sqltype.Text)
			case "procedure":
				wrong.Function = installation.Members[4].ObjectID
			case "extension":
				wrong.Extension.ObjectID = 990001
			case "principal":
				wrong.Owner = (&authorization.Domain{}).Principal(wrong.Owner.ID())
			case "backing_principal":
				backing := *candidate.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: wrong.Function})
				backing.Owner = (&authorization.Domain{}).Principal(backing.Owner.ID())
				candidate.SetDescriptor(backing.Address(), &backing)
			}
			candidate.SetDescriptor(wrong.Address(), &wrong)
			if err := stored.ValidateDomainCheckDefinitions(view.WithState(candidate), domain); err == nil {
				t.Fatal("accepted corrupt stock operator")
			}
		})
	}
	for _, foreign := range []bool{false, true} {
		candidate := catalog.CloneState(view.State())
		current := *operator
		current.ObjectName = "==="
		current.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		current.Extension = ids.ObjectAddress{}
		parent := *installation
		parent.Members = nil
		for _, address := range installation.Members {
			if address != operator.Address() {
				parent.Members = append(parent.Members, address)
			}
		}
		candidate.SetDescriptor(parent.Address(), &parent)
		if foreign {
			target := *view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
			target.Members = append(append([]ids.ObjectAddress(nil), target.Members...), operator.Address())
			current.Extension = target.Address()
			candidate.SetDescriptor(target.Address(), &target)
		}
		candidate.UnbindName(catalog.DescriptorNameKey(operator))
		candidate.SetDescriptor(current.Address(), &current)
		candidate.BindName(catalog.DescriptorNameKey(&current), current.Address())
		if err := stored.ValidateDomainCheckDefinitions(view.WithState(candidate), domain); err != nil {
			t.Fatal("lawful operator rename/schema/membership mutation rejected", foreign, err)
		}
	}
	t.Run("retained_temp_import", func(t *testing.T) {
		target := engine.New()
		owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
		t.Cleanup(closeOwner)
		namespace := intarrayUnitTemporaryNamespace(t, target, owner)
		installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
		extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
		closeInstaller()
		if _, err := target.ReplaceCatalog(context.Background(), e, view); err != nil {
			t.Fatal("domain operator import", err)
		}
		mapped := target.CatalogRead()
		cube := mapped.MustExtension(mapped.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
		mappedBase := mapped.MustType(cube.Members[0])
		imported := mapped.MustType(mapped.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: mappedBase.Namespace(), Name: domain.Name()}))
		if err := stored.ValidateDomainCheckDefinitions(mapped, imported); err != nil {
			t.Fatal("mapped domain operator authority", err)
		}
		found := false
		for _, dependency := range imported.DomainChecks[0].Dependencies {
			if dependency.Ref.Address.ClassID != ids.OperatorClassID {
				continue
			}
			if dependency.Ref.Address != cube.Members[10] || dependency.Ref == selected || dependency.Ref.Domain() != mapped.Owner().Domain || dependency.Ref.Epoch != mapped.Epoch() {
				t.Fatal("operator dependency lost mapped identity/lifetime", dependency)
			}
			found = true
		}
		if !found {
			t.Fatal("import erased selected operator dependency")
		}
	})
}

func TestCubeRuleConditionRemap(t *testing.T) {
	source, installation, base := cubeUnitInstalled(t)
	op, done := enginetest.RoleOperation(t, source, "postgres")
	for _, sql := range []string{
		"CREATE TABLE public.cube_rule_provider(v public.cube)",
		"CREATE RULE cube_rule_condition AS ON UPDATE TO public.cube_rule_provider WHERE NEW.v IN ('1'::public.cube,'2'::public.cube) DO ALSO NOTHING",
	} {
		if _, err := source.Execute(op, enginetest.Statement(t, sql), 0, nil); err != nil {
			done()
			t.Fatal(err)
		}
	}
	done()
	view := source.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: base.Namespace(), Name: "cube_rule_provider"}))
	condition := stored.RulesOf(relation)[0].Condition().(plan.ScalarArrayComparisonExpr)
	originalRef := condition.RoutineRef
	captured := condition.RoutineReference.(*catalog.Procedure)
	originalSignature := captured.Signature
	if originalRef.Address != installation.Members[3] {
		t.Fatal("rule did not capture selected cube equality", originalRef)
	}
	target := engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	t.Cleanup(closeOwner)
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(context.Background(), source, view); err != nil {
		t.Fatal("actual rule condition import", err)
	}
	mapped := target.CatalogRead()
	cube := mapped.MustExtension(mapped.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	mappedBase := mapped.MustType(cube.Members[0])
	imported := mapped.MustRelation(mapped.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: mappedBase.Namespace(), Name: relation.Name()}))
	next := stored.RulesOf(imported)[0].Condition().(plan.ScalarArrayComparisonExpr)
	if next.RoutineRef.Address != cube.Members[3] || next.RoutineRef == originalRef || next.RoutineRef.Domain() != mapped.Owner().Domain || next.RoutineRef.Epoch != mapped.Epoch() || next.Left.ResultUsage().Type != mappedBase.SQLType {
		t.Fatal("rule condition retained source identity/type/lifetime", next.RoutineRef)
	}
	if next.RoutineReference != mapped.MustProcedure(cube.Members[3]) {
		t.Fatal("rule condition retained source procedure carrier")
	}
	unchanged := stored.RulesOf(relation)[0].Condition().(plan.ScalarArrayComparisonExpr)
	if unchanged.RoutineRef != originalRef || unchanged.RoutineReference != captured || unchanged.Left.ResultUsage().Type != base.SQLType || captured.Signature != originalSignature {
		t.Fatal("rule import mutated immutable source capture")
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == base.SQLType {
			return mappedBase.SQLType
		}
		return old
	}
	if _, err := stored.RemapRules(mapped, relation, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, typ, nil, nil); err == nil {
		t.Fatal("rule condition accepted an unmapped source lifetime")
	}
}

func TestCubeComparisonBindingWrittenProjection(t *testing.T) {
	ctx := context.Background()
	source, installation, base := cubeUnitInstalled(t)
	op, done := enginetest.RoleOperation(t, source, "postgres")
	defer done()
	if _, err := source.Execute(op, enginetest.Statement(t, "CREATE FUNCTION public.cube_written_projection(p public.cube DEFAULT NULLIF('1'::public.cube,'1'::public.cube)) RETURNS boolean LANGUAGE SQL RETURN p IS NULL"), 0, nil); err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, source, op, cubeUnitStage(t, source, op, "uuid-ossp", "public"))
	view := source.CatalogRead()
	bind := func(text string) plan.SQLRoutineExpr {
		raw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0]
		source.Mu.Lock()
		auth, err := source.AuthorizationLocked(op)
		var bound expr.Expr
		if err == nil {
			b := core.NewExpressionBinder(ctx, source, sqltype.InputEnvironment{}, op, nil)
			b.Query.Auth = &auth
			b.Params.NoParameters = true
			bound, err = b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		}
		source.Mu.Unlock()
		if err != nil {
			t.Fatal("actual routine call binding", text, err)
		}
		call, ok := bound.(plan.SQLRoutineExpr)
		if !ok {
			t.Fatalf("unexpected actual routine carrier %T", bound)
		}
		return call
	}
	omitted := bind("public.cube_written_projection()")
	if omitted.WrittenCount != 0 || len(omitted.Args) != 1 {
		t.Fatal("fixture did not expand a real omitted default")
	}
	namespace, err := stored.ComparisonBindingNamespace(view, omitted, nil)
	if err != nil || namespace != (ids.ObjectAddress{}) {
		t.Fatal("omitted execution default became caller binding requirement", namespace, err)
	}
	definition, ok := omitted.Procedure.SQLDefaults.Default(0)
	if !ok {
		t.Fatal("missing actual declaration default")
	}
	namespace, err = stored.ComparisonBindingNamespace(view, definition, nil)
	if err != nil || namespace != base.Namespace() {
		t.Fatal("declaration default lost its actual binding requirement", namespace, err)
	}
	explicit := bind("public.cube_written_projection(NULLIF('2'::public.cube,'1'::public.cube))")
	namespace, err = stored.ComparisonBindingNamespace(view, explicit, nil)
	if err != nil || explicit.WrittenCount != 1 || namespace != base.Namespace() {
		t.Fatal("explicit written comparison lost caller binding requirement", namespace, err)
	}
	plain := bind("public.cube_written_projection(NULL)")
	namespace, err = stored.ComparisonBindingNamespace(view, plain, nil)
	if err != nil || plain.WrittenCount != 1 || namespace != (ids.ObjectAddress{}) {
		t.Fatal("ordinary written NULL acquired a comparison requirement", namespace, err)
	}
	for _, count := range []int{-1, len(omitted.Args) + 1} {
		wrong := omitted
		wrong.WrittenCount = count
		if _, err := stored.ComparisonBindingNamespace(view, wrong, nil); err == nil {
			t.Fatal("accepted invalid written projection", count)
		}
	}
	executableChildren := 0
	plan.ExpressionChildren(omitted, func(expr.Expr) { executableChildren++ })
	if executableChildren != 1 || omitted.WrittenCount != 0 || len(omitted.Args) != 1 {
		t.Fatal("binding traversal changed full executable children")
	}
	dependencies := stored.DependRefs{Context: ctx, Catalog: view}
	dependencies.Expression(omitted)
	if _, found := dependencies.Addresses[installation.Members[3]]; dependencies.Err != nil || !found {
		t.Fatal("omitted default lost executable comparison dependency", dependencies.Err)
	}
	namespace, err = stored.ComparisonBindingNamespace(view, definition, nil)
	if err != nil || namespace != base.Namespace() {
		t.Fatal("caller classification mutated declaration default", namespace, err)
	}
	selected := view.MustProcedure(installation.Members[3])
	receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
	for _, membership := range []string{"attached", "detached", "foreign"} {
		t.Run(membership, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			current := *selected
			current.ObjectName = "comparison_now"
			current.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
			if membership != "attached" {
				parent := *installation
				parent.Members = nil
				for _, address := range installation.Members {
					if address != selected.Address() {
						parent.Members = append(parent.Members, address)
					}
				}
				state.SetDescriptor(parent.Address(), &parent)
				current.Extension = ids.ObjectAddress{}
			}
			if membership == "foreign" {
				target := *receiver
				target.Members = append(append([]ids.ObjectAddress(nil), receiver.Members...), selected.Address())
				state.SetDescriptor(target.Address(), &target)
				current.Extension = target.Address()
			}
			state.UnbindName(catalog.DescriptorNameKey(selected))
			state.SetDescriptor(current.Address(), &current)
			state.BindName(catalog.DescriptorNameKey(&current), current.Address())
			visits := 0
			namespace, err := stored.ComparisonBindingNamespace(view.WithState(state), definition, func(p *catalog.Procedure) error {
				visits++
				if p != &current {
					t.Fatal("callback did not receive genuine current comparison", p)
				}
				return nil
			})
			if err != nil || visits == 0 || namespace != base.Namespace() || namespace == current.Namespace() {
				t.Fatal("comparison binding followed independently moved backing routine instead of operator", membership, namespace, visits, err)
			}
			for _, corruption := range []string{"seal", "principal", "type"} {
				broken := catalog.CloneState(state)
				bad := current
				switch corruption {
				case "seal":
					unsealed := *bad.Origin.Installation
					bad.Origin = &catalog.InstalledOrigin{Installation: &unsealed, Declaration: bad.Origin.Declaration}
				case "principal":
					bad.Owner = (&authorization.Domain{}).Principal(bad.Owner.ID())
				case "type":
					bad.Signature = "25,25"
				}
				broken.SetDescriptor(bad.Address(), &bad)
				if _, err := stored.ComparisonBindingNamespace(view.WithState(broken), definition, nil); err == nil {
					t.Fatal("comparison binding admitted corrupt current authority", corruption)
				}
			}
			operator := view.MustOperator(installation.Members[10])
			for _, corruption := range []string{"seal", "principal", "operand", "function", "name"} {
				broken := catalog.CloneState(state)
				bad := *operator
				switch corruption {
				case "seal":
					unsealed := *bad.Origin.Installation
					bad.Origin = &catalog.InstalledOrigin{Installation: &unsealed, Declaration: bad.Origin.Declaration}
				case "principal":
					bad.Owner = (&authorization.Domain{}).Principal(bad.Owner.ID())
				case "operand":
					bad.Left = 25
				case "function":
					bad.Function = installation.Members[4].ObjectID
				case "name":
					broken.UnbindName(catalog.DescriptorNameKey(&bad))
					bad.ObjectName = "==="
					broken.BindName(catalog.DescriptorNameKey(&bad), bad.Address())
				}
				broken.SetDescriptor(bad.Address(), &bad)
				if _, err := stored.ComparisonBindingNamespace(view.WithState(broken), definition, nil); err == nil {
					t.Fatal("comparison binding admitted corrupt current operator authority", corruption)
				}
			}
		})
	}
}
