package parser

import "testing"

func TestDateNamesLiteralsAndModifiers(t *testing.T) {
	for _, name := range []string{"DATE", `"date"`, `U&"d\0061te"`} {
		stmts, err := Parse("/*é*/ CREATE TABLE date(date " + name + "(3)); SELECT " + name + " '2001--01-01' FROM date")
		if err != nil || len(stmts) != 2 {
			t.Fatal(stmts, err)
		}
		typ := stmts[0].(*CreateTable).Elements[0].Column.TypeName
		if typ.Name != "date" || len(typ.Modifiers) != 1 || typ.Modifiers[0].Integer == nil || *typ.Modifiers[0].Integer != 3 || typ.Pos.Character != 30 {
			t.Fatal(typ)
		}
		raw := stmts[1].(*Select).Targets[0].(TypedLiteralExpr)
		if raw.TypeName.Name != "date" || raw.Text != "2001--01-01" || raw.Pos.Character <= raw.TypeName.Pos.Character {
			t.Fatal(raw)
		}
	}
	stmts, err := Parse("CREATE TABLE date(date date); INSERT INTO date VALUES(DATE 'epoch'); SELECT date FROM date")
	if err != nil || len(stmts) != 3 {
		t.Fatal(stmts, err)
	}
	if _, ok := stmts[2].(*Select).Targets[0].(ColumnExpr); !ok {
		t.Fatal("date identifier lost")
	}
	stmts, err = Parse("SELECT DATE(3) 'epoch' FROM date")
	if err != nil {
		t.Fatal(err)
	}
	raw := stmts[0].(*Select).Targets[0].(TypedLiteralExpr)
	if len(raw.TypeName.Modifiers) != 1 || raw.TypeName.Pos.Character != 8 || raw.Pos.Character != 16 {
		t.Fatal(raw)
	}
	for _, q := range []string{"SELECT DATE 'epoch'", "SELECT DATE 'epoch' FROM date; SELECT date FROM", "SELECT UUID 'bad' FROM date", "SELECT date(3) FROM date", "SELECT date('epoch') FROM date", "SELECT date($1) FROM date", "SELECT date((3)) 'epoch' FROM date"} {
		stmts, err := Parse(q)
		if err == nil || stmts != nil {
			t.Fatal("excluded syntax accepted", q, stmts, err)
		}
	}
}

func TestDateLiteralTemporalDisambiguationAndPositions(t *testing.T) {
	for _, tc := range []struct {
		head string
		name string
	}{
		{"DATE", "date"}, {`"date"`, "date"}, {`U&"d\0061te"`, "date"},
		{"TIME", "time"}, {"TIMESTAMP", "timestamp"},
		{"TIME(3)", "time"}, {"TIMESTAMP(3)", "timestamp"}, {"DATE(3)", "date"},
	} {
		q := "/*é*/ SELECT " + tc.head + " 'epoch' FROM date"
		stmts, err := Parse(q)
		if err != nil || len(stmts) != 1 {
			t.Fatal(q, stmts, err)
		}
		raw, ok := stmts[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || raw.TypeName.Name != tc.name || raw.TypeName.Pos.Character != 14 || raw.Pos.Character != 15+len([]rune(tc.head)) {
			t.Fatal(q, raw)
		}
		if stmts, err := Parse(q + "; SELECT date FROM"); err == nil || stmts != nil {
			t.Fatal("valid prefix accepted with incomplete suffix", q, stmts, err)
		}
	}
}

func TestDateModifierModeAndBaselineCastCoexistence(t *testing.T) {
	for _, name := range []string{"DATE", `"date"`, `U&"d\0061te"`} {
		for _, modifier := range []string{"3", "3.0", ".5", "2147483648", "999999999999999999999999999999", "+3", "+3.0", "-3"} {
			q := "/*é*/ SELECT " + name + "(" + modifier + ") 'epoch', int4('7'), CAST('8' AS int8), date, DATE 'today'::text FROM date"
			stmts, err := Parse(q)
			if err != nil || len(stmts) != 1 {
				t.Fatal(q, stmts, err)
			}
			targets := stmts[0].(*Select).Targets
			raw, ok := targets[0].(TypedLiteralExpr)
			if !ok || raw.TypeName.Name != "date" || raw.TypeName.Pos.Character != 14 || len(raw.TypeName.Modifiers) != 1 || raw.Pos.Character != 17+len([]rune(name))+len(modifier) {
				t.Fatal(q, raw)
			}
			if targets[1].(CastExpr).Syntax != FunctionCast || targets[2].(CastExpr).Syntax != StandardCast || targets[3].(ColumnExpr).Name.Parts[0].Text != "date" {
				t.Fatal(q, targets)
			}
			cast := targets[4].(CastExpr)
			if cast.Syntax != PostfixCast || cast.Child.(TypedLiteralExpr).TypeName.Name != "date" {
				t.Fatal(q, cast)
			}
			if raw.TypeName.Modifiers[0].UnaryPlus != (modifier[0] == '+') {
				t.Fatal(q, raw.TypeName.Modifiers)
			}
			if modifier == "3.0" || modifier == ".5" || modifier == "2147483648" || modifier == "999999999999999999999999999999" {
				if raw.TypeName.Modifiers[0].Raw != modifier || raw.TypeName.Modifiers[0].Integer != nil {
					t.Fatal("raw modifier converted before type resolution", q, raw.TypeName.Modifiers)
				}
			}
			if stmts, err := Parse(q + "; SELECT date FROM"); err == nil || stmts != nil {
				t.Fatal("valid prefix accepted with malformed suffix", q, stmts, err)
			}
		}
	}
}

func TestDateAndByteaRawModifierModeIsolation(t *testing.T) {
	for _, name := range []string{"BYTEA", `"bytea"`} {
		q := "CREATE TABLE d(a DATE(3.0), b " + name + "(2147483648), c DATE(+3.0)); SELECT DATE(2147483648) 'epoch', int4('7') FROM d; CREATE TABLE raw(v " + name + "(+3.0))"
		stmts, err := Parse(q)
		if err != nil || len(stmts) != 3 {
			t.Fatal(q, stmts, err)
		}
		columns := stmts[0].(*CreateTable).Elements
		for i, want := range []struct {
			name, raw string
			plus      bool
		}{{"date", "3.0", false}, {"bytea", "2147483648", false}, {"date", "3.0", true}} {
			typ := columns[i].Column.TypeName
			if typ.Name != want.name || len(typ.Modifiers) != 1 || typ.Modifiers[0].Raw != want.raw || typ.Modifiers[0].Integer != nil || typ.Modifiers[0].UnaryPlus != want.plus {
				t.Fatal(q, typ, want)
			}
		}
		targets := stmts[1].(*Select).Targets
		if targets[0].(TypedLiteralExpr).TypeName.Modifiers[0].Raw != "2147483648" || targets[1].(CastExpr).Syntax != FunctionCast {
			t.Fatal(q, targets)
		}
		typ := stmts[2].(*CreateTable).Elements[0].Column.TypeName
		if typ.Name != "bytea" || len(typ.Modifiers) != 1 || typ.Modifiers[0].Raw != "3.0" || !typ.Modifiers[0].UnaryPlus {
			t.Fatal(q, typ)
		}
	}
}
