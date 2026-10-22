package stored

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func parameterCaptureFixture(t *testing.T) plan.SQLRoutineExpr {
	t.Helper()
	read, _, integer, _ := routineCaptureFixture(t)
	literal := func(n int64) expr.Expr {
		value, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}
	}
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 93000}, SQL: &proc.Definition{Result: integer.Usage()}}
	if err := d.SQL.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: integer.Usage()}, {Mode: "i", Name: "b", Usage: integer.Usage(), HasDefault: true}, {Mode: "i", Name: "c", Usage: integer.Usage(), HasDefault: true}}); err != nil {
		t.Fatal(err)
	}
	args := []expr.Expr{literal(2), literal(1), literal(7)}
	d.SQLDefaults = &RoutineDefaults{entries: []expr.Expr{nil, args[2], literal(9)}}
	return plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: d, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: d, Ref: read.Owner().Domain.Ref(read.Epoch(), d.Address()), Args: args, WrittenCount: 2, ArgNames: []parser.Identifier{{Text: "c"}, {Text: "a"}}, InputMap: []plan.RoutineInput{{Start: 1, Count: 1}, {Start: 2, Count: 1}, {Start: 0, Count: 1}}, ChildSlots: []int{2, 0, 1}}
}

func TestSQLRoutineWrittenCapture(t *testing.T) {
	n := parameterCaptureFixture(t)
	if err := n.ValidateArguments(n.Args); err != nil {
		t.Fatal(err)
	}
	capture, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 2 || !reflect.DeepEqual(capture.Args, n.Args[:2]) || len(n.Children()) != 3 {
		t.Fatal("written/default projection", capture, ok, err)
	}
	if _, err := n.Rebuild(n.Info, n.Args); err != nil {
		t.Fatal(err)
	}
	corrupt := n
	corrupt.InputMap = append([]plan.RoutineInput(nil), n.InputMap...)
	corrupt.InputMap[1].Start = 1
	if _, err := corrupt.Rebuild(n.Info, n.Args); err == nil {
		t.Fatal("duplicate child consumption admitted")
	}
	corrupt = n
	corrupt.WrittenCount = 4
	if _, _, err := corrupt.Capture(expr.CaptureFunction); err == nil {
		t.Fatal("out-of-range written projection admitted")
	}
	corrupt = n
	corrupt.ChildSlots = []int{0, 0, 1}
	if _, err := corrupt.Rebuild(n.Info, n.Args); err == nil {
		t.Fatal("inconsistent inverse map admitted")
	}
}

func TestSQLRoutineParameterRemap(t *testing.T) {
	n := parameterCaptureFixture(t)
	callee := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 94000}, SQL: &proc.Definition{Result: n.Usage}}
	hidden := plan.SQLRoutineExpr{Info: expr.Info{Usage: n.Usage, RoutineReference: callee, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: callee, Ref: n.Ref.Domain().Ref(n.Ref.Epoch, callee.Address())}
	n.Args = append([]expr.Expr(nil), n.Args...)
	n.Args[2] = hidden
	d := *n.Procedure
	d.SQLDefaults = &RoutineDefaults{entries: []expr.Expr{nil, hidden, n.Args[0]}}
	n.Procedure, n.Info.RoutineReference = &d, &d
	bad := false
	mapped, err := RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref.Address.ObjectID != 93000 && ref.Address.ObjectID != 94000 {
			bad = true
		}
		ref.Address.ObjectID += 100
		return ref
	}, nil)
	if err != nil || bad {
		t.Fatal("non-idempotent remap", err, bad)
	}
	result := mapped.(plan.SQLRoutineExpr)
	if result.Ref.Address.ObjectID != 93100 || result.Args[2].(plan.SQLRoutineExpr).Ref.Address.ObjectID != 94100 || n.Ref.Address.ObjectID != 93000 || hidden.Ref.Address.ObjectID != 94000 {
		t.Fatal("hidden capture remap/source ownership", result)
	}
	t.Run("formal and default descriptor generations", func(t *testing.T) {
		read, relation, integer, _ := routineCaptureFixture(t)
		domains := make([]*sqltype.Descriptor, 3)
		for i, name := range []string{"old_parameter", "mapped_parameter", "second_parameter"} {
			typ, err := sqltype.Domain(sqltype.ID(95000+i), name, name, integer.Usage())
			if err != nil {
				t.Fatal(err)
			}
			domains[i] = typ
			descriptor := &catalog.Type{SQLType: typ, ObjectNamespace: relation.Namespace(), ObjectRevision: 1}
			read.State().SetDescriptor(descriptor.Address(), descriptor)
		}
		source := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96000}, ObjectNamespace: relation.Namespace(), ObjectName: "typed", Signature: "95000", Result: 95000, SQL: &proc.Definition{Name: "typed", Result: domains[0].Usage()}}
		if err := source.SQL.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: domains[0].Usage(), HasDefault: true}, {Mode: "o", Name: "y", Usage: domains[0].Usage()}}); err != nil {
			t.Fatal(err)
		}
		literal := plan.LiteralExpr{Info: expr.Info{Usage: domains[0].Usage()}, Value: sqltype.Null(domains[0])}
		payload, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
		if err != nil {
			t.Fatal(err)
		}
		source.SQLDefaults = payload
		read.State().SetDescriptor(source.Address(), source)
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{
			{ClassID: ids.TypeClassID, ObjectID: 95000}: {ClassID: ids.TypeClassID, ObjectID: 95001},
			{ClassID: ids.TypeClassID, ObjectID: 95001}: {ClassID: ids.TypeClassID, ObjectID: 95002},
		}
		if err := routine.RebindSQLDefinitions(read, read.State(), mapping); err != nil {
			t.Fatal(err)
		}
		next, ok := read.Procedure(source.Address())
		if !ok {
			t.Fatal("mapped declaration disappeared")
		}
		for _, formal := range next.SQL.Formals {
			if formal.Usage.Type != domains[1] {
				t.Fatal("formal mapped zero or twice", formal)
			}
		}
		node, ok := next.SQLDefaults.Default(0)
		if !ok || next.SQL.Inputs[0].Type != domains[1] || next.SQL.Result.Type != domains[1] || node.ResultUsage().Type != domains[1] || node.(plan.LiteralExpr).Value.Type() != domains[1] {
			t.Fatal("default/result generation mapped zero or twice", next.SQL, node)
		}
		original, _ := source.SQLDefaults.Default(0)
		if source.SQL.Formals[0].Usage.Type != domains[0] || original.ResultUsage().Type != domains[0] || literal.Value.Type() != domains[0] {
			t.Fatal("replacement mutated original formal/default")
		}
		corrupt := *payload
		corrupt.dependencies = append([]catalog.ExpressionDependency(nil), payload.dependencies...)
		corrupt.dependencies[0].Ref.Epoch++
		if _, err := corrupt.Rebind(read, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("foreign default manifest admitted", err)
		}
	})
}

func TestSQLRoutineDefaultAddressRemap(t *testing.T) {
	read, relation, _, _ := routineCaptureFixture(t)
	regclass, _ := read.LookupType(sqltype.Regclass)
	original := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage(), RegclassRef: read.Owner().Domain.Ref(read.Epoch(), relation.Address())}, Value: sqltype.String(regclass, "901")}
	payload, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{original})
	if err != nil {
		t.Fatal(err)
	}
	first, second := *relation, *relation
	first.ObjectAddress.ObjectID, first.ObjectName = 902, "first"
	second.ObjectAddress.ObjectID, second.ObjectName = 903, "second"
	read.State().SetDescriptor(first.Address(), &first)
	read.State().SetDescriptor(second.Address(), &second)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{relation.Address(): first.Address(), first.Address(): second.Address()}
	rebound, err := payload.Rebind(read, mapping)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := rebound.Default(0)
	literal := node.(plan.LiteralExpr)
	text, err := literal.Value.Text()
	if err != nil || text != "902" || literal.RegclassRef.Address != first.Address() || literal.RegclassRef.Domain() != read.Owner().Domain || literal.RegclassRef.Epoch != read.Epoch() {
		t.Fatal("datum address/full reference must map exactly once", literal, text, err)
	}
	sourceText, _ := original.Value.Text()
	if sourceText != "901" || original.RegclassRef.Address != relation.Address() {
		t.Fatal("source datum/reference mutated", original)
	}
	corrupt := *payload
	bad := original
	bad.RegclassRef.Epoch++
	corrupt.entries = []expr.Expr{bad}
	if _, err := corrupt.Rebind(read, mapping); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("real stale reference treated as an address projection", err)
	}
}

func TestSQLRoutineTypeGenerationRefresh(t *testing.T) {
	read, relation, integer, _ := routineCaptureFixture(t)
	old, err := sqltype.Domain(95000, "old_parameter", "capture.old_parameter", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	oldType := &catalog.Type{SQLType: old, ObjectNamespace: relation.Namespace(), ObjectRevision: 1}
	read.State().SetDescriptor(oldType.Address(), oldType)
	source := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96000}, ObjectNamespace: relation.Namespace(), ObjectName: "typed", ObjectRevision: 1, RoutineKind: catalog.FunctionRoutine, Signature: "95000", Result: 95000, SQL: &proc.Definition{Name: "typed", Result: old.Usage()}}
	if err := source.SQL.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: old.Usage(), HasDefault: true}, {Mode: "o", Name: "y", Usage: old.Usage()}}); err != nil {
		t.Fatal(err)
	}
	payload, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: old.Usage()}, Value: sqltype.Null(old)}})
	if err != nil {
		t.Fatal(err)
	}
	source.SQLDefaults = payload
	read.State().SetDescriptor(source.Address(), source)
	action := routineLiteralAction(t, integer)
	program := NewRoutineProgram([]RuleAction{action})
	unchanged := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96001}, SQL: &proc.Definition{Standard: true, Result: integer.Usage(), Statements: []parser.Statement{action.Syntax()}}, SQLBody: program}
	read.State().SetDescriptor(unchanged.Address(), unchanged)
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	syntax := &parser.Select{Targets: []parser.Expr{raw}}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: old.Usage()}, Value: sqltype.Null(old)}
	root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: old.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{literal}, Syntax: syntax, Frame: 1}
	query := &plan.Query{Root: root}
	presentation := NewView(ViewParts{Query: query, Syntax: syntax, Frames: map[uint32]*plan.ProjectNode{1: root}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: root}, RoutineInputs: []sqltype.Usage{}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{{Query: 1, Clause: analyze.ViewTargetLiteral}: literal}})
	dependency := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), oldType.Address()), Revision: oldType.Revision()}
	domainAction := NewRuleAction(RuleActionParts{Syntax: syntax, Binding: analyze.Binding{Query: query, Dependencies: []catalog.ExpressionDependency{dependency}}, Presentation: presentation})
	domainProgram := NewRoutineProgram([]RuleAction{domainAction})
	frozen := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96002}, RoutineKind: catalog.FunctionRoutine, Result: 95000, SQL: &proc.Definition{Standard: true, Result: old.Usage(), Statements: []parser.Statement{syntax}}, SQLBody: domainProgram}
	read.State().SetDescriptor(frozen.Address(), frozen)
	current, err := old.RebindDomain(old.DomainBase(), "renamed_parameter", "capture.renamed_parameter")
	if err != nil {
		t.Fatal(err)
	}
	nextType := *oldType
	nextType.SQLType, nextType.ObjectRevision = current, 2
	read.State().SetDescriptor(nextType.Address(), &nextType)
	if err := ReownExpressions(read, read.State(), nil, true); err != nil {
		t.Fatal(err)
	}
	next, _ := read.Procedure(source.Address())
	defaultNode, _ := next.SQLDefaults.Default(0)
	if next == source || next.SQL == source.SQL || next.SQLDefaults == source.SQLDefaults || next.SQL.Formals[0].Usage.Type != current || next.SQL.Formals[1].Usage.Type != current || next.SQL.Inputs[0].Type != current || next.SQL.Result.Type != current || defaultNode.ResultUsage().Type != current || defaultNode.(plan.LiteralExpr).Value.Type() != current {
		t.Fatal("formal/input/output/result/default generation not refreshed", next)
	}
	if retained, _ := read.Procedure(unchanged.Address()); retained != unchanged || retained.SQLBody != program {
		t.Fatal("unrelated immutable declaration/program identity changed")
	}
	refreshed, _ := read.Procedure(frozen.Address())
	if refreshed == frozen || refreshed.SQL == frozen.SQL || refreshed.SQLBody == domainProgram || refreshed.SQL.Result.Type != current {
		t.Fatal("frozen result/program generation not refreshed")
	}
	rebuilt := refreshed.SQLBody.(*RoutineProgram).Action(0).Binding().Query.Root.(*plan.ProjectNode)
	if rebuilt.Schema()[0].Usage.Type != current || rebuilt.Expressions[0].ResultUsage().Type != current || root.Schema()[0].Usage.Type != old || literal.Value.Type() != old {
		t.Fatal("frozen program generation or source ownership lost")
	}
	if source.SQL.Formals[0].Usage.Type != old || source.SQL.Result.Type != old || payload.entries[0].ResultUsage().Type != old {
		t.Fatal("source declaration/default mutated")
	}
	if err := ReownExpressions(read, read.State(), nil, true); err != nil {
		t.Fatal(err)
	}
	if stable, _ := read.Procedure(source.Address()); stable != next || stable.SQLDefaults != next.SQLDefaults {
		t.Fatal("unchanged refresh replaced declaration/default identity")
	}
	t.Run("declaration versus replacement carriers", func(t *testing.T) {
		read, relation, integer, _ := routineCaptureFixture(t)
		action := routineLiteralAction(t, integer)
		definition := &proc.Definition{Name: "captured", Standard: true, Result: integer.Usage(), Statements: []parser.Statement{action.Syntax()}}
		if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: integer.Usage(), HasDefault: true}}); err != nil {
			t.Fatal(err)
		}
		presentation := *action.presentation
		presentation.routineInputs = definition.Inputs
		action.presentation = &presentation
		literal := action.binding.Query.Root.(*plan.ProjectNode).Expressions[0]
		defaults, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
		if err != nil {
			t.Fatal(err)
		}
		program := NewRoutineProgram([]RuleAction{action})
		replacementDefaults, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
		if err != nil {
			t.Fatal(err)
		}
		source := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 97000}, ObjectRevision: 1, ObjectNamespace: relation.Namespace(), ObjectName: "captured", RoutineKind: catalog.FunctionRoutine, Signature: "23", Result: 23, SQL: definition, SQLDefaults: defaults, SQLBody: program}
		call := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: source}, Procedure: source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Args: []expr.Expr{literal}, WrittenCount: 1, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}}
		inner := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: integer.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{call}}
		nested := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: integer.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{plan.SubqueryExpr{Info: expr.Info{Usage: integer.Usage()}, QueryPlan: &plan.Query{Root: inner}}}}
		for _, tc := range []struct {
			name               string
			change             func(*catalog.Procedure)
			declarationChanged bool
		}{
			{"equivalent COW descriptor", func(*catalog.Procedure) {}, false},
			{"SQL definition", func(d *catalog.Procedure) { copy := *d.SQL; d.SQL = &copy }, true},
			{"checked defaults", func(d *catalog.Procedure) { d.SQLDefaults = replacementDefaults }, true},
			{"frozen body", func(d *catalog.Procedure) { d.SQLBody = NewRoutineProgram([]RuleAction{action}) }, true},
			{"missing descriptor", nil, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				state := catalog.CloneState(read.State())
				if tc.change != nil {
					current := *source
					tc.change(&current)
					state.SetDescriptor(current.Address(), &current)
				}
				view := catalog.NewRead(read.Owner(), state)
				for _, root := range []plan.Node{inner, nested} {
					if analyze.QueryRoutineDeclarationsChanged(view, root) != tc.declarationChanged {
						t.Fatal("ordinary evolution confused descriptor COW with declaration change")
					}
					if !analyze.QueryRoutineCarriersChanged(view, root) {
						t.Fatal("replacement failed to detect the destination carrier")
					}
				}
			})
		}
	})
}

func TestSQLRoutineDomainRecipeTypeGeneration(t *testing.T) {
	for _, kind := range []string{"DEFAULT", "CHECK"} {
		for _, change := range []string{"equivalent COW", "changed frozen body", "changed defaults"} {
			name := kind + "/" + change
			t.Run(name, func(t *testing.T) {
				read, relation, integer, boolean := routineCaptureFixture(t)
				principals := new(authorization.Domain)
				roles := authorization.Seed(principals)
				bootstrap, ok := roles.Read(principals.Principal(authorization.BootstrapPrincipalID)).Lookup("postgres")
				if !ok {
					t.Fatal("missing real seeded bootstrap owner")
				}
				read.Owner().Principals = principals
				database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "capture", Owner: bootstrap.Ref}
				read.State().SetDescriptor(database.Address(), database)
				read.State().BindName(catalog.DescriptorNameKey(database), database.Address())
				if err := (catalog.DatabaseProvider{}).Validate(read, database); err != nil {
					t.Fatal(err)
				}
				action := routineLiteralAction(t, integer)
				definition := &proc.Definition{Name: "value", Standard: true, Result: integer.Usage(), Statements: []parser.Statement{action.Syntax()}}
				if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: integer.Usage(), HasDefault: true}}); err != nil {
					t.Fatal(err)
				}
				presentation := *action.presentation
				presentation.routineInputs = definition.Inputs
				action.presentation = &presentation
				program := NewRoutineProgram([]RuleAction{action})
				literal := action.binding.Query.Root.(*plan.ProjectNode).Expressions[0]
				defaults, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
				if err != nil {
					t.Fatal(err)
				}
				source := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 97100}, ObjectRevision: 1, ObjectNamespace: relation.Namespace(), ObjectName: "value", Signature: "23", RoutineKind: catalog.FunctionRoutine, Result: 23, Volatility: expr.Volatile, SQL: definition, SQLDefaults: defaults, SQLBody: program}
				source.Owner = bootstrap.Ref
				read.State().SetDescriptor(source.Address(), source)
				ref := read.Owner().Domain.Ref(read.Epoch(), source.Address())
				call := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: source, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: source, Ref: ref, Args: []expr.Expr{literal}, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}}
				var recipe expr.Expr = call
				if kind == "CHECK" {
					recipe = plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage(), Property: call.Properties()}, Child: call, Not: true}
				}
				// Obtain genuine checked serializer captures, not fabricated
				// dependency payloads or an executable SQL fixture.
				var refs []ids.ObjectRef
				if _, err := (ScalarRenderer{Dependencies: &refs}).SQL(context.Background(), read, recipe); err != nil {
					t.Fatal(err)
				}
				domain, err := sqltype.Domain(98000, "consumer", "capture.consumer", integer.Usage(), kind == "CHECK")
				if err != nil {
					t.Fatal(err)
				}
				consumer := &catalog.Type{SQLType: domain, ObjectNamespace: relation.Namespace(), ObjectRevision: 1}
				consumer.Owner = bootstrap.Ref
				if kind == "DEFAULT" {
					consumer.DomainDefaultOwn = true
					consumer.DomainDefault = &catalog.ColumnDefault{OID: 98001, Expression: recipe, Dependencies: refs}
				} else {
					dependencies := make([]catalog.ExpressionDependency, len(refs))
					for i, captured := range refs {
						address := captured.Address
						address.SubID = 0
						provider, ok := read.Find(address)
						if !ok {
							t.Fatal("missing actual captured provider", captured)
						}
						dependencies[i] = catalog.ExpressionDependency{Ref: captured, Revision: provider.Revision()}
					}
					consumer.DomainChecks = []catalog.DomainCheckConstraint{{OID: 98001, Name: "retained", Expression: recipe, Dependencies: dependencies}}
				}
				read.State().SetDescriptor(consumer.Address(), consumer)
				state := catalog.CloneState(read.State())
				current := *source
				switch change {
				case "changed frozen body":
					current.SQLBody = NewRoutineProgram([]RuleAction{action})
				case "changed defaults":
					replacement, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
					if err != nil {
						t.Fatal(err)
					}
					current.SQLDefaults = replacement
				}
				state.SetDescriptor(current.Address(), &current)
				view := catalog.NewRead(read.Owner(), state)
				if err := ReownExpressions(view, state, nil, true); err != nil {
					t.Fatal(err)
				}
				next := state.MustType(consumer.Address())
				if change == "equivalent COW" {
					if next != consumer || next.DomainDefault != consumer.DomainDefault || !reflect.DeepEqual(next.DomainChecks, consumer.DomainChecks) {
						t.Fatal("equivalent declaration COW replaced domain owner/recipe")
					}
					return
				}
				if next == consumer {
					t.Fatal("unchanged visible types hid the changed callee payload")
				}
				var nextRecipe expr.Expr
				if kind == "DEFAULT" {
					if next.DomainDefault == consumer.DomainDefault || !reflect.DeepEqual(next.DomainDefault.Dependencies, refs) {
						t.Fatal("default ownership or full references changed incorrectly")
					}
					nextRecipe = next.DomainDefault.Expression
				} else {
					if !reflect.DeepEqual(next.DomainChecks[0].Dependencies, consumer.DomainChecks[0].Dependencies) {
						t.Fatal("CHECK full references changed during ordinary evolution")
					}
					nextRecipe = next.DomainChecks[0].Expression.(plan.NullTestExpr).Child
				}
				bound := nextRecipe.(plan.SQLRoutineExpr)
				if bound.Procedure != &current || bound.RoutineReference != &current || bound.Ref != ref || bound.ResultUsage() != call.ResultUsage() {
					t.Fatal("domain consumer failed coherent callee-only refresh", bound)
				}
				if call.Procedure != source || source.SQLBody != program || source.SQLDefaults != defaults || consumer.DomainDefault != nil && consumer.DomainDefault.Expression.(plan.SQLRoutineExpr).Procedure != source || len(consumer.DomainChecks) != 0 && consumer.DomainChecks[0].Expression.(plan.NullTestExpr).Child.(plan.SQLRoutineExpr).Procedure != source {
					t.Fatal("refresh mutated original owner, recipe or callee")
				}
				if err := ReownExpressions(view, state, nil, true); err != nil {
					t.Fatal(err)
				}
				if state.MustType(consumer.Address()) != next {
					t.Fatal("unchanged second evolution replaced refreshed owner")
				}
			})
		}
	}
}
