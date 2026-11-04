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
	"stuntgres/internal/engine/sepgsql"
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
	if err := routine.RebindRoutineDefinitions(read.State(), remap); err != nil {
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

// A->B->C is intentionally nonidempotent. The operation owns its accepted
// definition outputs, not a global exemption for the intermediate type B.
func TestRoutineDefinitionRemapOutputIdentity(t *testing.T) {
	read, relation, integer, _ := routineCaptureFixture(t)
	var types [3]*sqltype.Descriptor
	for i, name := range []string{"remap_source", "remap_destination", "remap_next"} {
		typ, err := sqltype.Domain(sqltype.ID(97000+i), name, name, integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		types[i] = typ
		d := &catalog.Type{SQLType: typ, ObjectNamespace: relation.Namespace(), ObjectRevision: 1}
		read.State().SetDescriptor(d.Address(), d)
	}
	var sources [2]*catalog.Procedure
	for i, name := range []string{"first", "independent_second"} {
		definition := &proc.Definition{Name: name, Result: types[i].Usage()}
		if err := definition.SetFormals([]proc.Formal{
			{Mode: "i", Name: "value", Usage: types[i].Usage()},
			{Mode: "o", Name: "answer", Usage: types[i].Usage()},
		}); err != nil {
			t.Fatal(err)
		}
		signature := catalog.ProcedureSignature("97000")
		if i == 1 {
			signature = "97001"
		}
		p := &catalog.Procedure{
			ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(98000 + i)},
			ObjectNamespace: relation.Namespace(), ObjectName: name, ObjectRevision: 1,
			Owner: read.Owner().Principals.Principal(10), Signature: signature,
			Result: ids.OID(types[i].ID()), SQL: definition,
		}
		sources[i] = p
		read.State().SetDescriptor(p.Address(), p)
	}
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == types[0] {
			return types[1]
		}
		if old == types[1] {
			return types[2]
		}
		return old
	}
	remap := NewRoutineRemap(read, nil, mapType, nil, false)
	if err := routine.RebindRoutineDefinitions(read.State(), remap); err != nil {
		t.Fatal(err)
	}
	for i, source := range sources {
		current, ok := read.Procedure(source.Address())
		if !ok || current == source || current.SQL == source.SQL {
			t.Fatal("missing detached declaration")
		}
		captured, err := remap.Definition(source.SQL)
		if err != nil || captured != current.SQL {
			t.Fatal("original lost operation-owned definition identity", err)
		}
		again, err := remap.Definition(current.SQL)
		if err != nil || again != current.SQL {
			t.Fatal("accepted output was remapped again", err)
		}
		if current.SQL.Result.Type != types[i+1] || current.SQL.Inputs[0].Type != types[i+1] {
			t.Fatal("nonidentity mapping applied zero/two times", i, current.SQL)
		}
		for _, formal := range current.SQL.Formals {
			if formal.Usage.Type != types[i+1] {
				t.Fatal("formal differs from operation-owned result", i, formal)
			}
		}
		if source.SQL.Result.Type != types[i] || source.SQL.Formals[0].Usage.Type != types[i] {
			t.Fatal("operation changed source definition", i)
		}
	}
}

func TestSepgsqlRoutinePayloadRemap(t *testing.T) {
	read, relation, _, _ := routineCaptureFixture(t)
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{Policy: sepgsql.Policy{
		ID: "payload-remap", Version: "1", Contexts: []string{"A", "U"}, InitialContext: "A", UnlabeledContext: "U",
	}})
	if err != nil {
		t.Fatal(err)
	}
	read.Owner().Sepgsql = policy
	var declarations []*catalog.Procedure
	for i, symbol := range []string{"sepgsql_getcon", "sepgsql_setcon", "sepgsql_restorecon", "sepgsql_mcstrans_in", "sepgsql_mcstrans_out"} {
		signature, result, strict, _ := catalog.SepgsqlDeclaration(symbol)
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(98500 + i)}
		capability, err := catalog.NewSepgsqlRoutine(read, policy, symbol, address, nil)
		if err != nil {
			t.Fatal(err)
		}
		declaration := &catalog.Procedure{
			ObjectAddress: address, ObjectNamespace: relation.Namespace(), ObjectName: "renamed_" + symbol, ObjectRevision: 1,
			Owner: read.Owner().Principals.Principal(10), Signature: signature, Result: result, RoutineKind: catalog.FunctionRoutine,
			Volatility: expr.Volatile, Strict: strict, Parallel: 'u', Cost: 1,
			Language: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 13}, Sepgsql: capability,
			Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol, Binary: "$libdir/sepgsql", HasBinary: true},
		}
		read.State().SetDescriptor(address, declaration)
		read.State().BindName(catalog.DescriptorNameKey(declaration), address)
		if err := catalog.ValidateSepgsqlRoutine(read, declaration); err != nil {
			t.Fatal("genuine source declaration", err)
		}
		declarations = append(declarations, declaration)
	}
	for round := range 2 {
		mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
		if round == 1 {
			for _, declaration := range declarations {
				mapped := declaration.Address()
				mapped.ObjectID += 1000
				mapping[declaration.Address()] = mapped
			}
		}
		remap := NewRoutineRemap(read, mapping, nil, nil, false)
		mappedDeclarations := make([]*catalog.Procedure, len(declarations))
		aliases := make([]*catalog.Procedure, len(declarations))
		for i, declaration := range declarations {
			mapped, err := remap.Procedure(declaration)
			if err != nil {
				t.Fatal(err)
			}
			historical := *declaration
			aliases[i], err = remap.Procedure(&historical)
			if err != nil {
				t.Fatal(err)
			}
			read.State().RemoveDescriptor(declaration.Address())
			read.State().UnbindName(catalog.DescriptorNameKey(declaration))
			read.State().SetDescriptor(mapped.Address(), mapped)
			read.State().BindName(catalog.DescriptorNameKey(mapped), mapped.Address())
			mappedDeclarations[i] = mapped
		}
		if err := routine.RebindRoutineDefinitions(read.State(), remap); err != nil {
			t.Fatal("shared declaration stabilization", round, err)
		}
		for i, mapped := range mappedDeclarations {
			original := declarations[i]
			if mapped.SQL != nil || mapped.SQLBody != nil || mapped.SQLDefaults != nil || mapped.Sepgsql == original.Sepgsql ||
				mapped.Sepgsql != aliases[i].Sepgsql || mapped.RoutineDefinition().Kind != proc.NativeBody ||
				mapped.Name() != original.Name() || mapped.Sepgsql.Symbol() != original.Sepgsql.Symbol() {
				t.Fatal("native declaration acquired SQL payload or lost shared capability", round, mapped.Name())
			}
			if err := catalog.ValidateSepgsqlRoutine(read, mapped); err != nil {
				t.Fatal("mapped destination capability", round, mapped.Name(), err)
			}
			if round == 1 {
				foreign := *mapped
				foreign.Sepgsql = original.Sepgsql
				read.State().SetDescriptor(foreign.Address(), &foreign)
				err := catalog.ValidateSepgsqlRoutine(read, &foreign)
				read.State().SetDescriptor(mapped.Address(), mapped)
				if err == nil {
					t.Fatal("old address capability authenticated remapped declaration")
				}
			}
			if original.SQL != nil || original.RoutineDefinition().Kind != proc.NativeBody || original.Address() == mapped.Address() && round == 1 {
				t.Fatal("original declaration changed or address mapping was lost")
			}
		}
		declarations = mappedDeclarations
	}
}
