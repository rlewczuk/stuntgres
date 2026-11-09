package proc

import (
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func returnNextUnit(t *testing.T, source string, set bool) (*Definition, error) {
	t.Helper()
	row, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: plUnitInteger(t).Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	d := &Definition{Kind: PLpgSQLBody, Name: "next_unit", Result: row.Usage(), Source: source, ReturnsSet: set}
	d.PLSyntax, err = parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		return d, err
	}
	resolve := plUnitResolver(t, row.Usage())
	d.PL, err = CompilePL(d, d.PLSyntax, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "record" {
			return sqltype.Usage{}, true, nil
		}
		return resolve(raw)
	})
	return d, err
}

func TestPLpgSQLReturnNextValidation(t *testing.T) {
	const source = "<<s>> DECLARE r row_type; a ALIAS FOR r; BEGIN RETURN NEXT s.a; RETURN; END s"
	d, err := returnNextUnit(t, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "slot", "variant", "query", "span", "operand", "set", "extra"} {
		t.Run(name, func(t *testing.T) {
			d, err := returnNextUnit(t, source, true)
			if err != nil {
				t.Fatal(err)
			}
			in := &d.PL.Root.Statements[0].Block.Statements[0]
			switch name {
			case "missing":
				in.ReturnNext = nil
			case "slot":
				in.ReturnNext.Slot = d.PL.FoundSlot
			case "variant":
				in.Syntax.Return = true
			case "query":
				in.Query = &parser.Select{}
			case "span":
				in.Syntax.ReturnNext.Start.Character++
			case "operand":
				in.Syntax.ReturnNext.Expression = parser.LiteralExpr{}
			case "set":
				d.ReturnsSet = false
			case "extra":
				in.Target = PLTarget{Slot: 1}
			}
			if d.PL.ValidateDefinition(d) == nil {
				t.Fatal("accepted forged RETURN NEXT", name)
			}
		})
	}
	for _, mutate := range []func(*PLInstruction){
		func(in *PLInstruction) { in.Block = &PLBlock{} },
		func(in *PLInstruction) { in.If = &PLConditional{} },
		func(in *PLInstruction) { in.Case = &PLConditional{} },
		func(in *PLInstruction) { in.Loop = &PLBlock{} },
		func(in *PLInstruction) { in.While = &PLWhile{} },
		func(in *PLInstruction) { in.Transfer = &PLControl{} },
		func(in *PLInstruction) { in.Cursor = &PLCursorInstruction{} },
		func(in *PLInstruction) { in.Dynamic = &PLDynamic{} },
		func(in *PLInstruction) { in.Diagnostics = &PLDiagnostics{} },
		func(in *PLInstruction) { in.SQL = &PLSQL{} },
		func(in *PLInstruction) { in.Into = &PLInto{} },
		func(in *PLInstruction) { in.IntegerFor = &PLIntegerFor{} },
		func(in *PLInstruction) { in.Foreach = &PLForeach{} },
		func(in *PLInstruction) { in.QueryLoop = &PLQueryLoop{} },
		func(in *PLInstruction) { in.Raise = &PLRaise{} },
		func(in *PLInstruction) { in.Assert = &PLAssert{} },
		func(in *PLInstruction) { in.Transaction = &parser.PLTransaction{} },
	} {
		d, err := returnNextUnit(t, source, true)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&d.PL.Root.Statements[0].Block.Statements[0])
		if d.PL.ValidateDefinition(d) == nil {
			t.Fatal("accepted additional checked payload")
		}
	}
	for _, body := range []string{
		"DECLARE r row_type; BEGIN RETURN; RETURN NEXT r; END",
		"DECLARE r row_type; BEGIN IF false THEN RETURN NEXT r; END IF; END",
		"DECLARE r row_type; BEGIN LOOP RETURN NEXT r; END LOOP; END",
		"DECLARE r row_type; BEGIN BEGIN RETURN; EXCEPTION WHEN OTHERS THEN RETURN NEXT r; END; END",
	} {
		if _, err := returnNextUnit(t, body, false); err == nil {
			t.Fatal("accepted unreachable nonset NEXT", body)
		}
	}
	for _, body := range []string{
		"BEGIN RETURN NEXT 1; END",
		"DECLARE r row_type; BEGIN RETURN; RETURN NEXT missing; END",
		"DECLARE r row_type; BEGIN IF false THEN RETURN NEXT r.id; END IF; END",
	} {
		if _, err := returnNextUnit(t, body, true); err == nil {
			t.Fatal("accepted invalid read", body)
		}
	}
	for _, body := range []string{
		"DECLARE r CONSTANT row_type := NULL; BEGIN RETURN NEXT r; END",
		source,
	} {
		d, err := returnNextUnit(t, body, true)
		if err != nil {
			t.Fatal(err)
		}
		if err = d.PL.ValidateDefinition(d); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPLpgSQLReturnNextOutputAdmission(t *testing.T) {
	compile := func(source, mode string) (*Definition, error) {
		t.Helper()
		d, err := returnNextUnit(t, "BEGIN RETURN; END", true)
		if err != nil {
			t.Fatal(err)
		}
		if err = d.SetFormals([]Formal{{Mode: mode, Name: "id", Usage: plUnitInteger(t).Usage()}}); err != nil {
			t.Fatal(err)
		}
		d.Source = source
		d.PLSyntax, err = parser.ParsePLpgSQL(source, PLReturnPolicy(d.Result, len(d.OutputFormals), false))
		if err != nil {
			t.Fatal(err)
		}
		d.PL, err = CompilePL(d, d.PLSyntax, false, plUnitCollation(t), plUnitResolver(t, d.Result))
		return d, err
	}
	for _, source := range []string{
		"DECLARE r row_type; BEGIN RETURN NEXT r; END",
		"DECLARE r row_type; BEGIN RETURN; RETURN NEXT r; END",
		"DECLARE r row_type; BEGIN IF false THEN RETURN NEXT r; END IF; END",
	} {
		t.Run(source, func(t *testing.T) {
			if _, err := compile(source, "o"); err == nil {
				t.Fatal("accepted explicit NEXT with output formal")
			}
		})
	}
	t.Run("checked_output_projection", func(t *testing.T) {
		// Changing only the formal mode preserves the checked slots, bindings,
		// usages and source. The matching INOUT control is valid.
		control, err := compile("DECLARE r row_type; BEGIN RETURN; END", "b")
		if err != nil {
			t.Fatal(err)
		}
		if err = control.PL.ValidateDefinition(control); err != nil {
			t.Fatal("invalid output-formal control", err)
		}
		d, err := compile("DECLARE r row_type; BEGIN RETURN NEXT r; END", "i")
		if err != nil {
			t.Fatal(err)
		}
		if err = d.PL.ValidateDefinition(d); err != nil {
			t.Fatal("invalid input-formal control", err)
		}
		inputs := append([]sqltype.Usage(nil), d.Inputs...)
		names := append([]string(nil), d.Names...)
		inputFormals := append([]int(nil), d.InputFormals...)
		formalInputs := append([]int(nil), d.FormalInputs...)
		formal := d.Formals[0]
		formal.Mode = "b"
		if err = d.SetFormals([]Formal{formal}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(d.Inputs, inputs) || !reflect.DeepEqual(d.Names, names) || !reflect.DeepEqual(d.InputFormals, inputFormals) || !reflect.DeepEqual(d.FormalInputs, formalInputs) || d.Formals[0].Usage != d.PL.Variables[0].Usage || !reflect.DeepEqual(d.OutputFormals, []int{0}) {
			t.Fatal("mode mutation changed input layout or checked formal usage")
		}
		if err = d.PL.ValidateDefinition(d); err == nil {
			t.Fatal("accepted checked NEXT with output formal")
		}
	})
}

func TestPLRunReturnNextSnapshots(t *testing.T) {
	d, err := returnNextUnit(t, "DECLARE r row_type; BEGIN RETURN NEXT r; END", true)
	if err != nil {
		t.Fatal(err)
	}
	in := d.PL.Root.Statements[0].Block.Statements[0]
	host := newWalkHost(t)
	frame := NewPLFrame(d.PL, d, &Frame{}, sqltype.CollationRef{})
	frame.SetFound(true)
	frame.SetRowCount(7)
	integer := d.Result.Type.CompositeMember(0).Usage.Type
	one, _ := sqltype.Integer(integer, 1)
	row, err := sqltype.CompositeValue(d.Result.Type, []sqltype.Value{one})
	if err != nil {
		t.Fatal(err)
	}
	frame.Values[in.ReturnNext.Slot] = row
	var values []sqltype.Value
	run := &PLRun{Host: host, Definition: d, Frame: frame, Values: &values}
	before := append([]sqltype.Value(nil), frame.Values...)
	if _, flow, err := run.Statements([]PLInstruction{in}); err != nil || flow.Kind != PLNormal {
		t.Fatal(flow, err)
	}
	if len(host.calls) != 0 || !reflect.DeepEqual(before, frame.Values) || frame.RowCount() != 7 || len(values) != 1 {
		t.Fatal("NEXT changed status or evaluated SQL")
	}
	frame.Values[in.ReturnNext.Slot] = sqltype.Null(d.Result.Type)
	if _, _, err := run.Statements([]PLInstruction{in}); err != nil {
		t.Fatal(err)
	}
	n, _ := values[0].CompositeField(0).Integer()
	if n != 1 || len(values) != 2 || !values[1].CompositeField(0).IsNull() {
		t.Fatal("lost immutable snapshot or NULL row")
	}
	frame.Values[in.ReturnNext.Slot] = sqltype.Value{}
	if _, _, err := run.Statements([]PLInstruction{in}); err == nil || len(values) != 2 {
		t.Fatal("unassigned record appended")
	}
}
