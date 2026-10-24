package proc

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLIntegerForCompiler(t *testing.T) {
	const source = `DECLARE i CONSTANT text:='outer'; r record; BEGIN <<n>> FOR i IN r.lo..r.hi BY r.step LOOP r.item:=i; CONTINUE n; END LOOP n; CASE 1 WHEN 1 THEN RETURN 1; ELSE RETURN 0; END CASE; END`
	d := walkUnitDefinition(t, source)
	body := d.PL.Root.Statements[0].Block
	loop := body.Statements[0].IntegerFor
	outer := body.Bindings["i"]
	if loop.Counter == outer || loop.Name != "i" || len(loop.Body.Bindings) != 1 || loop.Body.Bindings["i"] != loop.Counter || loop.Body.Statements[1].Transfer != loop.Body.Control {
		t.Fatal("private counter/control ownership")
	}
	v := d.PL.Variables[loop.Counter]
	if v.Role != PLIntegerForSlot || v.Constant || v.NotNull || v.Record || v.Cursor != nil || v.InheritInputCollation || v.Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("counter inherited outer constraints", v)
	}
	if slot, ok := d.PL.Lookup([]string{"n", "i"}, []*PLBlock{d.PL.Root, body, loop.Body}); !ok || slot != loop.Counter {
		t.Fatal("label counter binding")
	}
	if slot, ok := d.PL.Lookup([]string{"i"}, []*PLBlock{d.PL.Root, body}); !ok || slot != outer {
		t.Fatal("outer binding lost")
	}
	// Three non-FOUND executable datums plus four fields give CASE datum8.
	if body.Statements[1].Case.SearchName != "__Case__Variable_8__" {
		t.Fatal("outer operand/body diagnostic order", body.Statements[1].Case.SearchName)
	}
	original, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(original, d.PLSyntax) {
		t.Fatal("compiler mutated syntax", err)
	}
	for _, source := range []string{`DECLARE r record; BEGIN FOR r.f1 IN 1..2 LOOP RETURN "r.f1"; END LOOP; RETURN 0; END`, `BEGIN FOR $1 IN 1..2 LOOP RETURN $1; END LOOP; RETURN 0; END`} {
		d := walkUnitDefinition(t, source)
		loop := d.PL.Root.Statements[0].Block.Statements[0].IntegerFor
		if len(loop.Body.Bindings) != 1 || loop.Body.Bindings[loop.Name] != loop.Counter {
			t.Fatal("nonplain private name lost")
		}
	}
	for _, tc := range []struct {
		name, declarations, targets, code, message string
		member                                     int
	}{
		{"unknown_first", "", "a,b", "42601", `"a" is not a known variable`, 0},
		{"unknown_second", "a integer;", "a,b", "42601", `"b" is not a known variable`, 1},
		{"constant_first", "a CONSTANT integer:=1; b integer;", "a,b", "22005", `variable "a" is declared CONSTANT`, 0},
		{"constant_second", "a integer; b CONSTANT integer:=1;", "a,b", "22005", `variable "b" is declared CONSTANT`, 1},
		{"record_first", "r record; b integer;", "r,b", "42601", `syntax error at or near ","`, -1},
		{"record_second", "a integer; r record;", "a,r", "42601", `"r" is not a scalar variable`, 1},
		{"array_scalar", "a integer; b integer[];", "a,b", "42601", "integer FOR loop must have only one target variable", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "BEGIN FOR " + tc.targets + " IN 1..3 LOOP END LOOP; RETURN 1; END"
			if tc.declarations != "" {
				source = "DECLARE " + tc.declarations + " " + source
			}
			raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			integer := plUnitInteger(t)
			d := &Definition{Kind: PLpgSQLBody, Name: "list_unit", Source: source, Result: integer.Usage(), PLSyntax: raw}
			resolve := plUnitResolver(t, integer.Usage())
			_, err = CompilePL(d, raw, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
				if raw.Type.Name == "record" {
					return sqltype.Usage{}, true, nil
				}
				if len(raw.Type.ArrayBounds) != 0 {
					return plUnitBuiltin(t, 1007).Usage(), false, nil
				}
				return resolve(raw)
			})
			var got *sqlstate.Error
			position := raw.Statements[0].IntegerFor.TargetCommas[0]
			if tc.member >= 0 {
				position = raw.Statements[0].IntegerFor.Targets[tc.member].SourcePosition()
			}
			if !errors.As(err, &got) || got.Code != tc.code || got.Message != tc.message || got.Position != strconv.Itoa(position.Character) {
				t.Fatal("list admission diagnostic", err, position)
			}
		})
	}
}

func TestPLpgSQLIntegerForValidation(t *testing.T) {
	const source = `DECLARE i integer:=3; BEGIN <<n>> FOR i IN i..i+2 BY 1 LOOP CONTINUE n; END LOOP n; RETURN i; END`
	original := walkUnitDefinition(t, source)
	for _, name := range []string{"slot", "name", "type", "role", "constant", "notnull", "record", "collation", "cursor", "lower", "upper", "step", "clause", "shared_query", "reverse", "reverse_position", "span", "delimiter", "target", "target_coordinated", "target_position", "target_list", "target_comma", "missing_target", "nil_target", "binding", "extra_binding", "body", "control", "foreign_control", "payload", "mixed_raw", "mixed_checked", "absent"} {
		t.Run(name, func(t *testing.T) {
			d := walkUnitDefinition(t, source)
			in := &d.PL.Root.Statements[0].Block.Statements[0]
			loop, raw := in.IntegerFor, in.Syntax.IntegerFor
			switch name {
			case "slot":
				loop.Counter = d.PL.FoundSlot
			case "name":
				loop.Name = "other"
			case "type":
				d.PL.Variables[loop.Counter].Usage = plUnitBuiltin(t, sqltype.Text).Usage()
			case "role":
				d.PL.Variables[loop.Counter].Role = PLOrdinarySlot
			case "constant":
				d.PL.Variables[loop.Counter].Constant = true
			case "notnull":
				d.PL.Variables[loop.Counter].NotNull = true
			case "record":
				d.PL.Variables[loop.Counter].Record = true
			case "collation":
				d.PL.Variables[loop.Counter].InheritInputCollation = true
			case "cursor":
				d.PL.Variables[loop.Counter].Cursor = &PLCursorRecipe{}
			case "lower":
				loop.Lower = nil
			case "upper":
				raw.Upper = parser.PLExpression{}
			case "step":
				loop.Step = nil
			case "clause":
				loop.Upper.Where = raw.Lower.Expression
			case "shared_query":
				loop.Step = loop.Lower
			case "reverse":
				loop.Reverse = true
			case "reverse_position":
				raw.ReversePos = raw.Lower.Start
			case "span":
				raw.Lower.End.Character++
			case "delimiter":
				raw.Upper.Delimiter = raw.Lower.Delimiter
			case "target":
				raw.Targets[0] = parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "other"}}}, Pos: raw.Targets[0].SourcePosition()}
			case "target_coordinated":
				pos := raw.Targets[0].SourcePosition()
				raw.Targets[0] = parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "other", Pos: pos}}}, Pos: pos}
				delete(loop.Body.Bindings, loop.Name)
				loop.Name = "other"
				d.PL.Variables[loop.Counter].Name = "other"
				loop.Body.Bindings["other"] = loop.Counter
			case "target_position":
				target := raw.Targets[0].(parser.ColumnExpr)
				target.Name.Parts = append([]parser.Identifier(nil), target.Name.Parts...)
				target.Name.Parts[0].Pos.Character++
				raw.Targets[0] = target
			case "target_list":
				raw.Targets = append(raw.Targets, raw.Targets[0])
				raw.TargetCommas = []parser.Position{raw.Lower.Delimiter}
			case "target_comma":
				raw.TargetCommas = []parser.Position{raw.Lower.Delimiter}
			case "missing_target":
				raw.Targets = nil
			case "nil_target":
				raw.Targets[0] = nil
			case "binding":
				loop.Body.Bindings[loop.Name] = d.PL.FoundSlot
			case "extra_binding":
				loop.Body.Bindings["extra"] = loop.Counter
			case "body":
				loop.Body = d.PL.Root
			case "control":
				loop.Body.Control.Loop = false
			case "foreign_control":
				loop.Body.Statements[0].Transfer = original.PL.Root.Statements[0].Block.Statements[0].IntegerFor.Body.Control
			case "payload":
				in.Query = &parser.Select{}
			case "mixed_raw":
				in.Syntax.Loop = raw.Body
			case "mixed_checked":
				in.Loop = loop.Body
			case "absent":
				in.IntegerFor = nil
			}
			if err := d.PL.ValidateDefinition(d); err == nil {
				t.Fatal("accepted forged integer FOR", name)
			}
			if err := original.PL.ValidateDefinition(original); err != nil {
				t.Fatal("mutation damaged independent original", err)
			}
		})
	}
	for _, source := range []string{`BEGIN FOR i IN 1..3 LOOP END LOOP; RETURN 1; END`, `BEGIN FOR i IN REVERSE 3..1 LOOP END LOOP; RETURN 1; END`, `BEGIN FOR i IN 1..3 BY 2 LOOP END LOOP; RETURN 1; END`} {
		d := walkUnitDefinition(t, source)
		if !d.PL.SameLayout(walkUnitDefinition(t, source).PL) {
			t.Fatal("unchanged program layout differs")
		}
		if original.PL.SameLayout(d.PL) {
			t.Fatal("different range layout equivalent")
		}
	}
}
