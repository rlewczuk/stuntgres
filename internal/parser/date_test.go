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
	stmts, err = Parse("SELECT DATE 'epoch'")
	if err != nil || len(stmts) != 1 || stmts[0].(*Select).From != nil {
		t.Fatal(stmts, err)
	}
	if literal, ok := stmts[0].(*Select).Targets[0].(TypedLiteralExpr); !ok || literal.TypeName.Name != "date" {
		t.Fatal(stmts)
	}
	for _, tc := range []struct {
		head, argument string
		argumentOffset int
	}{
		{"date", "3", 0}, {"date", "+3", 0}, {"date", "-3", 0},
		{"date", "1 + DATE '2000-01-01'", 2},
		{"pg_catalog.timetz", "3", 0}, {"pg_catalog.timestamptz", "3", 0},
	} {
		q := "/*é*/ SELECT " + tc.head + "(" + tc.argument + "), DATE(3) 'epoch', int4('7') FROM date; SELECT date(4)"
		statements, err := Parse(q)
		if err != nil || len(statements) != 2 {
			t.Fatal(q, statements, err)
		}
		targets := statements[0].(*Select).Targets
		call, ok := targets[0].(CastExpr)
		if !ok || call.Syntax != FunctionCast || call.Pos.Character != 14 || call.Child.SourcePosition().Character != 15+len(tc.head)+tc.argumentOffset {
			t.Fatal("numeric argument became temporal precision", q, targets[0])
		}
		literal, ok := targets[1].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "date" || len(literal.TypeName.Modifiers) != 1 || targets[2].(CastExpr).Syntax != FunctionCast {
			t.Fatal("call/literal mode leaked", q, targets)
		}
		next, ok := statements[1].(*Select).Targets[0].(CastExpr)
		if !ok || next.Syntax != FunctionCast || next.Child.(LiteralExpr).Value.Integer != 4 {
			t.Fatal("temporal mode leaked into next statement", q, next)
		}
	}
	for _, q := range []string{"SELECT DATE 'epoch' FROM date; SELECT date FROM", "SELECT date((3)) 'epoch' FROM date"} {
		stmts, err := Parse(q)
		if err == nil || stmts != nil {
			t.Fatal("excluded syntax accepted", q, stmts, err)
		}
	}
}

func TestDomainNominalLiteralSyntax(t *testing.T) {
	for _, head := range []string{"positive", "public.positive", "UUID", `"name"`, `"e"`, `"b"`, `"x"`} {
		stmts, err := Parse("SELECT " + head + " '1'")
		if err != nil || len(stmts) != 1 {
			t.Fatal(head, stmts, err)
		}
		raw, ok := stmts[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || !raw.RequiresDomain {
			t.Fatal("lost domain-only syntax fact", head, stmts)
		}
	}
	for _, head := range []string{"e", "E", "b", "B", "x", "X"} {
		stmts, err := Parse("SELECT " + head + " '1'")
		if err == nil || stmts != nil {
			t.Fatal("accepted separated string prefix", head, stmts, err)
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
		{"pg_catalog.timetz(3)", "timetz"}, {"pg_catalog.timestamptz(3)", "timestamptz"},
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
