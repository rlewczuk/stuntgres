package core_test

import (
	"reflect"
	"strings"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLSQLExecutionProgramValidation(t *testing.T) {
	const source = "DECLARE r record; x integer; BEGIN PERFORM r.n; CALL missing(r.n,x); IF false THEN INSERT INTO missing VALUES(x); END IF; CASE x WHEN 1 THEN RETURN 7; END CASE; RETURN 7; END"
	_, original, _, _ := loopUnitFrame(t, source)
	program := original.SQL.PL
	body := program.Root.Statements[0].Block
	if body.Statements[0].SQL.Query != body.Statements[0].Syntax.SQL.Query || body.Statements[1].SQL.Kind != parser.PLCallSQL {
		t.Fatal("checked AST association")
	}
	before := *body.Statements[0].SQL
	formals := append([]proc.Formal(nil), original.SQL.Formals...)
	for _, name := range []string{"absent", "kind", "query", "span", "text", "raw_kind", "extra", "mixed", "ledger"} {
		t.Run(name, func(t *testing.T) {
			_, d, _, _ := loopUnitFrame(t, source)
			b := d.SQL.PL.Root.Statements[0].Block
			in := &b.Statements[0]
			switch name {
			case "absent":
				in.SQL = nil
			case "kind":
				in.SQL.Kind = parser.PLCallSQL
			case "query":
				in.SQL.Query = &parser.Select{}
			case "span":
				in.Syntax.SQL.End.Character++
			case "text":
				in.Syntax.SQL.Text = "PERFORM 2"
			case "raw_kind":
				in.Syntax.SQL.Kind = parser.PLCallSQL
				in.SQL.Kind = parser.PLCallSQL
			case "extra":
				in.Query = &parser.Select{}
			case "mixed":
				in.Syntax.Target = parser.ColumnExpr{}
				in.Target.Slot = d.SQL.PL.FoundSlot
			case "ledger":
				b.Statements[3].Case.SearchName = "__Case__Variable_999__"
			}
			if err := d.SQL.PL.ValidateDefinition(d.SQL); err == nil {
				t.Fatal("accepted forged program", name)
			}
		})
	}
	if *body.Statements[0].SQL != before || !reflect.DeepEqual(formals, original.SQL.Formals) || program.ValidateDefinition(original.SQL) != nil {
		t.Fatal("independent compile/validation mutated immutable original")
	}
}

func TestPLpgSQLSQLExecutionProgramAdmission(t *testing.T) {
	const source = "BEGIN INSERT INTO missing VALUES(1); INSERT INTO missing VALUES(2); PERFORM 1; RETURN 7; END"
	excluded := []struct {
		name  string
		query func(parser.Position) parser.Statement
	}{
		{"transaction_control", func(at parser.Position) parser.Statement { return &parser.TransactionControl{Pos: at} }},
		{"transaction_characteristics", func(at parser.Position) parser.Statement { return &parser.TransactionCharacteristics{Pos: at} }},
		{"declare_cursor", func(at parser.Position) parser.Statement { return &parser.DeclareCursor{Pos: at} }},
		{"fetch_cursor", func(at parser.Position) parser.Statement { return &parser.FetchCursor{Pos: at} }},
		{"close_cursor", func(at parser.Position) parser.Statement { return &parser.CloseCursor{Pos: at} }},
		{"execute_sql", func(at parser.Position) parser.Statement { return &parser.ExecuteSQL{Pos: at} }},
	}
	for _, tc := range excluded {
		t.Run(tc.name, func(t *testing.T) {
			_, d, _, read := loopUnitFrame(t, source)
			in := &d.SQL.PL.Root.Statements[0].Block.Statements[0]
			query := tc.query(in.Syntax.SQL.Start)
			in.Syntax.SQL.Query, in.SQL.Query = query, query
			if err := d.SQL.PL.ValidateDefinition(d.SQL); err == nil {
				t.Fatal("validator admitted coordinated grammar-excluded AST")
			}
			if _, err := stored.NewRoutineRemap(read, nil, nil, nil, true).Definition(d.SQL); err == nil {
				t.Fatal("remapper admitted coordinated grammar-excluded AST")
			}
		})
	}
	for _, name := range []string{"span", "source", "variant"} {
		t.Run(name, func(t *testing.T) {
			_, d, _, read := loopUnitFrame(t, source)
			body := d.SQL.PL.Root.Statements[0].Block
			in := &body.Statements[0]
			switch name {
			case "span":
				// All current coordinates/text agree, but this is no longer the
				// single static statement the grammar admitted.
				raw := in.Syntax.SQL
				raw.End, raw.Delimiter = body.Statements[1].Syntax.SQL.End, body.Statements[1].Syntax.SQL.Delimiter
				raw.Text = source[raw.Start.Offset:raw.End.Offset]
			case "source":
				// Preserve lengths, positions, AST identity and diagnostic slots.
				d.SQL.Source = strings.Replace(source, "VALUES(1)", "VALUES(9)", 1)
				raw := in.Syntax.SQL
				raw.Text = d.SQL.Source[raw.Start.Offset:raw.End.Offset]
			case "variant":
				in = &body.Statements[2]
				d.SQL.Source = strings.Replace(source, "PERFORM", "SELECT ", 1)
				raw := in.Syntax.SQL
				raw.Kind, in.SQL.Kind = parser.PLCommandSQL, parser.PLCommandSQL
				raw.Text = d.SQL.Source[raw.Start.Offset:raw.End.Offset]
			}
			if err := d.SQL.PL.ValidateDefinition(d.SQL); err == nil {
				t.Fatal("validator admitted coordinated source/category substitution")
			}
			if _, err := stored.NewRoutineRemap(read, nil, nil, nil, true).Definition(d.SQL); err == nil {
				t.Fatal("remapper admitted coordinated source/category substitution")
			}
		})
	}
}

func TestPLpgSQLSQLExecutionRemap(t *testing.T) {
	_, d, _, read := loopUnitFrame(t, "DECLARE x integer; BEGIN PERFORM x; CALL missing(x); INSERT INTO missing VALUES(x); RETURN 7; END")
	original := d.SQL
	program := original.PL
	body := program.Root.Statements[0].Block
	mapper := stored.NewRoutineRemap(read, nil, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old != nil && old.ID() == sqltype.Int4 {
			next, _ := read.LookupType(sqltype.Int8)
			return next
		}
		return old
	}, nil, true)
	first, err := mapper.Definition(original)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mapper.Definition(original)
	if err != nil || first != second || first == original || first.PL == program {
		t.Fatal("operation-wide original memo/ownership", err)
	}
	mapped := first.PL.Root.Statements[0].Block
	if mapped != body || first.PLSyntax != original.PLSyntax || mapped.Statements[1].SQL != body.Statements[1].SQL {
		t.Fatal("syntax-only command recipes unnecessarily copied")
	}
	for i, v := range program.Variables {
		if v.Usage.Type != nil && v.Usage.Type.ID() == sqltype.Int4 && first.PL.Variables[i].Usage.Type.ID() != sqltype.Int8 {
			t.Fatal("local complete usage not remapped")
		}
	}
	if original.Result.Type.ID() != sqltype.Int4 || first.Result.Type.ID() != sqltype.Int8 || original.PL != program || program.ValidateDefinition(original) != nil || first.PL.ValidateDefinition(first) != nil {
		t.Fatal("source mutation or stale mapped layout")
	}
	forged := *original
	invalid := *program
	forged.PL = &invalid
	invalid.Root = &proc.PLBlock{}
	if _, err := stored.NewRoutineRemap(read, nil, nil, nil, true).Definition(&forged); err == nil {
		t.Fatal("remapper admitted forged checked program")
	}
}
