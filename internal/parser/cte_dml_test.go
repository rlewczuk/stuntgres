package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestCTEDMLParserBodiesAndPrimaries(t *testing.T) {
	bodies := []string{"INSERT INTO t VALUES(1) RETURNING id", "UPDATE t SET id=2 RETURNING id", "DELETE FROM t RETURNING id", "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE RETURNING t.id"}
	primaries := []string{"SELECT * FROM d", "INSERT INTO dst SELECT * FROM d", "UPDATE dst SET id=d.id FROM d", "DELETE FROM dst USING d", "MERGE INTO dst USING d ON dst.id=d.id WHEN MATCHED THEN DELETE"}
	bodyTypes := []reflect.Type{reflect.TypeFor[*Insert](), reflect.TypeFor[*Update](), reflect.TypeFor[*Delete](), reflect.TypeFor[*Merge]()}
	mainTypes := []reflect.Type{reflect.TypeFor[*Select](), reflect.TypeFor[*Insert](), reflect.TypeFor[*Update](), reflect.TypeFor[*Delete](), reflect.TypeFor[*Merge]()}
	for i, body := range bodies {
		for j, primary := range primaries {
			for _, hint := range []string{"", "MATERIALIZED ", "NOT MATERIALIZED "} {
				q := "/* é */ WITH d(n) AS " + hint + "(" + body + ") " + primary
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, statements, err)
				}
				definitions := StatementWith(statements[0])
				if len(definitions) != 1 || definitions[0].Name.Text != "d" || reflect.TypeOf(definitions[0].Query) != bodyTypes[i] || reflect.TypeOf(statements[0]) != mainTypes[j] || !HasModifyingCTE(statements[0]) {
					t.Fatal(q, definitions)
				}
				at := strings.Index(q, "d(n)")
				if definitions[0].Name.Pos.Offset != at || definitions[0].Name.Pos.Character != utf8.RuneCountInString(q[:at])+1 {
					t.Fatal(definitions[0].Name.Pos)
				}
			}
		}
	}
}

func TestCTEDMLParserTargetOccurrences(t *testing.T) {
	statements, err := Parse("WITH t AS (SELECT 7 AS id), a AS (DELETE FROM t RETURNING id), b AS (INSERT INTO t SELECT id FROM a RETURNING id) UPDATE t SET id=b.id FROM b")
	if err != nil {
		t.Fatal(err)
	}
	var targets []Statement
	if err := VisitMutationTargets(statements[0], func(command Statement) error { targets = append(targets, command); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0] == targets[1] || targets[1] == targets[2] {
		t.Fatal(targets)
	}
	var physical, virtual []string
	err = VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
		physical = append(physical, table.Name.Parts[0].Text)
		return nil
	}, func(table *TableReference, cte bool, _ string) {
		if cte {
			virtual = append(virtual, table.Name.Parts[0].Text)
		}
	})
	if err != nil || len(physical) != 0 || !reflect.DeepEqual(virtual, []string{"a", "b"}) {
		t.Fatal(physical, virtual, err)
	}
}

func TestCTEDMLParserNestingAndWholeInput(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM (WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d) s",
		"INSERT INTO t WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d",
		"WITH a AS (WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d) SELECT * FROM a",
		"WITH a AS (WITH d AS (DELETE FROM t RETURNING id) INSERT INTO dst SELECT * FROM d RETURNING id) SELECT * FROM a",
	} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "WITH clause containing a data-modifying statement must be at the top level" {
			t.Fatal(q, statements, err)
		}
		at := strings.Index(q, "d AS")
		if diagnostic.Position != strconv.Itoa(utf8.RuneCountInString(q[:at])+1) {
			t.Fatal(diagnostic)
		}
	}
	if statements, err := Parse("WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d; SELECT FROM"); err == nil || len(statements) != 0 {
		t.Fatal("accepted a valid prefix", statements, err)
	}
}
