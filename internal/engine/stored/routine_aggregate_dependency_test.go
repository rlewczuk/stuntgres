package stored

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A real selected int4pl frame plus frozen aggregate presentation; no SQL
// executor or re-binding participates in these original-manifest invariants.
func routineAggregateCaptureFixture(t *testing.T, mixed, directFirst bool) (catalog.Read, *RoutineProgram, *exec.TransitionAggregate) {
	t.Helper()
	read, presentation, raw, _, aggregate := aggregateRecipeFixture(t)
	integer, _ := read.LookupType(sqltype.Int4)
	support, _ := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 177})
	pg := &catalog.Namespace{ObjectAddress: support.Namespace(), ObjectRevision: 1, ObjectName: "pg_catalog"}
	read.State().SetDescriptor(pg.Address(), pg)
	arithmetic, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, integer, integer, integer)
	if err != nil {
		t.Fatal(err)
	}
	conversion, err := sqltype.PlanCoercion(integer.Usage(), integer.Usage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	parameter := func(n int) expr.Expr {
		return plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: n}
	}
	call := plan.ArithmeticExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: support}, Left: parameter(1), Right: parameter(2), Provider: arithmetic, Operator: sqltype.ArithmeticAdd, Namespace: "pg_catalog"}
	// Selected providers retain the declaration's execution recipe as well as
	// its identities. Remap validates that seed/final mode before copying it.
	provider := &exec.TransitionAggregate{
		Aggregate: aggregate, Support: support,
		Initial: aggregate.Aggregate.Definition.Initial, HasInitial: aggregate.Aggregate.Definition.HasInitial,
		FinalModify: aggregate.Aggregate.Definition.FinalModify,
		StateUsage:  integer.Usage(), Result: integer.Usage(),
		Inputs: []sqltype.Usage{integer.Usage(), integer.Usage()}, Formals: []sqltype.Usage{integer.Usage(), integer.Usage()},
		Conversions: []sqltype.CoercionPlan{conversion, conversion}, Call: call,
	}
	if err := provider.ValidateSelection(); err != nil {
		t.Fatal("invalid original selected-provider fixture", err)
	}
	var literal plan.LiteralExpr
	for _, value := range presentation.literals {
		literal = value
	}
	ref := read.Owner().Domain.Ref(read.Epoch(), aggregate.Address())
	aggregateNode := &plan.AggregateNode{Child: &plan.SingletonNode{}, Calls: []plan.AggregateCall{{Provider: provider, Procedure: aggregate, Ref: ref, Routine: ref, Operation: functions.AggregateTransition, Arguments: []expr.Expr{literal}, IncludeNulls: true}}}
	syntax := &parser.Select{Targets: []parser.Expr{raw}}
	root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: integer.Usage()}}, Child: aggregateNode, Expressions: []expr.Expr{plan.GroupExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{StageDependent: true}}, Frame: 1, Slot: 0}}, Frame: 1, Syntax: syntax}
	presentation.expressionSyntax = nil
	presentation.syntax, presentation.query = syntax, &plan.Query{Root: root}
	presentation.frames = map[uint32]*plan.ProjectNode{1: root}
	presentation.queries = map[*parser.Select]*plan.ProjectNode{syntax: root}
	supportRef := read.Owner().Domain.Ref(read.Epoch(), support.Address())
	supportCapture := catalog.ExpressionDependency{Ref: supportRef, Revision: support.Revision(), Purpose: catalog.AggregateSupportDependency}
	dependencies := []catalog.ExpressionDependency{{Ref: ref, Revision: aggregate.Revision()}, supportCapture}
	if mixed {
		direct := call
		direct.Left, direct.Right = literal, literal
		// AnalysisExpressions retain genuine written calls before stage rewrites.
		root.AnalysisExpressions = []expr.Expr{direct}
		directCapture := supportCapture
		directCapture.Purpose = catalog.DefinitionDependency
		if directFirst {
			dependencies = append([]catalog.ExpressionDependency{directCapture}, dependencies...)
		} else {
			dependencies = append(dependencies, directCapture)
		}
	}
	presentation.dependencies = slices.Clone(dependencies)
	binding := analyze.Binding{Query: presentation.query, Dependencies: slices.Clone(dependencies)}
	action := NewRuleAction(RuleActionParts{Syntax: syntax, Binding: binding, Presentation: presentation})
	return read, NewRoutineProgram([]RuleAction{action}), provider
}

func routineAggregateLiteralFixture(t *testing.T, id sqltype.ID) (catalog.Read, *RoutineProgram, *exec.TransitionAggregate, analyze.ViewLiteralPath) {
	t.Helper()
	read, p, provider := routineAggregateCaptureFixture(t, false, false)
	typ, ok := read.LookupType(id)
	if !ok {
		t.Fatal("missing routine alias type", id)
	}
	value, err := sqltype.RegprocValue(typ, uint32(provider.Support.Address().ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
	presentation := p.actions[0].presentation
	root := presentation.query.Root.(*plan.ProjectNode)
	path := analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral, Target: 1}
	root.Syntax.Targets = append(root.Syntax.Targets, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_catalog.int4pl"}})
	root.Expressions = append(root.Expressions, literal)
	root.QuerySchema = append(root.QuerySchema, catalog.Column{Name: "routine", Usage: typ.Usage()})
	presentation.literals[path] = literal
	direct := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), provider.Support.Address()), Revision: provider.Support.Revision()}
	presentation.dependencies = append(presentation.dependencies, direct)
	p.actions[0].binding.Dependencies = append(p.actions[0].binding.Dependencies, direct)
	return read, NewRoutineProgram(p.actions), provider, path
}

func TestRoutineAggregateDependencyPurpose(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, first := range []bool{false, true} {
			read, p, provider := routineAggregateCaptureFixture(t, mixed, first)
			if err := p.Validate(read); err != nil {
				t.Fatal("valid full capture", mixed, first, err)
			}
			counts := map[catalog.DependencyPurpose]int{}
			for _, dependency := range p.Dependencies() {
				if dependency.Ref.Address == provider.Support.Address() {
					counts[dependency.Purpose]++
				}
			}
			wantDirect := 0
			if mixed {
				wantDirect = 1
			}
			if counts[catalog.AggregateSupportDependency] != 1 || counts[catalog.DefinitionDependency] != wantDirect {
				t.Fatal("full capture lanes", counts)
			}
			d := &catalog.Procedure{SQL: &proc.Definition{}, SQLBody: p}
			directEdges := 0
			catalog.VisitProcedureDefinitionDependencies(read, d, func(address ids.ObjectAddress) bool {
				if address == provider.Support.Address() {
					directEdges++
				}
				return true
			})
			if directEdges != wantDirect {
				t.Fatal("semantic projection flattened support", directEdges)
			}
			duplicated := append(slices.Clone(p.dependencies), p.dependencies...)
			if got := routineDependencySet(duplicated); !slices.Equal(got, p.dependencies) {
				t.Fatal("repetition did not collapse per lane", got)
			}
			// The separate defaults recording pass must not be globally deduplicated.
			if mixed {
				d.SQLDefaults = &RoutineDefaults{dependencies: []catalog.ExpressionDependency{{Ref: read.Owner().Domain.Ref(read.Epoch(), provider.Support.Address()), Revision: provider.Support.Revision()}}}
				n := 0
				catalog.VisitProcedureDefinitionDependencies(read, d, func(a ids.ObjectAddress) bool {
					if a == provider.Support.Address() {
						n++
					}
					return true
				})
				if n != 2 {
					t.Fatal("body/default passes collapsed", n)
				}
			}
		}
	}
	t.Run("column-coverage-is-purpose-local", func(t *testing.T) {
		read, relation, _, _ := routineCaptureFixture(t)
		whole := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Revision: relation.Revision()}
		column := whole
		column.Ref.Address.SubID = 1
		column.Purpose = catalog.AggregateSupportDependency
		if len(routineDependencySet([]catalog.ExpressionDependency{whole, column})) != 2 {
			t.Fatal("support column erased definition relation")
		}
		column.Purpose = catalog.DefinitionDependency
		if got := routineDependencySet([]catalog.ExpressionDependency{whole, column}); len(got) != 1 || got[0] != column {
			t.Fatal("ordinary column coverage lost", got)
		}
	})
	t.Run("historical-stamps-one-semantic-edge", func(t *testing.T) {
		read, p, provider := routineAggregateCaptureFixture(t, true, false)
		original := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), provider.Support.Address()), Revision: provider.Support.Revision()}
		historical := original
		historical.Revision++
		for _, captures := range [][]catalog.ExpressionDependency{{original, historical, original}, {historical, original, historical}} {
			normalized := routineDependencySet(captures)
			if len(normalized) != 2 || normalized[0].Revision == normalized[1].Revision {
				t.Fatal("same-purpose original stamps collapsed", normalized)
			}
			p.dependencies = normalized
			d := &catalog.Procedure{SQL: &proc.Definition{}, SQLBody: p}
			edges := 0
			catalog.VisitProcedureDefinitionDependencies(read, d, func(address ids.ObjectAddress) bool {
				if address == provider.Support.Address() {
					edges++
				}
				return true
			})
			if edges != 1 {
				t.Fatal("historical authority variants duplicated semantic edge", edges)
			}
		}
	})
	t.Run("direct-only", func(t *testing.T) {
		read, mixed, provider := routineAggregateCaptureFixture(t, true, false)
		integer, _ := read.LookupType(sqltype.Int4)
		action := routineLiteralAction(t, integer)
		direct := mixed.actions[0].presentation.query.Root.(*plan.ProjectNode).AnalysisExpressions[0]
		action.presentation.query.Root.(*plan.ProjectNode).AnalysisExpressions = []expr.Expr{direct}
		dependency := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), provider.Support.Address()), Revision: provider.Support.Revision()}
		action.binding.Dependencies = []catalog.ExpressionDependency{dependency}
		action.presentation.dependencies = []catalog.ExpressionDependency{dependency}
		p := NewRoutineProgram([]RuleAction{action})
		if err := p.Validate(read); err != nil {
			t.Fatal("direct-only recipe", err)
		}
		if len(p.dependencies) != 1 || p.dependencies[0] != dependency {
			t.Fatal("direct-only capture acquired support lane", p.dependencies)
		}
	})
}

func TestRoutineAggregateManifestValidation(t *testing.T) {
	t.Run("moving-provider-carriers", testRoutineAggregateMovingCaptures)
	for _, id := range []sqltype.ID{sqltype.Regproc, sqltype.Regprocedure} {
		t.Run(sqltypeNameForAggregateLiteral(id), func(t *testing.T) {
			for _, kind := range []string{"valid", "missing-literal", "extra-literal", "null-literal", "wrong-datum", "wrong-type", "missing-definition", "missing-action-literal", "missing-all-action-literals", "extra-action-literal", "wrong-purpose", "zero-revision", "wrong-revision", "nil-domain", "foreign-domain", "stale-epoch", "wrong-class", "subobject", "mutable-current-revision", "historical-definition-revision"} {
				t.Run(kind, func(t *testing.T) {
					read, p, provider, path := routineAggregateLiteralFixture(t, id)
					if err := p.Validate(read); err != nil {
						t.Fatal("genuine literal control", err)
					}
					presentation := p.actions[0].presentation
					original := slices.Clone(p.dependencies)
					literal := presentation.literals[path]
					switch kind {
					case "missing-all-action-literals":
						p.actionDependencies[0] = slices.DeleteFunc(p.actionDependencies[0], func(dependency catalog.ExpressionDependency) bool {
							return dependency.Ref.Address == provider.Support.Address() && dependency.Purpose == catalog.DefinitionDependency
						})
					case "extra-action-literal":
						for _, dependency := range p.actionDependencies[0] {
							if dependency.Ref.Address == provider.Support.Address() && dependency.Purpose == catalog.DefinitionDependency {
								p.actionDependencies[0] = append(p.actionDependencies[0], dependency)
								break
							}
						}
					case "missing-action-literal":
						for i, dependency := range p.actionDependencies[0] {
							if dependency.Ref.Address == provider.Support.Address() && dependency.Purpose == catalog.DefinitionDependency {
								p.actionDependencies[0] = append(p.actionDependencies[0][:i], p.actionDependencies[0][i+1:]...)
								break
							}
						}
					case "missing-literal":
						delete(presentation.literals, path)
					case "extra-literal":
						extra := path
						extra.Node = 999
						presentation.literals[extra] = literal
					case "null-literal":
						literal.Value = sqltype.Null(literal.Value.Type())
						presentation.literals[path] = literal
					case "wrong-datum":
						value, err := sqltype.RegprocValue(literal.Value.Type(), uint32(provider.Aggregate.Address().ObjectID))
						if err != nil {
							t.Fatal(err)
						}
						literal.Value = value
						presentation.literals[path] = literal
					case "wrong-type":
						integer, _ := read.LookupType(sqltype.Int4)
						literal.Info.Usage = integer.Usage()
						presentation.literals[path] = literal
					case "historical-definition-revision":
						for _, captures := range [][]catalog.ExpressionDependency{presentation.dependencies, p.actions[0].binding.Dependencies} {
							for i := range captures {
								if captures[i].Ref.Address == provider.Support.Address() && captures[i].Purpose == catalog.DefinitionDependency {
									captures[i].Revision += 3
								}
							}
						}
						p = NewRoutineProgram(p.actions)
						original = slices.Clone(p.dependencies)
						current := *provider.Support
						current.ObjectName = "later_renamed_literal_support"
						current.ObjectRevision += 10
						read.State().SetDescriptor(current.Address(), &current)
					case "mutable-current-revision":
						current := *provider.Support
						current.ObjectName = "renamed_literal_support"
						current.ObjectRevision += 10
						read.State().SetDescriptor(current.Address(), &current)
					case "missing-definition":
						presentation.dependencies = presentation.dependencies[:len(presentation.dependencies)-1]
						p.actions[0].binding.Dependencies = p.actions[0].binding.Dependencies[:len(p.actions[0].binding.Dependencies)-1]
						p = NewRoutineProgram(p.actions)
						// Retain the forged lane only in the binding. A datum alone
						// cannot lend it an original presentation stamp.
						p.actions[0].binding.Dependencies = append(p.actions[0].binding.Dependencies, original[len(original)-1])
						p = NewRoutineProgram(p.actions)
					default:
						if kind != "valid" {
							for i := range p.actionDependencies[0] {
								d := &p.actionDependencies[0][i]
								if d.Ref.Address != provider.Support.Address() || d.Purpose != catalog.DefinitionDependency {
									continue
								}
								switch kind {
								case "wrong-purpose":
									d.Purpose = catalog.AggregateSupportDependency
								case "zero-revision":
									d.Revision = 0
								case "wrong-revision":
									d.Revision += 100
								case "nil-domain":
									d.Ref = ids.ObjectRef{Address: d.Ref.Address, Epoch: d.Ref.Epoch}
								case "foreign-domain":
									d.Ref = new(ids.Domain).Ref(d.Ref.Epoch, d.Ref.Address)
								case "stale-epoch":
									d.Ref.Epoch++
								case "wrong-class":
									d.Ref.Address.ClassID = ids.TypeClassID
								case "subobject":
									d.Ref.Address.SubID = 1
								}
							}
						}
					}
					valid := kind == "valid" || kind == "mutable-current-revision" || kind == "historical-definition-revision"
					if err := p.Validate(read); (err == nil) != valid {
						t.Fatal("literal manifest validation", kind, err)
					}
					// Reached authority is an exact triple, not the multiplicity
					// of identical binding/presentation recording occurrences.
					// Full validation and replacement still reject count damage.
					reached := valid || kind == "missing-action-literal" || kind == "extra-action-literal"
					if err := p.ValidateAction(read, 0); (err == nil) != reached {
						t.Fatal("literal reached authority", kind, err)
					}
					if _, err := p.StatementSQL(context.Background(), read, 0); (err == nil) != reached {
						t.Fatal("literal reached rendering", kind, err)
					}
					if _, err := p.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false)); (err == nil) != valid {
						t.Fatal("literal replacement source", kind, err)
					}
					if valid && !slices.Equal(p.dependencies, original) {
						t.Fatal("literal authority refreshed")
					}
				})
			}
		})
	}
	for _, kind := range []string{"missing-support", "missing-support-only", "missing-direct", "unknown-purpose", "forged-origin", "swapped-origin", "extra-lane", "extra-direct-lane", "duplicate-slot", "missing-action", "extra-action", "zero-revision", "wrong-revision", "nil-domain", "foreign-domain", "stale-epoch", "wrong-class", "subobject", "foreign-descriptor"} {
		t.Run(kind, func(t *testing.T) {
			read, p, provider := routineAggregateCaptureFixture(t, kind != "missing-support-only" && kind != "extra-direct-lane", false)
			if err := p.Validate(read); err != nil {
				t.Fatal("valid control", err)
			}
			mutate := func(dependencies []catalog.ExpressionDependency) []catalog.ExpressionDependency {
				next := slices.Clone(dependencies)
				for i := range next {
					d := &next[i]
					if d.Ref.Address != provider.Support.Address() {
						continue
					}
					switch kind {
					case "missing-support", "missing-support-only":
						if d.Purpose == catalog.AggregateSupportDependency {
							next = append(next[:i], next[i+1:]...)
							return next
						}
					case "missing-direct":
						if d.Purpose == catalog.DefinitionDependency {
							next = append(next[:i], next[i+1:]...)
							return next
						}
					case "unknown-purpose":
						d.Purpose = 255
					case "forged-origin":
						d.Purpose = catalog.DefinitionDependency
					case "swapped-origin":
						if d.Purpose == catalog.DefinitionDependency {
							d.Purpose = catalog.AggregateSupportDependency
						} else {
							d.Purpose = catalog.DefinitionDependency
						}
					case "zero-revision":
						d.Revision = 0
					case "wrong-revision":
						d.Revision += 100
					case "nil-domain":
						d.Ref = ids.ObjectRef{Address: d.Ref.Address, Epoch: d.Ref.Epoch}
					case "foreign-domain":
						d.Ref = new(ids.Domain).Ref(read.Epoch(), d.Ref.Address)
					case "stale-epoch":
						d.Ref.Epoch++
					case "wrong-class":
						d.Ref.Address.ClassID = ids.TypeClassID
					case "subobject":
						d.Ref.Address.SubID = 1
					}
				}
				return next
			}
			switch kind {
			case "extra-lane":
				extra := p.dependencies[0]
				extra.Purpose = catalog.AggregateSupportDependency
				p.actions[0].binding.Dependencies = append(p.actions[0].binding.Dependencies, extra)
				p.actions[0].presentation.dependencies = append(p.actions[0].presentation.dependencies, extra)
				rebuilt := NewRoutineProgram(p.actions)
				p.actionDependencies, p.dependencies = rebuilt.actionDependencies, rebuilt.dependencies
			case "extra-direct-lane":
				extra := p.dependencies[1]
				extra.Purpose = catalog.DefinitionDependency
				p.actions[0].binding.Dependencies = append(p.actions[0].binding.Dependencies, extra)
				p.actions[0].presentation.dependencies = append(p.actions[0].presentation.dependencies, extra)
				rebuilt := NewRoutineProgram(p.actions)
				p.actionDependencies, p.dependencies = rebuilt.actionDependencies, rebuilt.dependencies
			case "duplicate-slot":
				p.dependencies = append(p.dependencies, p.dependencies[0])
			case "missing-action":
				p.actionDependencies = nil
			case "extra-action":
				p.actionDependencies = append(p.actionDependencies, nil)
			case "foreign-descriptor":
				foreign := *provider.Support
				other, _, _, _ := routineCaptureFixture(t)
				foreign.Owner = other.Owner().Principals.Principal(10)
				provider.Support = &foreign
			default:
				p.actions[0].binding.Dependencies = mutate(p.actions[0].binding.Dependencies)
				p.actions[0].presentation.dependencies = mutate(p.actions[0].presentation.dependencies)
				if kind == "swapped-origin" { // Preserve a plausible projected set but alter its actual origin stamps.
					p.actions[0].binding.Dependencies[1].Revision++
					p.actions[0].presentation.dependencies[1].Revision++
				}
				rebuilt := NewRoutineProgram(p.actions)
				p.actionDependencies, p.dependencies = rebuilt.actionDependencies, rebuilt.dependencies
			}
			if err := p.Validate(read); err == nil {
				t.Fatal("malformed complete capture admitted")
			}
			if kind != "duplicate-slot" && kind != "extra-action" {
				if _, err := p.StatementSQL(context.Background(), read, 0); err == nil {
					t.Fatal("malformed reached capture rendered")
				}
			}
			if _, err := p.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Fatal("malformed source repaired by replacement")
			}
		})
	}
	t.Run("mutable-current-revision", func(t *testing.T) {
		read, p, provider := routineAggregateCaptureFixture(t, true, false)
		current := *provider.Support
		current.ObjectName = "renamed_support"
		current.ObjectRevision += 10
		current.ObjectNamespace = provider.Aggregate.Namespace()
		current.Owner = read.Owner().Principals.Principal(20)
		current.ACL.Explicit = !current.ACL.Explicit
		read.State().SetDescriptor(current.Address(), &current)
		if err := p.Validate(read); err != nil {
			t.Fatal("mutable revision replaced original authority", err)
		}
		if provider.Support == &current || p.dependencies[1].Revision != 1 {
			t.Fatal("original descriptor/stamp refreshed")
		}
	})
	t.Run("runtime-purpose-selection", func(t *testing.T) {
		read, _, provider := routineAggregateCaptureFixture(t, true, false)
		direct := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), provider.Support.Address()), Revision: provider.Support.Revision()}
		support := direct
		support.Purpose = catalog.AggregateSupportDependency
		for _, aggregate := range []*catalog.Procedure{nil, provider.Aggregate} {
			good, wrong := direct, support
			if aggregate != nil {
				good, wrong = support, direct
			}
			context := &exec.Context{}
			context.RoutineChecks = []catalog.ExpressionDependency{wrong}
			consumed := &exec.RoutineProgram{}
			if err := consumed.RecordActor(context, nil, provider.Support, provider.Support.Owner, aggregate); err == nil {
				t.Fatal("wrong lane supplied runtime authority")
			}
			if len(consumed.Calls) != 0 {
				t.Fatal("failed admission changed consumed graph")
			}
			context.RoutineChecks = []catalog.ExpressionDependency{wrong, good}
			if err := consumed.RecordActor(context, nil, provider.Support, provider.Support.Owner, aggregate); err != nil {
				t.Fatal("valid dual-lane runtime capture rejected", err)
			}
			if len(consumed.Calls) != 1 || consumed.Calls[0].Routine != provider.Support || consumed.Calls[0].SupportAggregate != aggregate {
				t.Fatal("original runtime association lost")
			}
			forged := good
			forged.Revision++
			context.RoutineChecks = []catalog.ExpressionDependency{wrong, forged}
			if err := (&exec.RoutineProgram{}).RecordActor(context, nil, provider.Support, provider.Support.Owner, aggregate); err == nil {
				t.Fatal("wrong-revision lane borrowed sibling stamp")
			}
		}
	})
	t.Run("declaration-default-purpose", func(t *testing.T) {
		for _, purpose := range []catalog.DependencyPurpose{catalog.AggregateSupportDependency, 255} {
			read, _, integer, _ := routineCaptureFixture(t)
			action := routineLiteralAction(t, integer)
			literal := action.presentation.query.Root.(*plan.ProjectNode).Expressions[0]
			defaults, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
			if err != nil {
				t.Fatal("valid default control", err)
			}
			if err := defaults.Validate(read); err != nil {
				t.Fatal("valid default manifest", err)
			}
			defaults.dependencies = append(defaults.dependencies, catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(integer.ID())}), Revision: 1, Purpose: purpose})
			if err := defaults.Validate(read); err == nil {
				t.Fatal("declaration default borrowed support/unknown purpose")
			}
			if _, err := defaults.DefaultSQL(context.Background(), read, 0); err == nil {
				t.Fatal("malformed default rendered")
			}
			if _, err := defaults.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Fatal("malformed default remapped")
			}
		}
	})
}

func TestRoutineAggregatePurposeRemap(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Regproc, sqltype.Regprocedure} {
		t.Run(sqltypeNameForAggregateLiteral(id), func(t *testing.T) {
			read, source, provider, path := routineAggregateLiteralFixture(t, id)
			for _, captures := range [][]catalog.ExpressionDependency{source.actions[0].presentation.dependencies, source.actions[0].binding.Dependencies} {
				for i := range captures {
					if captures[i].Ref.Address == provider.Support.Address() && captures[i].Purpose == catalog.DefinitionDependency {
						captures[i].Revision += 3
					}
				}
			}
			source = NewRoutineProgram(source.actions)
			original := slices.Clone(source.dependencies)
			literal := source.actions[0].presentation.literals[path]
			target, _, _ := routineAggregateCaptureFixture(t, false, false)
			target.Owner().Principals = read.Owner().Principals
			target.State().SetEpoch(read.Epoch() + 7)
			old := provider.Aggregate.Address()
			replacement := *provider.Aggregate
			replacement.ObjectAddress.ObjectID += 10000
			payload := *replacement.Aggregate
			payload.Definition.Function = replacement.Address().ObjectID
			replacement.Aggregate = &payload
			target.State().RemoveDescriptor(old)
			target.State().SetDescriptor(replacement.Address(), &replacement)
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{old: replacement.Address()}
			rebound, err := source.Rebind(target, mapping, NewRoutineRemap(target, mapping, nil, nil, false))
			if err != nil {
				t.Fatal("literal purpose remap", err)
			}
			next := rebound.(*RoutineProgram)
			lanes := map[catalog.DependencyPurpose]int{}
			for _, dependency := range next.dependencies {
				if dependency.Ref.Address == provider.Support.Address() {
					lanes[dependency.Purpose]++
					wantRevision := provider.Support.Revision()
					if dependency.Purpose == catalog.DefinitionDependency {
						wantRevision += 3
					}
					if dependency.Ref.Domain() != target.Owner().Domain || dependency.Ref.Epoch != target.Epoch() || dependency.Revision != wantRevision {
						t.Fatal("literal/support original lifetime remap", dependency)
					}
				}
			}
			if lanes[catalog.DefinitionDependency] != 1 || lanes[catalog.AggregateSupportDependency] != 1 {
				t.Fatal("literal and support lanes collapsed", lanes)
			}
			mapped := next.actions[0].presentation.literals[path]
			oid, err := mapped.Value.ProcedureOID()
			if err != nil || ids.OID(oid) != provider.Support.Address().ObjectID {
				t.Fatal("fixed builtin literal identity lost", oid, err)
			}
			if !slices.Equal(source.dependencies, original) || source.actions[0].presentation.literals[path].Value != literal.Value || source.actions[0].presentation.literals[path].ResultUsage() != literal.ResultUsage() {
				t.Fatal("replacement mutated source literal authority")
			}
			if err := source.Validate(read); err != nil {
				t.Fatal("source invalid after replacement", err)
			}
		})
	}
	for _, first := range []bool{false, true} {
		read, source, provider := routineAggregateCaptureFixture(t, true, first)
		original := slices.Clone(source.dependencies)
		originalAction := slices.Clone(source.actionDependencies[0])
		destination, _, _ := routineAggregateCaptureFixture(t, true, first)
		destination.Owner().Principals = read.Owner().Principals
		destination.State().SetEpoch(read.Epoch() + 7)
		old := provider.Aggregate.Address()
		replacement := *provider.Aggregate
		replacement.ObjectAddress.ObjectID += 10000
		payload := *replacement.Aggregate
		payload.Definition.Function = replacement.Address().ObjectID
		replacement.Aggregate = &payload
		destination.State().RemoveDescriptor(old)
		destination.State().SetDescriptor(replacement.Address(), &replacement)
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{old: replacement.Address()}
		remap := NewRoutineRemap(destination, mapping, nil, nil, false)
		rebound, err := source.Rebind(destination, mapping, remap)
		if err != nil {
			t.Fatal("complete purpose remap", err)
		}
		next := rebound.(*RoutineProgram)
		if err := next.Validate(destination); err != nil {
			t.Fatal(err)
		}
		mappedProvider := next.actions[0].presentation.query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
		if mappedProvider == provider || mappedProvider.Support.Builtin != provider.Support.Builtin || mappedProvider.Support.Revision() != provider.Support.Revision() || mappedProvider.Aggregate.Address() != replacement.Address() {
			t.Fatal("mapped original provider association lost")
		}
		if captured, err := exec.ExpressionRoutineDescriptor(mappedProvider.Call); err != nil || captured != mappedProvider.Support {
			t.Fatal("synthetic provider and selected support diverged", err)
		}
		for _, dependency := range next.dependencies {
			if dependency.Ref.Domain() != destination.Owner().Domain || dependency.Ref.Epoch != destination.Epoch() {
				t.Fatal("unmapped capture lifetime", dependency)
			}
			if dependency.Ref.Address == old {
				t.Fatal("nonidentity aggregate map ignored")
			}
			matched := false
			for _, before := range original {
				a := before.Ref.Address
				if a == old {
					a = replacement.Address()
				}
				matched = matched || a == dependency.Ref.Address && before.Purpose == dependency.Purpose && before.Revision == dependency.Revision
			}
			if !matched {
				t.Fatal("purpose or original stamp refreshed", dependency)
			}
		}
		if !slices.Equal(source.dependencies, original) || !slices.Equal(source.actionDependencies[0], originalAction) || provider.Aggregate.Address() != old {
			t.Fatal("replacement mutated source authority")
		}
		if err := source.Validate(read); err != nil {
			t.Fatal("source no longer valid", err)
		}
	}
	t.Run("reached-action-isolation", func(t *testing.T) {
		read, p, _ := routineAggregateCaptureFixture(t, false, false)
		invalid := p.actions[0]
		invalid.presentation = nil
		p = NewRoutineProgram([]RuleAction{p.actions[0], invalid})
		if err := p.ValidateAction(read, 0); err != nil {
			t.Fatal("dormant invalid action poisoned reached capture", err)
		}
		if err := p.Validate(read); err == nil {
			t.Fatal("full publication accepted dormant corruption")
		}
		if _, err := p.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
			t.Fatal("replacement accepted dormant corruption")
		}
	})
	t.Run("mixed-original-revisions", func(t *testing.T) {
		for _, first := range []bool{false, true} {
			read, p, provider := routineAggregateCaptureFixture(t, true, first)
			direct := *provider.Support
			direct.ObjectRevision++
			read.State().SetDescriptor(direct.Address(), &direct)
			root := p.actions[0].presentation.query.Root.(*plan.ProjectNode)
			call := root.AnalysisExpressions[0].(plan.ArithmeticExpr)
			call.Info.RoutineReference = &direct
			root.AnalysisExpressions[0] = call
			for _, dependencies := range [][]catalog.ExpressionDependency{p.actions[0].binding.Dependencies, p.actions[0].presentation.dependencies} {
				for i := range dependencies {
					if dependencies[i].Ref.Address == direct.Address() && dependencies[i].Purpose == catalog.DefinitionDependency {
						dependencies[i].Revision = direct.Revision()
					}
				}
			}
			p = NewRoutineProgram(p.actions)
			if err := p.Validate(read); err != nil {
				t.Fatal("valid mixed historical stamps rejected", first, err)
			}
			for i := range p.actionDependencies[0] {
				if p.actionDependencies[0][i].Ref.Address == direct.Address() && p.actionDependencies[0][i].Purpose == catalog.AggregateSupportDependency {
					p.actionDependencies[0][i].Revision = direct.Revision()
				}
			}
			if err := p.ValidateAction(read, 0); err == nil {
				t.Fatal("wrong lane stamp authorized historical support")
			}
		}
	})
	t.Run("type-refresh-support-only", func(t *testing.T) {
		for _, mixed := range []bool{false, true} {
			read, p, provider := routineAggregateCaptureFixture(t, mixed, false)
			caller := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 99001}, SQL: &proc.Definition{Standard: true}, SQLBody: p}
			read.State().SetDescriptor(caller.Address(), caller)
			reverse := routineTypeDependents(read.State())
			edges := reverse[provider.Support.Address()]
			if len(edges) != 1 || edges[0].source != caller || edges[0].defaults {
				t.Fatal("runtime support handle lost from refresh propagation", mixed, edges)
			}
			if provider.Aggregate.SQL != nil {
				t.Fatal("fixture does not exercise SQL=nil aggregate gap")
			}
			for _, edge := range edges {
				if edge.source == provider.Aggregate {
					t.Fatal("refresh unexpectedly relies on aggregate SQL bridge")
				}
			}
			if !mixed && catalog.ProcedureDefinitionDependsOn(read, caller, provider.Support.Address()) {
				t.Fatal("refresh index leaked into semantic graph")
			}
		}
	})
}

func sqltypeNameForAggregateLiteral(id sqltype.ID) string {
	if id == sqltype.Regproc {
		return "regproc-literal"
	}
	return "regprocedure-literal"
}
