package stored

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func routineCaptureFixture(t *testing.T) (catalog.Read, *catalog.Relation, *sqltype.Descriptor, *sqltype.Descriptor) {
	t.Helper()
	state := catalog.NewState(1)
	var integer, boolean *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
		switch typ.ID() {
		case sqltype.Int4:
			integer = typ
		case sqltype.Bool:
			boolean = typ
		}
	}
	if integer == nil || boolean == nil {
		t.Fatal("missing builtin fixture types")
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}, ObjectName: "capture", ObjectRevision: 1}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 901}, ObjectNamespace: ns.Address(), ObjectName: "target", ObjectRevision: 1, Columns: []catalog.Column{{Name: "v", Usage: integer.Usage()}}, Attributes: []int32{1}}
	state.SetDescriptor(ns.Address(), ns)
	state.SetDescriptor(relation.Address(), relation)
	return catalog.NewRead(&catalog.Owner{Domain: new(ids.Domain), Principals: new(authorization.Domain)}, state), relation, integer, boolean
}

func routineLiteralAction(t *testing.T, typ *sqltype.Descriptor) RuleAction {
	return routineValueAction(t, typ, 1)
}

func routineValueAction(t *testing.T, typ *sqltype.Descriptor, number int64) RuleAction {
	t.Helper()
	value, err := sqltype.Integer(typ, number)
	if err != nil {
		t.Fatal(err)
	}
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: number}}
	syntax := &parser.Select{Targets: []parser.Expr{raw}}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
	root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: typ.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{literal}, Syntax: syntax, Frame: 1}
	query := &plan.Query{Root: root}
	presentation := NewView(ViewParts{Query: query, Syntax: syntax, Frames: map[uint32]*plan.ProjectNode{1: root}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: root}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{{Query: 1, Clause: analyze.ViewTargetLiteral}: literal}})
	return NewRuleAction(RuleActionParts{Syntax: syntax, Binding: analyze.Binding{Query: query}, Presentation: presentation})
}

func TestRoutineProgramCaptureValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		corrupt  func(catalog.Read, *RoutineProgram, *catalog.Relation)
		replaced bool
	}{
		{"missing-presentation", func(_ catalog.Read, p *RoutineProgram, _ *catalog.Relation) { p.actions[0].presentation = nil }, false},
		{"missing-literal", func(_ catalog.Read, p *RoutineProgram, _ *catalog.Relation) { p.actions[0].presentation.literals = nil }, false},
		{"extra-literal", func(_ catalog.Read, p *RoutineProgram, _ *catalog.Relation) {
			p.actions[0].presentation.literals[analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral, Node: 99}] = p.actions[0].presentation.literals[analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}]
		}, false},
		{"foreign-domain", func(read catalog.Read, p *RoutineProgram, relation *catalog.Relation) {
			p.actionDependencies[0] = []catalog.ExpressionDependency{{Ref: new(ids.Domain).Ref(read.Epoch(), relation.Address())}}
		}, true},
		{"stale-epoch", func(read catalog.Read, p *RoutineProgram, relation *catalog.Relation) {
			p.actionDependencies[0] = []catalog.ExpressionDependency{{Ref: read.Owner().Domain.Ref(read.Epoch()+1, relation.Address())}}
		}, true},
		{"missing-object", func(read catalog.Read, p *RoutineProgram, relation *catalog.Relation) {
			p.actionDependencies[0] = []catalog.ExpressionDependency{{Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address())}}
			read.State().RemoveDescriptor(relation.Address())
		}, true},
		{"vanished-attribute", func(read catalog.Read, p *RoutineProgram, relation *catalog.Relation) {
			address := relation.Address()
			address.SubID = 2
			p.actionDependencies[0] = []catalog.ExpressionDependency{{Ref: read.Owner().Domain.Ref(read.Epoch(), address)}}
		}, true},
		{"missing-modifier", func(_ catalog.Read, p *RoutineProgram, _ *catalog.Relation) {
			p.actions[0].modifierPresentations = map[parser.Statement]*View{&parser.Update{}: nil}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read, relation, integer, _ := routineCaptureFixture(t)
			p := NewRoutineProgram([]RuleAction{routineLiteralAction(t, integer)})
			if err := p.Validate(read); err != nil {
				t.Fatal("valid control", err)
			}
			tc.corrupt(read, p, relation)
			err := p.Validate(read)
			if err == nil || tc.replaced && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatalf("corrupt capture admitted or wrong replacement error: %v", err)
			}
			if _, err := p.StatementSQL(context.Background(), read, 0); err == nil {
				t.Fatal("corrupt program rendered")
			}
		})
	}
	for _, kind := range []string{"missing-dependency", "extra-dependency", "duplicate-dependency"} {
		t.Run(kind, func(t *testing.T) {
			read, _, integer, _ := routineCaptureFixture(t)
			p := NewRoutineProgram([]RuleAction{routineLiteralAction(t, integer)})
			ref := read.Owner().Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(integer.ID())})
			dependency := catalog.ExpressionDependency{Ref: ref, Revision: 1}
			switch kind {
			case "missing-dependency":
				p.actionDependencies[0] = []catalog.ExpressionDependency{dependency}
			case "extra-dependency":
				p.dependencies = []catalog.ExpressionDependency{dependency}
			case "duplicate-dependency":
				p.actionDependencies[0] = []catalog.ExpressionDependency{dependency}
				p.dependencies = []catalog.ExpressionDependency{dependency, dependency}
			}
			if err := p.ValidateAction(read, 0); err != nil {
				t.Fatal("aggregate-only corruption leaked into reached action", err)
			}
			if err := p.Validate(read); err == nil {
				t.Fatal("corrupt whole-program dependency set admitted")
			}
			if _, err := p.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Fatal("corrupt dependency set published by replacement")
			}
		})
	}
	t.Run("definition-association", func(t *testing.T) {
		_, _, integer, _ := routineCaptureFixture(t)
		action := routineLiteralAction(t, integer)
		p := NewRoutineProgram([]RuleAction{action})
		d := &catalog.Procedure{SQL: &proc.Definition{Standard: true, Result: integer.Usage(), Statements: []parser.Statement{action.Syntax()}}, SQLBody: p}
		if err := p.ValidateDefinition(d); err != nil {
			t.Fatal(err)
		}
		for _, statements := range [][]parser.Statement{nil, {action.Syntax(), action.Syntax()}, {&parser.Select{}}} {
			d.SQL.Statements = statements
			if err := p.ValidateDefinition(d); err == nil {
				t.Fatal("missing, extra or foreign command association admitted")
			}
		}
	})
	for _, kind := range []string{"valid", "scan-alias", "definition-alias", "absent-mutation", "opaque-mutation", "missing-returning", "scan-width", "scan-usage", "output-width", "output-usage", "returning-usage", "unsupported-statement", "inconsistent-statement", "missing-modifier", "extra-modifier", "swapped-modifier", "wrong-association", "orphan-scan", "recursive-mutation", "query-mutation"} {
		t.Run("mutation-scan/"+kind, func(t *testing.T) {
			read, p, scan, definition, modifier, boolean := routineMutationCaptureFixture(t)
			if err := p.Validate(read); err != nil {
				t.Fatal("valid mutation RETURNING scan", err)
			}
			if kind == "valid" {
				return
			}
			mutation, _ := analyze.MutationBinding(definition)
			if kind == "scan-alias" || kind == "definition-alias" {
				if kind == "scan-alias" {
					scan.QuerySchema = append(plan.QuerySchema(nil), scan.QuerySchema...)
					scan.QuerySchema[0].Name = "picked"
				} else {
					definition.Schema = append(plan.QuerySchema(nil), definition.Schema...)
					definition.Schema[0].Name = "declared"
					mutation.Returning.Schema = definition.Schema
				}
				if err := p.Validate(read); err != nil {
					t.Fatal("legal mutation output alias", err)
				}
				return
			}
			switch kind {
			case "absent-mutation":
				definition.Mutation = nil
			case "opaque-mutation":
				definition.Mutation = opaqueRoutineMutation{}
			case "missing-returning":
				mutation.Returning, definition.Schema = nil, nil
			case "scan-width":
				scan.QuerySchema = nil
			case "scan-usage":
				scan.QuerySchema = append(plan.QuerySchema(nil), scan.QuerySchema...)
				scan.QuerySchema[0].Usage = boolean.Usage()
			case "output-width":
				definition.Schema = nil
			case "output-usage":
				definition.Schema = append(plan.QuerySchema(nil), definition.Schema...)
				definition.Schema[0].Usage = boolean.Usage()
			case "returning-usage":
				mutation.Returning.Expressions = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}}
			case "unsupported-statement":
				definition.Statement = &parser.Select{}
			case "inconsistent-statement":
				mutation.Update = nil
			case "missing-modifier":
				p.actions[0].modifierPresentations = nil
			case "extra-modifier":
				p.actions[0].modifierPresentations[&parser.Update{}] = modifier
			case "swapped-modifier":
				other := &parser.Update{}
				copy := *definition
				copy.Statement = other
				p.actions[0].binding.CTEs = append(p.actions[0].binding.CTEs, &copy)
				p.actions[0].modifierPresentations[other] = modifier.With(func(parts *ViewParts) { parts.ActionSyntax = other })
				p.actions[0].modifierPresentations[definition.Statement], p.actions[0].modifierPresentations[other] = p.actions[0].modifierPresentations[other], modifier
			case "wrong-association":
				modifier.actionSyntax = &parser.Update{}
			case "orphan-scan":
				copy := *definition
				copy.Statement = &parser.Update{}
				scan.CTE = &copy
			case "recursive-mutation":
				definition.Recursive = true
			case "query-mutation":
				definition.Query = p.actions[0].binding.Query
			}
			if err := p.Validate(read); err == nil {
				t.Fatal("corrupt mutation scan admitted")
			}
			if _, err := p.StatementSQL(context.Background(), read, 0); err == nil {
				t.Fatal("corrupt mutation scan rendered")
			}
			if _, err := p.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Fatal("corrupt mutation scan published by replacement")
			}
		})
	}
}

func TestRoutineActionValidationBoundary(t *testing.T) {
	read, _, integer, _ := routineCaptureFixture(t)
	valid, invalid := routineLiteralAction(t, integer), routineLiteralAction(t, integer)
	invalid.presentation.literals = nil
	for _, actions := range [][]RuleAction{{valid, invalid}, {invalid, valid}} {
		p := NewRoutineProgram(actions)
		for i, action := range actions {
			err := p.ValidateAction(read, i)
			if (err == nil) != (action.presentation == valid.presentation) {
				t.Fatalf("action %d validation leaked across command boundary: %v", i, err)
			}
			if action.presentation == valid.presentation {
				if text, err := p.StatementSQL(context.Background(), read, i); err != nil || text == "" {
					t.Fatal("valid reached command failed rendering", text, err)
				}
			}
		}
		if err := p.Validate(read); err == nil {
			t.Fatal("whole-program publication admitted unrelated invalid command")
		}
		for _, index := range []int{-1, 2} {
			if err := p.ValidateAction(read, index); err == nil {
				t.Fatal("invalid ordinal admitted", index)
			}
			if _, err := p.StatementSQL(context.Background(), read, index); err == nil {
				t.Fatal("invalid ordinal rendered", index)
			}
		}
	}
}

func routineMergeCaptureFixture(t *testing.T) (catalog.Read, *RoutineProgram, *catalog.Procedure) {
	t.Helper()
	read, relation, integer, boolean := routineCaptureFixture(t)
	callee := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 902}, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, ObjectName: "dormant", RoutineKind: catalog.FunctionRoutine, Result: ids.OID(integer.ID()), Volatility: expr.Volatile, Parallel: 'u', Cost: 100, SQL: &proc.Definition{Result: integer.Usage(), Source: "SELECT 1"}}
	callee.Owner = read.Owner().Principals.Principal(10)
	read.State().SetDescriptor(callee.Address(), callee)
	ref := read.Owner().Domain.Ref(read.Epoch(), callee.Address())
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: callee, Property: expr.Properties{Volatility: expr.Volatile}}, Ref: ref, Procedure: callee}
	call.Signature = proc.InvocationSignature{Inputs: callee.SQL.Inputs, Result: integer.Usage()}
	condition := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}
	syntax := &parser.Merge{Arms: []parser.MergeArm{{Match: parser.MergeMatched, Action: parser.MergeUpdateAction}}}
	binding := analyze.Binding{Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Relation: relation, Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: 1}}, Merge: &analyze.MergeBinding{Source: &plan.SingletonNode{}, On: condition, Arms: []analyze.MergeArm{{Match: parser.MergeMatched, Action: parser.MergeUpdateAction, Condition: condition, Input: analyze.Binding{Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Relation: relation, Targets: []int{0}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{call}}}}}}}
	// The synthetic presentation owns the false predicate and the selected call
	// in the unreachable UPDATE action, just like FreezeRoutineAction.
	querySyntax := &parser.Select{}
	root := &plan.ProjectNode{Child: &plan.SingletonNode{QuerySchema: relation.Columns}, Expressions: []expr.Expr{condition, condition, call}, Syntax: querySyntax, Frame: 1}
	presentation := NewView(ViewParts{Query: &plan.Query{Root: root}, Syntax: querySyntax, Dependencies: binding.Dependencies, Frames: map[uint32]*plan.ProjectNode{1: root}, Queries: map[*parser.Select]*plan.ProjectNode{querySyntax: root}})
	return read, NewRoutineProgram([]RuleAction{NewRuleAction(RuleActionParts{Syntax: syntax, Binding: binding, Presentation: presentation})}), callee
}

func TestRoutineMergeCaptureValidation(t *testing.T) {
	for _, kind := range []string{"missing-arm", "extra-arm", "wrong-arm", "missing-call", "foreign-call", "stale-call-epoch", "stale-callee", "missing-callee", "missing-scalar", "extra-scalar", "extra-capture"} {
		t.Run(kind, func(t *testing.T) {
			read, p, callee := routineMergeCaptureFixture(t)
			if err := p.Validate(read); err != nil {
				t.Fatal("valid unreachable-arm control", err)
			}
			action := &p.actions[0]
			root := action.presentation.query.Root.(*plan.ProjectNode)
			call := root.Expressions[2].(plan.SQLRoutineExpr)
			switch kind {
			case "missing-arm":
				action.binding.Merge.Arms = nil
			case "extra-arm":
				action.binding.Merge.Arms = append(action.binding.Merge.Arms, action.binding.Merge.Arms[0])
			case "wrong-arm":
				action.binding.Merge.Arms[0].Action = parser.MergeDeleteAction
			case "missing-call":
				call.Procedure = nil
			case "foreign-call":
				call.Ref = new(ids.Domain).Ref(read.Epoch(), callee.Address())
			case "stale-call-epoch":
				call.Ref = read.Owner().Domain.Ref(read.Epoch()+1, callee.Address())
			case "stale-callee":
				copy := *callee
				definition := *callee.SQL
				definition.Source = "SELECT 2"
				copy.SQL = &definition
				read.State().SetDescriptor(copy.Address(), &copy)
			case "missing-callee":
				read.State().RemoveDescriptor(callee.Address())
			case "missing-scalar":
				root.Expressions = root.Expressions[:2]
			case "extra-scalar":
				root.Expressions = append(root.Expressions, call)
			case "extra-capture":
				action.presentation.routineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{{Query: 1, Clause: analyze.ViewTargetLiteral, Node: 99}: call.Ref}
			}
			if len(root.Expressions) > 2 {
				root.Expressions[2] = call
			}
			if err := p.Validate(read); err == nil {
				t.Fatal("unexecuted-arm corrupt capture admitted")
			}
			if _, err := p.StatementSQL(context.Background(), read, 0); err == nil {
				t.Fatal("unexecuted-arm corrupt capture rendered")
			}
			if _, err := p.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Fatal("unexecuted-arm corrupt capture published by replacement")
			}
		})
	}
	t.Run("replacement", func(t *testing.T) {
		read, p, callee := routineMergeCaptureFixture(t)
		copy := *callee
		copy.ObjectAddress.ObjectID = 903
		read.State().SetDescriptor(copy.Address(), &copy)
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{callee.Address(): copy.Address()}
		rebound, err := p.Rebind(read, mapping, NewRoutineRemap(read, mapping, nil, nil, false))
		if err != nil {
			t.Fatal(err)
		}
		next := rebound.(*RoutineProgram)
		call := next.Action(0).Binding().Merge.Arms[0].Input.Update.Assignments[0].(plan.SQLRoutineExpr)
		if call.Ref.Address != copy.Address() || call.Procedure.Address() != copy.Address() || call.Procedure.SQL != callee.SQL || call.Procedure.Revision() != callee.Revision() {
			t.Fatal("unexecuted-arm selected callee not remapped", call.Ref)
		}
		original := p.Action(0).Binding().Merge.Arms[0].Input.Update.Assignments[0].(plan.SQLRoutineExpr)
		if original.Ref.Address != callee.Address() || original.Procedure != callee {
			t.Fatal("replacement mutated original capture")
		}
	})
}

type opaqueRoutineMutation struct{}

func (opaqueRoutineMutation) CTEMutationPayload() {}

func routineMutationCaptureFixture(t *testing.T) (catalog.Read, *RoutineProgram, *plan.DerivedNode, *plan.CTEDefinition, *View, *sqltype.Descriptor) {
	t.Helper()
	read, relation, integer, boolean := routineCaptureFixture(t)
	action := routineLiteralAction(t, integer)
	project := action.presentation.query.Root.(*plan.ProjectNode)
	literal := project.Expressions[0]
	statement := &parser.Update{}
	mutation := &analyze.Binding{
		Relation: relation, Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()),
		Targets: []int{0}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{literal}},
		Returning: &analyze.Returning{Schema: relation.Columns, Expressions: []expr.Expr{literal}},
	}
	definition := &plan.CTEDefinition{Statement: statement, Mutation: mutation, Schema: relation.Columns, Name: "m"}
	table := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "m"}}}}
	scan := &plan.DerivedNode{QuerySchema: relation.Columns, CTE: definition, Table: table, Ordinal: -1, ScanFrame: 2}
	project.Child, project.Frame, project.Syntax.From = scan, 2, table
	action.presentation.frames = map[uint32]*plan.ProjectNode{2: project}
	action.presentation.literals = map[analyze.ViewLiteralPath]plan.LiteralExpr{{Query: 2, Clause: analyze.ViewTargetLiteral}: literal.(plan.LiteralExpr)}
	action.binding.CTEs = []*plan.CTEDefinition{definition}
	syntax := &parser.Select{}
	modifierRoot := &plan.ProjectNode{Child: &plan.SingletonNode{QuerySchema: relation.Columns}, Expressions: []expr.Expr{literal, literal}, Syntax: syntax, Frame: 1}
	modifier := NewView(ViewParts{
		Query: &plan.Query{Root: modifierRoot}, Syntax: syntax, ActionSyntax: statement, MutationFrame: true,
		Frames: map[uint32]*plan.ProjectNode{1: modifierRoot}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: modifierRoot},
	})
	action.modifierPresentations = map[parser.Statement]*View{statement: modifier}
	return read, NewRoutineProgram([]RuleAction{action}), scan, definition, modifier, boolean
}
