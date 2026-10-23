package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRoutineHistoricalPayloadRemap(t *testing.T) {
	read, relation, integer, _ := routineCaptureFixture(t)
	var types [2]*sqltype.Descriptor
	for i, name := range []string{"original_domain", "mapped_domain"} {
		d, err := sqltype.Domain(sqltype.ID(95000+i), name, name, integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		types[i] = d
		read.State().SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.ID())}, &catalog.Type{SQLType: d, ObjectNamespace: relation.Namespace(), ObjectRevision: 1})
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: types[0].Usage()}, Value: sqltype.Null(types[0])}
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	syntax := &parser.Select{Targets: []parser.Expr{raw}}
	root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: types[0].Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{literal}, Syntax: syntax, Frame: 1}
	query := &plan.Query{Root: root}
	dependency := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 95000}), Revision: 1}
	presentation := NewView(ViewParts{Query: query, Syntax: syntax, Frames: map[uint32]*plan.ProjectNode{1: root}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: root}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{{Query: 1, Clause: analyze.ViewTargetLiteral}: literal}})
	action := NewRuleAction(RuleActionParts{Syntax: syntax, Binding: analyze.Binding{Query: query, Dependencies: []catalog.ExpressionDependency{dependency}}, Presentation: presentation})
	body := NewRoutineProgram([]RuleAction{action})
	defaults, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
	if err != nil {
		t.Fatal(err)
	}
	definition := &proc.Definition{Standard: true, Name: "original", Result: types[0].Usage(), Statements: []parser.Statement{syntax}}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "value", Usage: types[0].Usage(), HasDefault: true}, {Mode: "o", Name: "result", Usage: types[0].Usage()}}); err != nil {
		t.Fatal(err)
	}
	original := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96000}, ObjectNamespace: relation.Namespace(), ObjectName: "original", ObjectRevision: 3, RoutineKind: catalog.FunctionRoutine, Signature: "95000", Result: 95000, Owner: read.Owner().Principals.Principal(10), SQL: definition, SQLBody: body, SQLDefaults: defaults}
	original.Body = []catalog.RoutineBodyEdge{{Routine: original}, {Routine: original}}
	current := *original
	current.ObjectName, current.ObjectRevision = "current", 9
	read.State().SetDescriptor(current.Address(), &current)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: 95000}: {ClassID: ids.TypeClassID, ObjectID: 95001}}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == types[0] {
			return types[1]
		}
		return old
	}
	remap := NewRoutineRemap(read, mapping, typ, nil, false)
	if err := routine.RebindSQLDefinitions(read.State(), remap); err != nil {
		t.Fatal(err)
	}
	captured, err := remap.Procedure(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := remap.Finish(); err != nil {
		t.Fatal(err)
	}
	published, _ := read.Procedure(current.Address())
	if captured == original || captured == published || captured.Revision() != 3 || published.Revision() != 9 || captured.Name() != "original" || published.Name() != "current" {
		t.Fatal("historical descriptor replaced by current COW identity")
	}
	if captured.SQL != published.SQL || captured.SQLBody != published.SQLBody || captured.SQLDefaults != published.SQLDefaults || captured.SQL == definition || captured.SQLBody == body || captured.SQLDefaults == defaults {
		t.Fatal("same original payload did not receive one shared mapped identity")
	}
	if !exec.SameRoutineDeclaration(published, captured) || captured.Signature != "95001" || captured.Result != 95001 || captured.SQL.Formals[0].Usage.Type != types[1] || captured.SQL.Formals[1].Usage.Type != types[1] || captured.SQL.Inputs[0].Type != types[1] || captured.SQL.Result.Type != types[1] {
		t.Fatal("mapped original SQL ABI or current association lost", captured)
	}
	if captured.Body[0].Routine != captured || captured.Body[1].Routine != captured || published.Body[0].Routine != captured {
		t.Fatal("historical shared/cyclic body graph was not retained")
	}
	mappedDefault, _ := captured.SQLDefaults.Default(0)
	if mappedDefault.ResultUsage().Type != types[1] || captured.SQLDefaults.Dependencies()[0].Revision != defaults.Dependencies()[0].Revision {
		t.Fatal("typed default or its original hard revision was lost")
	}
	mappedBody := captured.SQLBody.(*RoutineProgram).Action(0).Binding().Query.Root.(*plan.ProjectNode).Expressions[0]
	if mappedBody.ResultUsage().Type != types[1] || !mappedBody.(plan.LiteralExpr).Value.IsNull() {
		t.Fatal("original frozen body did not acquire the mapped domain generation")
	}
	for _, d := range captured.SQLBody.Dependencies() {
		if d.Ref.Address.ClassID == ids.TypeClassID && d.Ref.Address.ObjectID == 95001 && d.Revision != dependency.Revision {
			t.Fatal("body remap refreshed original dependency stamp", d)
		}
	}
	if original.SQL != definition || definition.Formals[0].Usage.Type != types[0] || definition.Result.Type != types[0] || original.SQLBody != body || original.SQLDefaults != defaults || original.Body[0].Routine != original {
		t.Fatal("original declaration graph mutated")
	}
}
