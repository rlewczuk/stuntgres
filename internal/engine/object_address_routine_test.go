package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These tests exercise the actual bound carrier and its original manifest,
// not public SQL answers or standalone Eval as a substitute for admission.
func TestObjectAddressRoutineOriginalAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		name, expression, signature string
		oid, result                 ids.OID
		slots                       [3]uint8
	}{
		{"pg_identify_object", "pg_identify_object(objsubid=>0,objid=>23,classid=>1247)", "26,26,23", 3839, 2249, [3]uint8{2, 1, 0}},
		{"pg_identify_object_as_address", "pg_identify_object_as_address(objsubid=>0,objid=>23,classid=>1247)", "26,26,23", 3382, 2249, [3]uint8{2, 1, 0}},
		{"pg_get_object_address", "pg_get_object_address(object_args=>'{}',type=>'type',object_names=>'{integer}')", "25,1009,1009", 3954, 2249, [3]uint8{2, 0, 1}},
		{"pg_describe_object", "pg_describe_object(1247,23,0)", "26,26,23", 3537, 25, [3]uint8{0, 1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			carrier, ok := node.(objectAddressExpression)
			if !ok || carrier.slots != tc.slots || carrier.validateStored() != nil {
				t.Fatal("canonical named-slot carrier", node)
			}
			captured, err := expressionRoutineDescriptor(node)
			if err != nil || captured == nil {
				t.Fatal("missing genuine routine reference", captured, err)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != tc.name || string(captured.Signature) != tc.signature || captured.Result != tc.result || captured.RoutineKind != functionRoutine || !captured.Strict || captured.Volatility != expr.Stable || captured.Revision() == 0 || len(captured.Body) != 0 {
				t.Fatal("independent released declaration", captured)
			}
			if carrier.Properties().Volatility != expr.Stable || generationImmutable(carrier) {
				t.Fatal("address inquiry lost STABLE boundary")
			}
			owned, ok := evaluation.catalog.Procedure(address)
			if !ok || owned != captured {
				t.Fatal("reference is not the original owned descriptor")
			}
			original := append([]expressionDependency(nil), binder.dependencies...)
			found := false
			for _, dependency := range original {
				if dependency.Ref.Address == address {
					found = true
					if dependency.Revision != captured.Revision() || dependency.Ref.Epoch != evaluation.catalog.Epoch() || dependency.Ref.Domain() != e.catalog.Domain {
						t.Fatal("original canonical dependency", dependency)
					}
				}
			}
			if !found {
				t.Fatal("canonical callable absent from original manifest")
			}
			before := carrier
			if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original admission", err)
			}
			missing := make([]expressionDependency, 0, len(original))
			for _, dependency := range original {
				if dependency.Ref.Address != address {
					missing = append(missing, dependency)
				}
			}
			for _, manifest := range [][]expressionDependency{nil, {}, missing} {
				probe := evaluation
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, node, true, false); err == nil {
					t.Fatal("missing original manifest acquired authority from an OID")
				}
			}
			for _, epoch := range []bool{false, true} {
				stale := append([]expressionDependency(nil), original...)
				for i := range stale {
					if stale[i].Ref.Address == address {
						if epoch {
							stale[i].Ref.Epoch++
						} else {
							stale[i].Revision++
						}
					}
				}
				probe := evaluation
				probe.routineChecks = stale
				if err := admitRoutineCall(&probe, node, true, false); err == nil {
					t.Fatal("stale original epoch/revision admitted")
				}
			}
			probe := evaluation
			foreignAuth := *foreign.routineAuthority
			probe.routineAuthority = &foreignAuth
			if err := admitRoutineCall(&probe, node, true, false); err == nil {
				t.Fatal("foreign original descriptor admitted")
			}
			remapped, _, err := retagBoundTypes(other.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil {
					return nil
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual carrier retag", err)
			}
			if err := admitRoutineCall(&probe, remapped, true, false); err == nil {
				t.Fatal("foreign retag manufactured an original manifest")
			}
			// A catalog-local replacement with an incompatible declaration is
			// not rescued by an equal numeric address either.
			changed := *evaluation.routineAuthority
			changed.catalog = changed.catalog.WithState(catalog.CloneState(changed.catalog.State()))
			replacement := *captured
			replacement.Strict = false
			changed.catalog.State().SetDescriptor(address, &replacement)
			probe = evaluation
			probe.routineAuthority = &changed
			if err := admitRoutineCall(&probe, node, true, false); err == nil {
				t.Fatal("changed selected declaration admitted")
			}
			if !reflect.DeepEqual(carrier, before) || !reflect.DeepEqual(binder.dependencies, original) {
				t.Fatal("negative probes mutated original carrier/manifest")
			}
			if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original authority lost after negative probes", err)
			}
		})
	}
}

func TestObjectAddressSourceNullOriginalGraph(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		name, expression  string
		parameter, pruned bool
	}{
		{"literal_scalar", "pg_describe_object(NULL, nextval('strict_null_steps')::oid,0)", false, true},
		{"parameter_scalar", "pg_describe_object($1::oid,nextval('strict_null_steps')::oid,0)", true, true},
		{"nonnull_scalar", "pg_describe_object(1247,23,0)", false, false},
		{"runtime_scalar", "pg_describe_object(1247,23,v)", false, false},
		{"record_identify", "pg_identify_object($1::oid,nextval('strict_null_steps')::oid,0)", true, false},
		{"record_address", "pg_identify_object_as_address($1::oid,nextval('strict_null_steps')::oid,0)", true, false},
		{"record_inverse", "pg_get_object_address($1::text,ARRAY[nextval('strict_null_steps')::text],'{}')", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, evaluation := strictNullUnitBind(t, e, op, scope, "1")
			text := "SELECT * FROM " + tc.expression + " WITH ORDINALITY"
			if tc.name == "runtime_scalar" {
				text = "SELECT f.* FROM strict_null_rows,LATERAL " + tc.expression + " WITH ORDINALITY AS f"
			}
			raw := statement(t, text).(*parser.Select)
			e.mu.Lock()
			binder := newExpressionBinder(context.Background(), e, scope.input, op, nil)
			binder.queryAuth = evaluation.routineAuthority
			query, err := binder.bindSelect(raw, false)
			e.mu.Unlock()
			if err != nil {
				t.Fatal("actual source binding", err)
			}
			var source *functionFromNode
			var findSource func(LogicalNode)
			findSource = func(node LogicalNode) {
				if node == nil {
					return
				}
				if found, ok := node.(*functionFromNode); ok {
					source = found
					return
				}
				left, right := queryInputs(node)
				findSource(left)
				findSource(right)
			}
			findSource(query.Root)
			if source == nil {
				t.Fatal("missing analyzed function source")
			}
			evaluation.routineChecks = binder.dependencies
			if tc.parameter {
				evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.parameters[1].Type)}
			}
			originalCalls := append([]expr.Expr(nil), source.calls...)
			originalSchema := append(querySchema(nil), source.querySchema...)
			originalManifest := append([]expressionDependency(nil), binder.dependencies...)
			planned, err := preflightFunctionFrom(&evaluation, source)
			if err != nil {
				t.Fatal("source preflight", err)
			}
			execution := planned.(*functionFromNode)
			pruned := execution.scalarNull != nil && execution.scalarNull[0]
			if pruned != tc.pruned || source.scalarNull != nil || !reflect.DeepEqual(source.calls, originalCalls) || !reflect.DeepEqual(source.querySchema, originalSchema) || !reflect.DeepEqual(binder.dependencies, originalManifest) {
				t.Fatal("preflight lost source immutability or scalar/RECORD distinction")
			}
			if !reflect.DeepEqual(execution.querySchema, originalSchema) {
				t.Fatal("NULL execution changed analyzed stage schema")
			}
			// Dependency/render walkers must still see the original callable,
			// even though this execution has no invocation/argument demand.
			seen := 0
			queryExpressions(execution, func(call expr.Expr) {
				seen++
				if ref, err := expressionRoutineDescriptor(call); err != nil || ref == nil {
					t.Fatal("lost original callable", ref, err)
				}
			})
			if seen != len(originalCalls) {
				t.Fatal("execution guard hid original graph")
			}
			probe := evaluation
			probe.routineChecks = []expressionDependency{}
			for _, empty := range []bool{true, false} {
				err := validateMarkedQueryJoin(&probe, execution, evaluation.catalog, nil, queryRowMark{}, empty, nil, nil)
				if (err == nil) != (tc.pruned || empty) {
					t.Fatal("execution demand versus original lifetime", empty, tc.pruned, err)
				}
			}
			if tc.pruned {
				// Both admission passes must consult the preflight-derived state.
				// Removing it on a private copy exposes the original missing refs.
				unpruned := *execution
				unpruned.scalarNull = nil
				if err := validateMarkedQueryJoin(&probe, &unpruned, evaluation.catalog, nil, queryRowMark{}, false, nil, nil); err == nil {
					t.Fatal("nonpruned original graph did not demand its manifest")
				}
				cursor, err := execution.openCall(&probe, 0)
				if err != nil || !cursor.scalar.IsNull() || !cursor.initialized {
					t.Fatal("typed NULL source cursor", cursor, err)
				}
				cursor.close()
				if tc.parameter {
					value, err := sqltype.OIDValue(binder.parameters[1].Type, 1247)
					if err != nil {
						t.Fatal(err)
					}
					again := evaluation
					again.Parameters = []sqltype.Value{value}
					replanned, err := preflightFunctionFrom(&again, source)
					if err != nil {
						t.Fatal(err)
					}
					fresh := replanned.(*functionFromNode)
					if fresh.scalarNull != nil && fresh.scalarNull[0] {
						t.Fatal("original source retained a prior NULL frame")
					}
					again.routineChecks = []expressionDependency{}
					if err := validateMarkedQueryJoin(&again, fresh, evaluation.catalog, nil, queryRowMark{}, false, nil, nil); err == nil {
						t.Fatal("non-NULL frame borrowed NULL admission")
					}
				}
			}
			if err := validateMarkedQueryJoin(&evaluation, execution, evaluation.catalog, nil, queryRowMark{}, false, nil, nil); err != nil {
				t.Fatal("intact original manifest", err)
			}
			sequence := catalogRef(t, e, e.CatalogRead(), "strict_null_steps")
			e.mu.Lock()
			called := e.sequences[sequence.Address].called
			e.mu.Unlock()
			if called {
				t.Fatal("planning/admission replayed volatile child")
			}
			ctx, cancel := context.WithCancel(evaluation.Context)
			cancel()
			canceled := evaluation
			canceled.Context = ctx
			if _, err := preflightFunctionFrom(&canceled, source); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation before source pruning", err)
			}
			if !reflect.DeepEqual(source.calls, originalCalls) || !reflect.DeepEqual(source.querySchema, originalSchema) || !reflect.DeepEqual(binder.dependencies, originalManifest) {
				t.Fatal("admission/replanning mutated analyzed source provenance")
			}
		})
	}
}

func TestObjectAddressScalarNullPreflightInfo(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	// Generic strict-scalar policy: a domain-converting sibling cannot veto
	// a NULL proof supplied by a different imported argument. The proof must
	// still refuse to cross the domain conversion itself.
	for _, planner := range []struct {
		name string
		run  func(*EvalContext, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", preflightCoercionsWithPolicy}, {"conditional", simplifyConditional}} {
		t.Run("domain_sibling_"+planner.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "($1::integer)+(NULL::strict_null_checked)::integer")
			evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.parameters[1].Type)}
			if !node.Properties().RuntimeDomainConversion {
				t.Fatal("fixture lost genuine domain-conversion sibling")
			}
			before := expr.InfoOf(node)
			proofs, barriers := 0, 0
			strictRoutineArguments(node, func(argument expr.Expr) {
				known, err := executionKnownNullArgument(&evaluation, argument)
				if err != nil {
					t.Fatal(err)
				}
				if known {
					proofs++
				}
				if argument.Properties().RuntimeDomainConversion {
					barriers++
					if known {
						t.Fatal("NULL proof crossed a domain conversion")
					}
				}
			})
			if proofs != 1 || barriers != 1 {
				t.Fatal("independent NULL proof/domain barrier", proofs, barriers)
			}
			folded, changed, err := planner.run(&evaluation, node, false)
			if err != nil || !changed || !randomLiteralNull(folded) {
				t.Fatal("generic domain-sibling pruning", changed, err)
			}
			value, err := folded.Eval(&evaluation)
			if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
				t.Fatal("generic typed NULL", value, err)
			}
			if ref, err := expressionRoutineDescriptor(folded); err != nil || ref != nil {
				t.Fatal("pruned callable identity", ref, err)
			}
			if folded.Properties() != node.Properties() || !reflect.DeepEqual(before, expr.InfoOf(node)) {
				t.Fatal("generic pruning changed conservative properties/original Info")
			}
		})
	}
	for _, text := range []string{"pg_describe_object(NULL,23,0)", "pg_describe_object($1::oid,23,0)", "pg_describe_object($1::oid,23,(NULL::strict_null_checked)::integer)"} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
		if len(binder.parameters) != 0 {
			evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.parameters[1].Type)}
		}
		before := expr.InfoOf(node)
		folded, changed, err := preflightCoercions(&evaluation, node)
		if err != nil || !changed || !randomLiteralNull(folded) {
			t.Fatal("scalar NULL folding", changed, err)
		}
		if ref, err := expressionRoutineDescriptor(folded); err != nil || ref != nil {
			t.Fatal("folded literal retained executable identity", ref, err)
		}
		if ref, err := expressionRoutineDescriptor(node); err != nil || ref == nil || ref.Address().ObjectID != 3537 {
			t.Fatal("folding erased original identity", ref, err)
		}
		if folded.ResultUsage() != node.ResultUsage() || !reflect.DeepEqual(before, expr.InfoOf(node)) {
			t.Fatal("folding changed original source info")
		}
		value, err := folded.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
			t.Fatal("pruned domain-literal sibling lost typed NULL", value, err)
		}
		raw := statement(t, "SELECT * FROM "+text+" WITH ORDINALITY").(*parser.Select)
		e.mu.Lock()
		sourceBinder := newExpressionBinder(context.Background(), e, scope.input, op, nil)
		sourceBinder.queryAuth = evaluation.routineAuthority
		query, bindErr := sourceBinder.bindSelect(raw, false)
		e.mu.Unlock()
		if bindErr != nil {
			t.Fatal("literal-sibling source binding", bindErr)
		}
		source := query.Root.(*projectNode).child.(*functionFromNode)
		sourceInfo := expr.InfoOf(source.calls[0])
		sourceSchema := append(querySchema(nil), source.querySchema...)
		sourceManifest := append([]expressionDependency(nil), sourceBinder.dependencies...)
		sourceEvaluation := evaluation
		sourceEvaluation.routineChecks = sourceBinder.dependencies
		planned, err := preflightFunctionFrom(&sourceEvaluation, source)
		if err != nil {
			t.Fatal("literal sibling source pruning", err)
		}
		execution := planned.(*functionFromNode)
		if execution.scalarNull == nil || !execution.scalarNull[0] {
			t.Fatal("known NULL source was not pruned")
		}
		cursor, err := execution.openCall(&sourceEvaluation, 0)
		if err != nil || !cursor.scalar.IsNull() || cursor.scalar.Type() != source.calls[0].ResultUsage().Type {
			t.Fatal("literal sibling source typed NULL", cursor, err)
		}
		cursor.close()
		if source.scalarNull != nil || !reflect.DeepEqual(sourceInfo, expr.InfoOf(source.calls[0])) || !reflect.DeepEqual(sourceSchema, execution.querySchema) || !reflect.DeepEqual(sourceManifest, sourceBinder.dependencies) {
			t.Fatal("literal sibling pruning changed original Info/schema/manifest")
		}
	}
	for _, text := range []string{"pg_describe_object($1::oid,23,1/0)"} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
		evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.parameters[1].Type)}
		_, _, err := preflightCoercions(&evaluation, node)
		want := sqlstate.Error{Code: "22012", Message: "division by zero"}
		nullCountUnitDiagnostic(t, err, want)
		for _, suffix := range []string{"", " WHERE FALSE"} {
			raw := statement(t, "SELECT * FROM "+text+suffix).(*parser.Select)
			e.mu.Lock()
			sourceBinder := newExpressionBinder(context.Background(), e, scope.input, op, nil)
			sourceBinder.queryAuth = evaluation.routineAuthority
			query, bindErr := sourceBinder.bindSelect(raw, false)
			e.mu.Unlock()
			if bindErr != nil {
				t.Fatal("source child-error binding", bindErr)
			}
			sourceEvaluation := evaluation
			sourceEvaluation.routineChecks = sourceBinder.dependencies
			_, err = preflightQuery(&sourceEvaluation, query.Root)
			nullCountUnitDiagnostic(t, err, want)
		}
	}
	// Validate a real typed DOMAIN parameter at its input boundary, before
	// outer strict-NULL planning. A literal sibling is not this boundary.
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "pg_describe_object(NULL::oid,23,($1::strict_null_checked)::integer)")
	domain := binder.parameters[1].Type
	if !domain.IsDomain() || !domain.HasDomainChecks() {
		t.Fatal("parameter lost genuine domain input authority")
	}
	before := expr.InfoOf(node)
	manifest := append([]expressionDependency(nil), binder.dependencies...)
	input := evaluation.Input
	input.DomainChecks = &evaluation
	err := domain.CheckDomainInput(evaluation.Context, sqltype.Null(domain), input)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "23514" {
		t.Fatal("typed domain input did not enforce its CHECK before planning", err)
	}
	var violation *sqlstate.Error
	if !errors.As(err, &violation) || violation.Schema != "public" || violation.DataType != "strict_null_checked" || violation.Constraint != "strict_null_present" {
		t.Fatal("typed domain input checked the wrong constraint", err)
	}
	// The raw domain-input seam has no portal Context; no adapter fields are
	// fabricated. A valid import permits ordinary outer-NULL simplification.
	value, err := domain.InputWithEnvironment(evaluation.Context, "7", -1, input)
	if err != nil {
		t.Fatal("valid typed domain import", err)
	}
	evaluation.Parameters = []sqltype.Value{value}
	folded, _, err := preflightCoercions(&evaluation, node)
	if err != nil || !randomLiteralNull(folded) {
		t.Fatal("valid domain import then strict NULL", err)
	}
	result, err := folded.Eval(&evaluation)
	if err != nil || !result.IsNull() || result.Type() != node.ResultUsage().Type {
		t.Fatal("imported-domain sibling result", result, err)
	}
	if !reflect.DeepEqual(before, expr.InfoOf(node)) || !reflect.DeepEqual(manifest, binder.dependencies) {
		t.Fatal("domain import/pruning changed original Info or manifest")
	}
}

// Exercise the existing proven-empty footprint on real analyzed plans. Missing
// original manifests distinguish invocation demand from merely returning no rows.
func TestObjectAddressSourceFalseDemand(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, expression := range []string{
		"pg_identify_object(1247,23,0)",
		"pg_identify_object_as_address(1247,23,0)",
		"pg_get_object_address('type','{integer}','{}')",
		"pg_describe_object(1247,23,0)",
		"pg_identify_object(NULL,nextval('strict_null_steps')::oid,0)",
		"pg_identify_object_as_address(NULL,nextval('strict_null_steps')::oid,0)",
		"pg_get_object_address(NULL,ARRAY[nextval('strict_null_steps')::text],'{}')",
	} {
		for _, route := range []struct {
			name, prefix, suffix string
			pruned               bool
		}{
			{"from_false", "SELECT * FROM ", " WHERE FALSE", true},
			{"from_limit0", "SELECT * FROM ", " LIMIT 0", false},
			{"target_false", "SELECT ", " AS v WHERE FALSE", false},
			{"from_runtime_filter", "SELECT * FROM ", " AS f WHERE ordinal > 99", false},
		} {
			t.Run(expression+"/"+route.name, func(t *testing.T) {
				text := route.prefix + expression + route.suffix
				if route.name == "from_runtime_filter" {
					text = "SELECT * FROM " + expression + " WITH ORDINALITY AS f"
					// The last output is runtime ordinality, not a planning proof.
					if expression == "pg_identify_object(1247,23,0)" || expression == "pg_identify_object(NULL,nextval('strict_null_steps')::oid,0)" {
						text += "(kind,namespace,name,identity,ordinal) WHERE ordinal > 99"
					} else if expression == "pg_describe_object(1247,23,0)" {
						text += "(value,ordinal) WHERE ordinal > 99"
					} else {
						text += "(a,b,c,ordinal) WHERE ordinal > 99"
					}
				}
				raw := statement(t, text).(*parser.Select)
				_, _, evaluation := strictNullUnitBind(t, e, op, scope, "1")
				e.mu.Lock()
				binder := newExpressionBinder(context.Background(), e, scope.input, op, nil)
				binder.queryAuth = evaluation.routineAuthority
				query, err := binder.bindSelect(raw, false)
				e.mu.Unlock()
				if err != nil {
					t.Fatal("actual source/target binding", err)
				}
				manifest := append([]expressionDependency(nil), binder.dependencies...)
				evaluation.routineChecks = binder.dependencies
				root, err := preflightQuery(&evaluation, query.Root)
				if err != nil {
					t.Fatal("child preflight before source pruning", err)
				}
				probe := evaluation
				probe.routineChecks = []expressionDependency{}
				err = validateMarkedQueryJoin(&probe, root, evaluation.catalog, nil, queryRowMark{}, false, nil, nil)
				if (err == nil) != route.pruned {
					t.Fatal("proven source demand", route.name, err)
				}
				if err := validateMarkedQueryJoin(&evaluation, root, evaluation.catalog, nil, queryRowMark{}, false, nil, nil); err != nil {
					t.Fatal("intact original authority", err)
				}
				if !reflect.DeepEqual(binder.dependencies, manifest) {
					t.Fatal("pruning erased original manifest")
				}
				sequence := catalogRef(t, e, e.CatalogRead(), "strict_null_steps")
				e.mu.Lock()
				called := e.sequences[sequence.Address].called
				e.mu.Unlock()
				if called {
					t.Fatal("source-demand proof evaluated volatile data")
				}
			})
		}
	}
}
