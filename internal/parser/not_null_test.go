package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestNotNullOrderedSyntax(t *testing.T) {
	statements, err := Parse(`CREATE TABLE t(CONSTRAINT before NOT NULL a,a INT NULL NULL,b TEXT CONSTRAINT nn NOT NULL NOT NULL,NOT NULL b)`)
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*CreateTable)
	if len(s.Elements) != 4 || s.Elements[0].NotNull.Name.Text != "before" || s.Elements[0].NotNull.Column.Text != "a" || len(s.Elements[1].Column.Constraints) != 2 || s.Elements[1].Column.Constraints[0].NotNull || len(s.Elements[2].Column.Constraints) != 2 || s.Elements[2].Column.Constraints[0].Name.Text != "nn" || !s.Elements[2].Column.Constraints[1].NotNull {
		t.Fatal(s)
	}
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", `"s"."t"`} {
		for _, action := range []string{"ALTER a SET NOT NULL", "ALTER COLUMN a DROP NOT NULL", "ADD NOT NULL a", "ADD CONSTRAINT nn NOT NULL a", "DROP CONSTRAINT nn", "DROP CONSTRAINT IF EXISTS nn CASCADE"} {
			statements, err = Parse("ALTER TABLE " + target + " " + action)
			if err != nil {
				t.Fatal(target, action, err)
			}
			s, ok := statements[0].(*AlterTable)
			if !ok || s.Pos.Character != 1 || s.Only != strings.HasPrefix(target, "ONLY") {
				t.Fatal(s)
			}
		}
	}
}

func TestNotNullDropIfIdentifierAndModifierContexts(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t DROP if", "ALTER TABLE t DROP COLUMN if", "ALTER TABLE t DROP CONSTRAINT if", "ALTER TABLE t DROP CONSTRAINT IF EXISTS if"} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"CREATE TABLE t(a INT NO INHERIT)", "CREATE TABLE t(a INT NOT VALID)"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("modifier without preceding constraint", q)
		}
	}
}

func TestNotNullParserBoundaries(t *testing.T) {
	for _, tc := range []struct{ q, token, message, code string }{
		{"CREATE TABLE t(a INT,NOT NULL (a))", "(", `syntax error at or near "("`, "42601"},
		{"CREATE TABLE t(a INT NOT NULL NO INHERIT)", "NO INHERIT", "NOT NULL modifiers are unsupported", "0A000"},
		{"ALTER TABLE t VALIDATE CONSTRAINT nn", "VALIDATE", "ALTER TABLE action is unsupported", "0A000"},
	} {
		_, err := Parse(tc.q)
		var got *sqlstate.Error
		offset := strings.Index(tc.q, tc.token)
		if tc.token == "(" {
			offset = strings.LastIndex(tc.q, "(a)")
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: strconv.Itoa(offset + 1)}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(tc.q, err, got, want)
		}
	}
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL garbage", "CREATE TABLE t(a INT CONSTRAINT nn)", "ALTER TABLE t ADD NOT NULL a; INSERT INTO"} {
		if s, err := Parse(q); err == nil || s != nil {
			t.Fatal(q, s, err)
		}
	}
}

func TestNotNullInheritanceCompleteInput(t *testing.T) {
	for _, tc := range []struct{ q, token string }{
		{"CREATE TABLE child(a INT) INHERITS()", ")"},
		{"CREATE TABLE child(a INT) INHERITS(parent", ""},
		{"CREATE TABLE child(a INT) INHERITS(parent,)", ")"},
		{"CREATE TABLE child(a INT) INHERITS(parent other)", "other"},
		{`CREATE TABLE child(a INT) INHERITS("s".)`, ")"},
		{"CREATE TABLE child(a INT) INHERITS(parent) garbage", "garbage"},
		{"CREATE TABLE child(a INT) INHERITS(parent) INHERITS(other)", "INHERITS"},
		{`CREATE TABLE "échild"(a INT) INHERITS("s"."p",other,)`, ")"},
		{"CREATE TEMP TABLE child() ON COMMIT DROP INHERITS(parent)", "INHERITS"},
		{"CREATE TEMP TABLE child() INHERITS(parent) ON COMMIT DROP garbage", "garbage"},
	} {
		for _, prefix := range []string{"", "CREATE TABLE prefix(a INT); INSERT INTO keeper VALUES(92); "} {
			q := prefix + tc.q
			position := utf8.RuneCountInString(q) + 1
			message := "syntax error at end of input"
			if tc.token != "" {
				position = utf8.RuneCountInString(q[:strings.LastIndex(q, tc.token)]) + 1
				message = `syntax error at or near "` + tc.token + `"`
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: message, Position: strconv.Itoa(position)}
			statements, err := Parse(q)
			var got *sqlstate.Error
			if statements != nil || !errors.As(err, &got) || *got != want {
				t.Fatal(q, statements, err, got, want)
			}
		}
	}
}

func TestNotNullIntegratedTemporalSyntax(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE t(a TIME(3) WITHOUT TIME ZONE CONSTRAINT nn NOT NULL,b TIMESTAMP(6) NULL)",
		`CREATE TABLE t(a "time"(3) CONSTRAINT nn NOT NULL,b "timestamp"(6) NULL)`,
	} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		table := statements[0].(*CreateTable)
		a, b := table.Elements[0].Column, table.Elements[1].Column
		if a.TypeName.Name != "time" || b.TypeName.Name != "timestamp" || len(a.TypeName.Modifiers) != 1 || a.TypeName.Modifiers[0].Integer == nil || *a.TypeName.Modifiers[0].Integer != 3 || len(b.TypeName.Modifiers) != 1 || b.TypeName.Modifiers[0].Integer == nil || *b.TypeName.Modifiers[0].Integer != 6 || len(a.Constraints) != 1 || !a.Constraints[0].NotNull || a.Constraints[0].Name.Text != "nn" || len(b.Constraints) != 1 || b.Constraints[0].NotNull {
			t.Fatal(table)
		}
	}
}

func TestNotNullIntegratedTemporalContextualNames(t *testing.T) {
	statements, err := Parse("CREATE TABLE time(timestamp TIME(3) CONSTRAINT time NOT NULL, time TIMESTAMP(6), CONSTRAINT timestamp NOT NULL time)")
	if err != nil {
		t.Fatal(err)
	}
	table := statements[0].(*CreateTable)
	a, b := table.Elements[0].Column, table.Elements[1].Column
	nn := table.Elements[2].NotNull
	if table.Name.Parts[0].Text != "time" || a.Name != "timestamp" || a.TypeName.Name != "time" || len(a.TypeName.Modifiers) != 1 || a.TypeName.Modifiers[0].Integer == nil || *a.TypeName.Modifiers[0].Integer != 3 || len(a.Constraints) != 1 || a.Constraints[0].Name.Text != "time" || !a.Constraints[0].NotNull || b.Name != "time" || b.TypeName.Name != "timestamp" || len(b.TypeName.Modifiers) != 1 || b.TypeName.Modifiers[0].Integer == nil || *b.TypeName.Modifiers[0].Integer != 6 || nn == nil || nn.Name.Text != "timestamp" || nn.Column.Text != "time" {
		t.Fatal(table)
	}
}

func TestNotNullIntegratedNumericModifierSyntax(t *testing.T) {
	statements, err := Parse("CREATE TABLE n(a NUMERIC(2,-3) CONSTRAINT nn NOT NULL,b DECIMAL(3,+5) NULL,CONSTRAINT bn NOT NULL b)")
	if err != nil {
		t.Fatal(err)
	}
	table := statements[0].(*CreateTable)
	for i, want := range [][]int32{{2, -3}, {3, 5}} {
		column := table.Elements[i].Column
		if len(column.TypeName.Modifiers) != 2 || len(column.Constraints) != 1 || column.Constraints[0].NotNull != (i == 0) {
			t.Fatal(column)
		}
		for j, value := range want {
			modifier := column.TypeName.Modifiers[j]
			if modifier.Integer == nil || *modifier.Integer != value {
				t.Fatal(column, modifier)
			}
		}
	}
	if table.Elements[0].Column.Constraints[0].Name.Text != "nn" || !table.Elements[1].Column.TypeName.Modifiers[1].UnaryPlus || table.Elements[2].NotNull == nil || table.Elements[2].NotNull.Name.Text != "bn" || table.Elements[2].NotNull.Column.Text != "b" {
		t.Fatal(table)
	}
}
