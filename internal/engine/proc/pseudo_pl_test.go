package proc

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func testPseudoPLProjection(t *testing.T) {
	t.Helper()
	u := pseudoUsages()
	definition := &Definition{Kind:PLpgSQLBody,Name:"pl_zero",Result:u[2283],Source:"DECLARE r ALIAS FOR $0; y r%TYPE; z y%TYPE; BEGIN z := x; y := z; r := y; RETURN $0; END"}
	if err := definition.SetFormals([]Formal{{Mode:"i",Name:"x",Usage:u[2283]}}); err != nil { t.Fatal(err) }
	syntax,err := parser.ParsePLpgSQL(definition.Source,PLReturnPolicy(definition.Result,0,false)); if err != nil { t.Fatal(err) }
	definition.PLSyntax = syntax
	resolve := func(raw parser.PLType) (sqltype.Usage,bool,error) { return u[sqltype.Bool],false,nil }
	collate := func(usage sqltype.Usage,_ *parser.ObjectName) (sqltype.Usage,error) { return usage,nil }
	published,err := CompilePL(definition,syntax,false,collate,resolve); if err != nil { t.Fatal(err) }
	definition.PL = published
	if err := published.ValidateDefinition(definition); err != nil { t.Fatal(err) }
	for _, id := range []sqltype.ID{sqltype.Int4,sqltype.Text,sqltype.Int4} {
		private := *definition
		private.Result,private.PolymorphicResult = u[id],true
		if err := private.SetFormals([]Formal{{Mode:"i",Name:"x",Usage:u[id]}}); err != nil { t.Fatal(err) }
		program,err := CompilePL(&private,syntax,false,collate,resolve); if err != nil { t.Fatal(err) }
		private.PL = program
		if err := program.ValidateDefinition(&private); err != nil { t.Fatal(err) }
		result := program.Root.Bindings["$0"]
		body := program.Root.Statements[0].Block
		if program.Variables[result].Usage != u[id] || program.Variables[body.Bindings["y"]].Usage != u[id] || program.Variables[body.Bindings["z"]].Usage != u[id] || body.Bindings["r"] != result { t.Fatal("result alias/transitive TYPE projection",id) }
		input := NewFrame(private.Inputs)
		if err := input.Bind(0,sqltype.Null(u[id].Type),u[id]); err != nil { t.Fatal(err) }
		frame := NewPLFrame(program,&private,input,sqltype.CollationRef{})
		if !frame.Values[result].IsNull() || frame.Values[result].Type() != u[id].Type { t.Fatal("result slot initialization",id) }
		frame.Close(); input.Close()
		if published.Variables[published.Root.Bindings["$0"]].Usage != u[2283] || definition.Inputs[0] != u[2283] || definition.Result != u[2283] { t.Fatal("published program mutated") }
	}
}

func TestPLpgSQLResultPlaceholderScope(t *testing.T) {
	u := pseudoUsages()
	for _, output := range []bool{false, true} {
		d := &Definition{Kind: PLpgSQLBody, Name: "scope", Result: u[sqltype.Int4], Source: "DECLARE r ALIAS FOR $0; BEGIN RETURN; END"}
		if output {
			d.Result = u[2283]
			if err := d.SetFormals([]Formal{{Name: "x", Mode: "b", Usage: u[2283]}}); err != nil {
				t.Fatal(err)
			}
		}
		syntax, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, len(d.OutputFormals), false))
		if err != nil {
			t.Fatal(err)
		}
		_, err = CompilePL(d, syntax, false, plUnitCollation(t), plUnitResolver(t, u[sqltype.Int4]))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != `variable "$0" does not exist` {
			t.Fatal("result placeholder scope diagnostic", output, err)
		}
	}
}

func TestPLpgSQLRecordFormalFrame(t *testing.T) {
	u := pseudoUsages()
	record := sqltype.DeferredRecord().Usage()
	d := &Definition{Kind: PLpgSQLBody, Name: "record_frame", Result: record, Source: "BEGIN RETURN x; END"}
	if err := d.SetFormals([]Formal{{Name: "x", Mode: "i", Usage: record}}); err != nil {
		t.Fatal(err)
	}
	syntax, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	program, err := CompilePL(d, syntax, false, plUnitCollation(t), plUnitResolver(t, u[sqltype.Int4]))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text} {
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: u[id]}})
		if err != nil {
			t.Fatal(err)
		}
		row, err := sqltype.OwnedCompositeValue(shape, []sqltype.Value{sqltype.Null(u[id].Type)})
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := sqltype.RecordValue(record.Type, row)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []sqltype.Value{wrapped, sqltype.Null(shape), sqltype.Null(record.Type)} {
			input := NewFrame([]sqltype.Usage{value.Type().Usage()})
			if err := input.Bind(0, value, value.Type().Usage()); err != nil {
				t.Fatal(err)
			}
			frame := NewPLFrame(program, d, input, sqltype.CollationRef{})
			actual := value.ActualRecord()
			if frame.Parameter(0).Type() != frame.Usage(0).Type {
				t.Fatal("record parameter type differs from analysis usage")
			}
			if actual.Type().IsDeferredRecord() {
				if frame.Values[0].Type() != nil || !frame.Parameter(0).IsNull() {
					t.Fatal("generic NULL acquired a record shape")
				}
			} else if frame.Values[0].Type() != shape || frame.Usage(0).Type != shape || frame.Values[0].IsNull() != value.IsNull() {
				t.Fatal("actual tuple or typed NULL lost its shape")
			}
			if program.Variables[0].Usage != record || d.Inputs[0] != record || input.Parameters[0].Type() != value.Type() {
				t.Fatal("invocation changed immutable declaration or caller frame")
			}
			frame.Close()
			input.Close()
		}
	}
}
