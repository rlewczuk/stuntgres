package stored

import (
	"context"
	"strconv"
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

// These are private immutable-payload tests, not public SQL scenarios. Both the
// collation capabilities and the type generations come from the real fixture.
func TestRoutinePayloadUsageRemap(t *testing.T) {
	for _, record := range []bool{false, true} {
		name := "collated_scalar"
		if record {
			name = "anonymous_record"
		}
		t.Run(name, func(t *testing.T) {
			read, source, types, collations := plpgsqlLocalUsageFixture(t)
			leaf := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
			if !leaf.Valid() {
				t.Fatal("invalid source domain usage")
			}
			usage := leaf
			if record {
				shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "value", Usage: leaf}})
				if err != nil {
					t.Fatal(err)
				}
				usage = shape.Usage()
			}
			literal := plan.LiteralExpr{Info: expr.Info{Usage: leaf}, Value: sqltype.Null(leaf.Type)}
			collated := plan.CollationExpr{Info: expr.Info{Usage: leaf}, Child: literal}
			var node expr.Expr = collated
			var raw parser.Expr = parser.CollateExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: collations[0].Name()}}}}
			collationPath := analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}
			path := collationPath
			path.Node = 1
			if record {
				node = plan.RowExpr{Info: expr.Info{Usage: usage}, Fields: []expr.Expr{collated}}
				raw = parser.RowExpr{Fields: []parser.Expr{raw}}
				collationPath.Node = 1
				path.Node = 2
			}
			defaults, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{node})
			if err != nil {
				t.Fatal("source default", err)
			}
			syntax := &parser.Select{Targets: []parser.Expr{raw}}
			root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: usage}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{node}, Syntax: syntax, Frame: 1}
			presentation := NewView(ViewParts{Query: &plan.Query{Root: root}, Syntax: syntax, RoutineInputs: []sqltype.Usage{usage}, Frames: map[uint32]*plan.ProjectNode{1: root}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: root}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{path: literal}, Dependencies: defaults.Dependencies(), Collations: map[analyze.ViewLiteralPath]uint32{collationPath: uint32(collations[0].OID)}})
			action := NewRuleAction(RuleActionParts{Syntax: syntax, Binding: analyze.Binding{Query: &plan.Query{Root: root}, Dependencies: defaults.Dependencies()}, Presentation: presentation})
			body := NewRoutineProgram([]RuleAction{action})
			definition := &proc.Definition{Standard: true, Name: "usage_payload", Result: usage, Statements: []parser.Statement{syntax}}
			if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "value", Usage: usage, HasDefault: true}}); err != nil {
				t.Fatal(err)
			}
			original := *source
			original.ObjectName, original.ObjectRevision = definition.Name, 2
			original.SQL, original.SQLBody, original.SQLDefaults = definition, body, defaults
			original.Signature, original.Result = catalog.ProcedureSignature(strconv.FormatUint(uint64(usage.Type.ID()), 10)), ids.OID(usage.Type.ID())
			original.Body = []catalog.RoutineBodyEdge{{Routine: &original}, {Routine: &original}}
			current := original
			current.ObjectName, current.ObjectRevision = "renamed_usage", 7
			read.State().SetDescriptor(current.Address(), &current)
			if err := defaults.Validate(read); err != nil {
				t.Fatal("source default validation", err)
			}
			if err := body.Validate(read); err != nil {
				t.Fatal("source body validation", err)
			}
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{
				{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[0].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())},
				collations[0].Address(): collations[1].Address(),
			}
			typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == types[0] {
					return types[1]
				}
				return old
			}
			remap := NewRoutineRemap(read, mapping, typ, nil, false)
			if err := routine.RebindRoutineDefinitions(read.State(), remap); err != nil {
				t.Fatal("mapped payload", err)
			}
			captured, err := remap.Procedure(&original)
			if err != nil {
				t.Fatal(err)
			}
			if err := remap.Finish(); err != nil {
				t.Fatal(err)
			}
			published, _ := read.Procedure(current.Address())
			if captured.Revision() != 2 || published.Revision() != 7 || !exec.SameRoutineDeclaration(published, captured) || captured.SQL != published.SQL || captured.SQLBody != published.SQLBody || captured.SQLDefaults != published.SQLDefaults {
				t.Fatal("current and historical declarations lost original shared payload authority")
			}
			wantLeaf := leaf
			wantLeaf.Type = types[1]
			wantLeaf.Collation, err = collations[1].Usage()
			if err != nil {
				t.Fatal(err)
			}
			want := wantLeaf
			if record {
				want = captured.SQL.Result
				if want.Type == usage.Type || want.Type.ID() != 2249 || want.Type.CompositeLen() != 1 || want.Type.CompositeMember(0).Usage != wantLeaf {
					t.Fatal("anonymous occurrence did not retain its mapped logical member")
				}
			}
			mappedDefault, _ := captured.SQLDefaults.Default(0)
			mappedAction := captured.SQLBody.(*RoutineProgram).Action(0)
			mappedRoot := mappedAction.Binding().Query.Root.(*plan.ProjectNode)
			mappedView := mappedAction.presentation
			usages := []sqltype.Usage{captured.SQL.Result, captured.SQL.Formals[0].Usage, captured.SQL.Inputs[0], mappedDefault.ResultUsage(), mappedRoot.Expressions[0].ResultUsage(), mappedRoot.QuerySchema[0].Usage, mappedView.query.Root.(*plan.ProjectNode).Expressions[0].ResultUsage(), mappedView.routineInputs[0]}
			for i, got := range usages {
				if got != want {
					t.Fatalf("payload usage %d = %#v, want one coherent occurrence %#v", i, got, want)
				}
			}
			if mappedView.literals[path].ResultUsage() != wantLeaf || mappedView.collations[collationPath] != uint32(collations[1].OID) {
				t.Fatal("frozen presentation retained source collation identity")
			}
			if captured.Body[0].Routine != captured || captured.Body[1].Routine != captured || original.Body[0].Routine != &original || original.SQL != definition || original.SQLBody != body || original.SQLDefaults != defaults || definition.Result != usage || root.Expressions[0].ResultUsage() != usage || presentation.collations[collationPath] != uint32(collations[0].OID) {
				t.Fatal("mapping mutated source payload/graph or lost shared historical edges")
			}
			if err := captured.SQLDefaults.ValidateDefinition(captured); err != nil {
				t.Fatal("mapped default ABI", err)
			}
		})
	}
}

func TestRoutinePayloadUsageRejectsIncompatibleCollation(t *testing.T) {
	read, source, _, collations := plpgsqlLocalUsageFixture(t)
	usage := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
	if !usage.Valid() {
		t.Fatal("invalid source domain usage")
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: sqltype.Null(usage.Type)}
	defaults, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{literal})
	if err != nil {
		t.Fatal(err)
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{collations[0].Address(): {ClassID: ids.CollationClassID, ObjectID: 950}}
	// There is no declaration/PL transform here to reject on our behalf: the
	// actual default payload must apply the complete operation transformation.
	remap := NewRoutineRemap(read, mapping, func(old *sqltype.Descriptor) *sqltype.Descriptor { return old }, nil, false)
	if _, err := remap.Defaults(defaults); err == nil {
		t.Fatal("incompatible default collation capability accepted")
	}
	if got, _ := defaults.Default(0); got.ResultUsage() != usage {
		t.Fatal("failed mapping mutated source default")
	}
	if current, ok := read.Procedure(source.Address()); !ok || current != source {
		t.Fatal("failed mapping published a routine")
	}
}

func TestRoutinePayloadPLRecordUsageRemap(t *testing.T) {
	read, source, types, collations := plpgsqlLocalUsageFixture(t)
	leaf := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
	if !leaf.Valid() {
		t.Fatal("invalid source domain usage")
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "value", Usage: leaf}})
	if err != nil {
		t.Fatal(err)
	}
	nested, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "nested", Usage: shape.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	definition := *source.SQL
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "value", Usage: nested.Usage()}}); err != nil {
		t.Fatal(err)
	}
	// Slot zero is intentionally the formal here, not the shared fixture's
	// local or the implicit FOUND slot. Compile the copied local consistently.
	definition.Source = "DECLARE x value%TYPE; BEGIN RETURN 1; END"
	definition.PLSyntax, err = parser.ParsePLpgSQL(definition.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	definition.PL, err = proc.CompilePL(&definition, definition.PLSyntax, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "bool" || raw.Type.Name == "boolean" {
			boolean, ok := read.LookupType(sqltype.Bool)
			if !ok {
				t.Fatal("missing implicit FOUND type")
			}
			return boolean.Usage(), false, nil
		}
		t.Fatalf("unexpected declaration type %v", raw)
		return sqltype.Usage{}, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	original := *source
	original.SQL, original.Signature = &definition, "2249"
	current := original
	current.ObjectRevision++
	read.State().SetDescriptor(current.Address(), &current)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[0].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())},
		collations[0].Address(): collations[1].Address(),
	}
	remap := NewRoutineRemap(read, mapping, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == types[0] {
			return types[1]
		}
		return old
	}, nil, false)
	if err := routine.RebindRoutineDefinitions(read.State(), remap); err != nil {
		t.Fatal(err)
	}
	captured, err := remap.Procedure(&original)
	if err != nil {
		t.Fatal(err)
	}
	if err := remap.Finish(); err != nil {
		t.Fatal(err)
	}
	published, _ := read.Procedure(current.Address())
	got := captured.SQL.PL.Variables[0].Usage
	if got.Type == nested || got != captured.SQL.Formals[0].Usage || got != captured.SQL.Inputs[0] || captured.SQL != published.SQL || captured.SQL.PL != published.SQL.PL {
		t.Fatal("PL and declaration lost their shared nested record occurrence")
	}
	mappedLeaf := got.Type.CompositeMember(0).Usage.Type.CompositeMember(0).Usage
	want := leaf
	want.Type = types[1]
	want.Collation, err = collations[1].Usage()
	if err != nil {
		t.Fatal(err)
	}
	if mappedLeaf != want || original.SQL.PL.Variables[0].Usage.Type != nested || nested.CompositeMember(0).Usage.Type != shape || shape.CompositeMember(0).Usage != leaf {
		t.Fatal("nested PL record usage incoherent or source mutated")
	}
}
