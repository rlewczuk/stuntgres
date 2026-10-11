package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestUniqueColumnAndTableSyntax(t *testing.T) {
	for _, query := range []string{
		`CREATE TABLE t(a INT UNIQUE,b TEXT CONSTRAINT "Named" UNIQUE NULLS NOT DISTINCT,CONSTRAINT c UNIQUE NULLS DISTINCT (b,a))`,
		`CREATE TABLE t(CONSTRAINT c UNIQUE NULLS NOT DISTINCT (b,a),a INT UNIQUE NULLS DISTINCT,b TEXT UNIQUE)`,
	} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		table := statements[0].(*CreateTable)
		var definitions []*UniqueConstraint
		for _, element := range table.Elements {
			if element.Unique != nil {
				definitions = append(definitions, element.Unique)
			}
			if element.Column != nil {
				for _, constraint := range element.Column.Constraints {
					if constraint.Unique != nil {
						definitions = append(definitions, constraint.Unique)
					}
				}
			}
		}
		if len(definitions) != 3 {
			t.Fatal(definitions)
		}
		for _, definition := range definitions {
			if definition.Pos.Character == 0 {
				t.Fatal("lost declaration position")
			}
			if definition.Name.Text != "" && definition.Name.Pos.Character == 0 {
				t.Fatal("lost name position")
			}
		}
		if strings.Contains(query, `"Named"`) {
			if definitions[0].NullsNotDistinct || !definitions[1].NullsNotDistinct || definitions[1].Name.Text != "Named" || definitions[2].NullsNotDistinct {
				t.Fatal(definitions)
			}
			if !reflect.DeepEqual([]string{definitions[2].Columns[0].Text, definitions[2].Columns[1].Text}, []string{"b", "a"}) {
				t.Fatal(definitions[2])
			}
		} else if !definitions[0].NullsNotDistinct || len(definitions[0].Columns) != 2 {
			t.Fatal(definitions[0])
		}
	}
	// NULLS FIRST/LAST remain sort lookahead, not UNIQUE policy or new names.
	if _, err := Parse("SELECT a FROM t ORDER BY a NULLS FIRST; SELECT a FROM t ORDER BY a DESC NULLS LAST"); err != nil {
		t.Fatal(err)
	}
}

func TestUniqueDeferrabilityCapabilityBoundary(t *testing.T) {
	for _, query := range []string{
		"CREATE TABLE t(a INT UNIQUE DEFERRABLE)",
		"CREATE TABLE t(a INT UNIQUE NOT DEFERRABLE)",
		"CREATE TABLE t(a INT UNIQUE DEFERRABLE INITIALLY DEFERRED)",
		"CREATE TABLE t(a INT,CONSTRAINT u UNIQUE(a) DEFERRABLE INITIALLY IMMEDIATE)",
		"CREATE TABLE t(a INT,UNIQUE(a) NOT DEFERRABLE)",
		"CREATE TABLE t(a INT UNIQUE INITIALLY DEFERRED)",
		"CREATE TABLE t(a INT,UNIQUE(a) INITIALLY IMMEDIATE)",
	} {
		_, err := Parse(query)
		var got *sqlstate.Error
		token := "DEFERRABLE"
		if strings.Contains(query, "NOT DEFERRABLE") {
			token = "NOT DEFERRABLE"
		}
		if !strings.Contains(query, "DEFERRABLE") {
			token = "INITIALLY"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "UNIQUE deferrability is unsupported", Position: strconv.Itoa(strings.Index(query, token) + 1)}
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatalf("%s: got %#v want %#v: %v", query, got, want, err)
		}
	}
}

func TestUniqueNotLookaheadPreservesConstraintsAndTransactions(t *testing.T) {
	for _, policy := range []string{"", " NULLS DISTINCT", " NULLS NOT DISTINCT"} {
		for _, query := range []string{
			"CREATE TABLE t(a INT UNIQUE" + policy + " NOT NULL)",
			"CREATE TABLE t(a INT CONSTRAINT u UNIQUE" + policy + " /* boundary */ NOT NULL)",
			"CREATE TABLE t(a INT NOT NULL UNIQUE" + policy + ")",
			"CREATE TABLE t(a INT CONSTRAINT n NOT NULL CONSTRAINT u UNIQUE" + policy + ")",
		} {
			if _, err := Parse(query); err != nil {
				t.Fatal(query, err)
			}
		}
	}
	for _, query := range []string{"BEGIN NOT /* boundary */ DEFERRABLE", "SET TRANSACTION NOT DEFERRABLE"} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		var modes []TransactionMode
		switch statement := statements[0].(type) {
		case *TransactionControl:
			modes = statement.Modes
		case *TransactionCharacteristics:
			modes = statement.Modes
		default:
			t.Fatal(statement)
		}
		if len(modes) != 1 || !modes[0].DeferrableSet || modes[0].Deferrable {
			t.Fatal("lost NOT DEFERRABLE transaction value", query, modes)
		}
	}
	for _, query := range []string{"SELECT NOT FALSE FROM t", "SELECT 1 IS NOT DISTINCT FROM 1 FROM t", "SELECT 1 IS NOT NULL FROM t"} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	for _, query := range []string{
		"CREATE TABLE t(a INT UNIQUE NOT)",
		"CREATE TABLE t(a INT UNIQUE NOT DISTINCT)",
		`CREATE TABLE t(a INT UNIQUE NOT "DEFERRABLE")`,
		`CREATE TABLE t(a INT UNIQUE "initially" "deferred")`,
	} {
		_, err := Parse(query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("malformed NOT tail admitted", query, err)
		}
	}
}

func TestUniqueWholeInputValidation(t *testing.T) {
	for _, query := range []string{
		"CREATE TABLE t(a INT UNIQUE) leftover",
		"CREATE TABLE t(a INT UNIQUE DEFERRABLE) leftover",
		"CREATE TABLE t(a INT UNIQUE NULLS FIRST)",
		"CREATE TABLE t(a INT,UNIQUE(a) INCLUDE(b))",
		"ALTER TABLE t ADD UNIQUE(a) leftover",
		"ALTER TABLE t ADD CONSTRAINT u UNIQUE(a) leftover",
	} {
		_, err := Parse(query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("trailing syntax was ignored or capability hid malformed input", query, err)
		}
	}
}

func TestUniqueAlterAddAttachments(t *testing.T) {
	for _, query := range []string{
		"ALTER TABLE t ADD UNIQUE(a)",
		"ALTER TABLE t ADD CONSTRAINT u UNIQUE NULLS NOT DISTINCT(a,b)",
	} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		action := statements[0].(*AlterTable).Actions[0]
		if action.Action != AddUnique || action.Unique == nil || action.Unique.Primary || len(action.Unique.Columns) == 0 {
			t.Fatal(query, action)
		}
		if action.Unique.NullsNotDistinct != strings.Contains(query, "NOT DISTINCT") {
			t.Fatal(query, action.Unique)
		}
	}
}
