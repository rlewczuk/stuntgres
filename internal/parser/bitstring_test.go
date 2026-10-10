package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestBitstringLiteralSyntax(t *testing.T) {
	for _, tc := range []struct {
		q, text string
		pos     int
	}{{"SELECT B'a'", "ba", 8}, {"SELECT X'a'", "xa", 8}, {"SELECT B'1010'", "b1010", 8}, {"/*é*/ SELECT b'10'\n-- next\n'01'", "b1001", 14}, {`SELECT X'0\f'`, "x0\\f", 8}, {"SELECT B''", "b", 8}} {
		s, err := Parse(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		v := s[0].(*Select).Targets[0].(LiteralExpr).Value
		if v.Kind != BitStringValue || v.Text != tc.text || v.Pos.Character != tc.pos {
			t.Fatal(v, tc)
		}
	}
	for _, tc := range []struct{ q, message, pos string }{{"SELECT B'10", `unterminated bit string literal at or near "B'10"`, "8"}, {"SELECT X'0f", `unterminated hexadecimal string literal at or near "X'0f"`, "8"}, {"SELECT B'1''0'", `syntax error at or near "'0'"`, "12"}, {"SELECT B'10'/*\n*/'01'", `syntax error at or near "'01'"`, "18"}} {
		s, err := Parse(tc.q)
		var d *sqlstate.Error
		if len(s) != 0 || !errors.As(err, &d) || d.Code != "42601" || d.Message != tc.message || d.Position != tc.pos {
			t.Fatal(tc, s, err)
		}
	}
}

func TestBitstringDeclarationSyntax(t *testing.T) {
	s, err := Parse(`CREATE TABLE t(a BIT,b BIT VARYING,c VARBIT(5),d "bit",e pg_catalog.bit,f BIT('3'),g BIT(1,2),h BIT(+1),i BIT(1+1))`)
	if err != nil {
		t.Fatal(err)
	}
	cols := s[0].(*CreateTable).Elements
	for i, name := range []string{"bit", "bit varying", "varbit", "bit", "bit", "bit", "bit", "bit", "bit"} {
		if cols[i].Column.TypeName.Name != name {
			t.Fatal(i, cols[i])
		}
	}
	if !cols[3].Column.TypeName.Quoted || cols[4].Column.TypeName.Schema == nil || cols[4].Column.TypeName.Schema.Text != "pg_catalog" {
		t.Fatal(cols)
	}
	if len(cols[6].Column.TypeName.Modifiers) != 2 || !cols[7].Column.TypeName.Modifiers[0].UnaryPlus || !cols[7].Column.TypeName.Modifiers[0].NonSimple || !cols[8].Column.TypeName.Modifiers[0].NonSimple || cols[8].Column.TypeName.Modifiers[0].UnaryPlus {
		t.Fatal(cols)
	}
}

func TestBitstringModifierProvenance(t *testing.T) {
	for _, head := range []string{"BIT", "VARBIT", "BIT VARYING", `"bit"`, "pg_catalog.varbit"} {
		for _, tc := range []struct {
			expression                       string
			raw                              string
			integer                          int32
			hasInteger, nonSimple, unaryPlus bool
		}{
			{expression: "1", integer: 1, hasInteger: true},
			{expression: "(1)", integer: 1, hasInteger: true},
			{expression: "((1))", integer: 1, hasInteger: true},
			{expression: "-1", integer: -1, hasInteger: true},
			{expression: "-2147483648", integer: -2147483648, hasInteger: true},
			{expression: "2147483648", raw: "2147483648"},
			{expression: "(3.0)", raw: "3.0"},
			{expression: "('3')", raw: "3"},
			{expression: "(foo)", raw: "foo"},
			{expression: "+1", integer: 1, hasInteger: true, nonSimple: true, unaryPlus: true},
			{expression: "1+1", nonSimple: true},
			{expression: "1-1", nonSimple: true},
			{expression: "1*2", nonSimple: true},
			{expression: "1/0", nonSimple: true},
			{expression: "1%2", nonSimple: true},
			{expression: "1^2", nonSimple: true},
			{expression: "(1+1)", nonSimple: true},
			{expression: "1-(1)", nonSimple: true},
			{expression: "((1-(2)))", nonSimple: true},
			{expression: "1+(2*3)", nonSimple: true},
			{expression: "1+1-1/1", nonSimple: true},
			{expression: "2147483648+1", nonSimple: true},
		} {
			q := "CREATE TABLE t(v " + head + "(" + tc.expression + "))"
			statements, err := Parse(q)
			if err != nil {
				t.Fatal(q, err)
			}
			raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
			if raw.Pos.Character != 18 || len(raw.Modifiers) != 1 {
				t.Fatal(q, raw)
			}
			modifier := raw.Modifiers[0]
			if modifier.Raw != tc.raw || (modifier.Integer != nil) != tc.hasInteger || modifier.Integer != nil && *modifier.Integer != tc.integer || modifier.NonSimple != tc.nonSimple || modifier.UnaryPlus != tc.unaryPlus {
				t.Fatal(q, modifier, tc)
			}
		}
	}
	// New expression/parenthesis provenance is bounded to bit modifiers;
	// existing numeric, character and floating syntax guards are not widened.
	for _, q := range []string{`CREATE TABLE t(v VARCHAR(1+1))`, `CREATE TABLE t(v "varchar"(1*2))`, `CREATE TABLE t(v NUMERIC((1)))`, `CREATE TABLE t(v FLOAT((3)))`} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
}

func TestBitstringModifierUnaryProvenanceIsolation(t *testing.T) {
	statements, err := Parse(`CREATE TABLE t(a BIT(+1),b DATE(+3),c "timestamp"(+3),d NUMERIC(+3))`)
	if err != nil {
		t.Fatal(err)
	}
	columns := statements[0].(*CreateTable).Elements
	for i, column := range columns {
		modifier := column.Column.TypeName.Modifiers[0]
		if !modifier.UnaryPlus || modifier.NonSimple != (i == 0) {
			t.Fatal(i, modifier)
		}
	}
}
