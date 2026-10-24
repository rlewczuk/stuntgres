package proc

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func foreachUnitDefinition(t *testing.T, source string) *Definition {
	t.Helper()
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	d := &Definition{Kind: PLpgSQLBody, Name: "foreach_unit", Result: types[sqltype.Int4].Usage(), Source: source}
	if err := d.SetFormals([]Formal{{Mode: "i", Name: "a", Usage: types[1007].Usage()}}); err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax = raw
	d.PL, err = CompilePL(d, raw, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "record" {
			return sqltype.Usage{}, true, nil
		}
		id := sqltype.Int4
		if raw.Type.Name == "boolean" || raw.Type.Name == "bool" {
			id = sqltype.Bool
		} else if len(raw.Type.ArrayBounds) != 0 {
			id = 1007
		}
		return types[id].Usage(), false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPLpgSQLForeachCompiler(t *testing.T) {
	const source = `<<s>> DECLARE x integer; y integer; z ALIAS FOR x; r record; BEGIN <<f>> FOREACH s.z,y IN ARRAY a LOOP r.a:=x; CONTINUE f; END LOOP f; CASE 1 WHEN 1 THEN RETURN x; ELSE RETURN 0; END CASE; END s`
	d := foreachUnitDefinition(t, source)
	body := d.PL.Root.Statements[0].Block
	loop := body.Statements[0].Foreach
	if len(d.PL.Variables) != 5 || loop.Targets[0].Slot != body.Bindings["x"] || loop.Targets[1].Slot != body.Bindings["y"] || len(loop.Body.Bindings) != 0 || loop.Body.Statements[1].Transfer != loop.Body.Control {
		t.Fatal("FOREACH created a shadow or lost existing target/control")
	}
	// Five real datums, one list ROW and one distinct r.a precede CASE.
	if body.Statements[1].Case.SearchName != "__Case__Variable_7__" {
		t.Fatal("FOREACH target/source/body diagnostic ledger lost lexical order", body.Statements[1].Case.SearchName)
	}
	original, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(original, d.PLSyntax) {
		t.Fatal("compilation mutated grammar-owned syntax", err)
	}
	for _, source := range []string{
		`BEGIN IF false THEN FOREACH absent IN ARRAY a LOOP END LOOP; END IF; RETURN 1; END`,
		`DECLARE x CONSTANT integer:=1; BEGIN IF false THEN FOREACH x IN ARRAY a LOOP END LOOP; END IF; RETURN 1; END`,
	} {
		raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		bad := &Definition{Name: "bad", Source: source, Result: plUnitInteger(t).Usage()}
		if _, err := CompilePL(bad, raw, false, plUnitCollation(t), plUnitResolver(t, bad.Result)); err == nil {
			t.Fatal("accepted unreachable invalid destination", source)
		}
	}
}

func TestPLpgSQLForeachValidation(t *testing.T) {
	const source = `DECLARE x integer; y integer; spare integer; BEGIN <<f>> FOREACH x,y SLICE 0 IN ARRAY a LOOP CONTINUE f; END LOOP f; RETURN x; END`
	original := foreachUnitDefinition(t, source)
	maximum := foreachUnitDefinition(t, strings.Replace(source, "SLICE 0", "SLICE 2147483647", 1))
	if maximum.PL.Root.Statements[0].Block.Statements[0].Foreach.Slice != 2147483647 {
		t.Fatal("maximum admitted SLICE was not retained")
	}
	for _, name := range []string{"slot", "order", "steps", "slice", "slice_negative", "slice_coordinated_negative", "slice_coordinated_overflow", "specified", "slice_position", "source", "source_clause", "body", "control", "foreign_control", "payload", "span", "missing", "raw_variant", "checked_variant", "raw_subscript", "missing_comma", "comma_span", "shadow_binding", "record_member", "row_member"} {
		t.Run(name, func(t *testing.T) {
			d := foreachUnitDefinition(t, source)
			body := d.PL.Root.Statements[0].Block
			in := &body.Statements[0]
			loop := in.Foreach
			switch name {
			case "slot":
				loop.Targets[0].Slot = d.PL.FoundSlot
			case "order":
				loop.Targets[0], loop.Targets[1] = loop.Targets[1], loop.Targets[0]
			case "steps":
				loop.Targets[0].Steps = []parser.TargetStep{{Field: &parser.Identifier{Text: "f"}}}
			case "slice":
				loop.Slice = 1
			case "slice_negative":
				loop.Slice = -1
			case "slice_coordinated_negative":
				loop.Slice, in.Syntax.Foreach.Slice = -1, -1
			case "slice_coordinated_overflow":
				level := int64(2147483648)
				loop.Slice, in.Syntax.Foreach.Slice = int(level), int(level)
			case "specified":
				loop.SliceSpecified = false
			case "slice_position":
				loop.SlicePos.Character++
			case "source":
				loop.Query = &parser.Select{}
			case "source_clause":
				loop.Query.Where = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}
			case "body":
				loop.Body = body
			case "control":
				loop.Body.Statements[0].Transfer = d.PL.Root.Control
			case "foreign_control":
				loop.Body.Statements[0].Transfer = original.PL.Root.Statements[0].Block.Statements[0].Foreach.Body.Control
			case "payload":
				in.Query = &parser.Select{}
			case "span":
				in.Syntax.Foreach.Source.End.Character++
			case "missing":
				in.Foreach = nil
			case "raw_variant":
				in.Syntax.Loop = in.Syntax.Foreach.Body
			case "checked_variant":
				in.Loop = loop.Body
			case "raw_subscript":
				subscript := parser.ArraySubscript{Upper: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}, Pos: in.Syntax.Foreach.Targets[0].SourcePosition()}
				in.Syntax.Foreach.Targets[0] = parser.SubscriptExpr{Base: in.Syntax.Foreach.Targets[0], Subscripts: []parser.ArraySubscript{subscript}, Pos: subscript.Pos}
				loop.Targets[0].Steps = []parser.TargetStep{{Subscript: &subscript}}
			case "missing_comma":
				in.Syntax.Foreach.TargetCommas = nil
			case "comma_span":
				in.Syntax.Foreach.TargetCommas[0].Offset++
			case "shadow_binding":
				slot := body.Bindings["spare"]
				delete(body.Bindings, "spare")
				body.Bindings["slice"] = slot
				d.PL.Variables[slot].Name = "slice"
				d.PLSyntax.Declarations[2].Name.Text = "slice"
				d.Source = strings.Replace(d.Source, "spare", "slice", 1)
				d.PL.Source = d.Source
			case "record_member":
				slot := body.Bindings["x"]
				d.PL.Variables[slot].Record, d.PL.Variables[slot].Usage = true, sqltype.Usage{}
				d.PLSyntax.Declarations[0].Type.Type.Name = "record"
				d.Source = strings.Replace(d.Source, "integer", "record ", 1)
				d.PL.Source = d.Source
			case "row_member":
				slot := body.Bindings["x"]
				shape, err := sqltype.Composite(99001, "pair", "pair", 99002, []sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: d.PL.Variables[slot].Usage}})
				if err != nil {
					t.Fatal(err)
				}
				d.PL.Variables[slot].Usage = shape.Usage()
				d.PLSyntax.Declarations[0].Type.Type.Name = "pair"
				d.Source = strings.Replace(d.Source, "integer", "pair   ", 1)
				d.PL.Source = d.Source
			}
			if err := d.PL.ValidateDefinition(d); err == nil {
				t.Fatal("accepted forged FOREACH", name)
			}
			if err := original.PL.ValidateDefinition(original); err != nil {
				t.Fatal("mutation damaged original program", err)
			}
		})
	}
}

func TestPLpgSQLForeachDatumDiagnostic(t *testing.T) {
	cause := plError("55000", `record "r" is not assigned yet`)
	datum := &PLDatumAccessError{Err: cause}
	for _, phase := range []error{datum, &PLExpressionPlanningError{Err: datum}, &PLExpressionBindingError{Err: datum}} {
		if !PLExpressionCallback(phase) || plAssignmentExpressionCallback(phase) || !errors.Is(phase, cause) {
			t.Fatal("expression/assignment datum provenance lost its owner or cause", phase)
		}
	}
	// A real outer execution boundary still owns its own callback policy.
	if !PLExpressionCallback(&PLExpressionExecutionError{Err: datum, Simple: false}) ||
		PLExpressionCallback(&PLExpressionExecutionError{Err: datum, Simple: true}) ||
		!plAssignmentExpressionCallback(&PLExpressionExecutionError{Err: datum, Simple: false}) ||
		plAssignmentExpressionCallback(&PLExpressionExecutionError{Err: datum, Simple: true}) {
		t.Fatal("inner datum access overrode the current execution route")
	}
	if !PLExpressionCallback(&PLExpressionPlanningError{Err: &PLExpressionExecutionError{Err: datum, Simple: true}}) ||
		!plAssignmentExpressionCallback(&PLExpressionPlanningError{Err: &PLExpressionExecutionError{Err: datum, Simple: true}}) {
		t.Fatal("inner executed datum access overrode the outer planning callback")
	}
	ordinary := &PLExpressionPlanningError{Err: plError("55000", "unrelated planning failure")}
	if !PLExpressionCallback(ordinary) || !plAssignmentExpressionCallback(ordinary) {
		t.Fatal("datum provenance was inferred from SQLSTATE")
	}
}

func TestPLpgSQLForeachTargetAdmission(t *testing.T) {
	integer := plUnitInteger(t)
	shape, err := sqltype.Composite(99001, "pair", "pair", 99002, []sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "record" {
			return sqltype.Usage{}, true, nil
		}
		if raw.Type.Name == "pair" {
			return shape.Usage(), false, nil
		}
		return plUnitResolver(t, integer.Usage())(raw)
	}
	for _, tc := range []struct {
		declarations, target string
		reject               bool
	}{
		{"x integer; r record;", "r,x", true},
		{"x integer; r record;", "x,r", true},
		{"x integer; r pair;", "r,x", true},
		{"x integer; r pair;", "x,r", true},
		{"x integer; r record;", "r.a,x", false},
		{"x integer; r pair;", "x,r.a", false},
		{"slice integer; x integer;", "x", false},
	} {
		source := "<<slice>> DECLARE " + tc.declarations + " BEGIN IF false THEN FOREACH " + tc.target + " IN ARRAY missing_array LOOP END LOOP; END IF; RETURN 0; END slice"
		raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		d := &Definition{Name: "admission", Source: source, Result: integer.Usage(), Kind: PLpgSQLBody, PLSyntax: raw}
		d.PL, err = CompilePL(d, raw, false, plUnitCollation(t), resolve)
		if (err != nil) != tc.reject {
			t.Fatal("FOREACH scalar-list admission", tc.target, err)
		}
		if err == nil {
			if err := d.PL.ValidateDefinition(d); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, declarations := range []string{
		"slice integer; x integer;",
		"hidden integer; slice ALIAS FOR hidden; x integer;",
	} {
		source := "DECLARE " + declarations + " BEGIN FOREACH x SLICE 1 IN ARRAY missing_array LOOP END LOOP; RETURN 0; END"
		raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		d := &Definition{Name: "shadow", Source: source, Result: integer.Usage()}
		if _, err := CompilePL(d, raw, false, plUnitCollation(t), resolve); err == nil {
			t.Fatal("visible datum failed to shadow the SLICE keyword", declarations)
		}
	}
}
