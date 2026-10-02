package parser

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestScriptAndLiterals(t *testing.T) {
	script := `; /* outer /* nested */ comment */
 CREATE TABLE "Mixed;Name" (INTEGER INT NOT NULL, "a""b" TEXT NULL, active BOOL, wide INT8);
 INSERT INTO "Mixed;Name" (INTEGER, "a""b", active, wide) VALUES
 (+12, 'O''Brien; $99 \\ stays', TRUE, -9223372036854775808),
 (NULL, 'first' -- continue a standard string
 'second', FALSE, 9223372036854775807);
 SELECT INTEGER, "a""b" FROM "Mixed;Name" WHERE INTEGER = $2 ORDER BY active DESC;; -- end`
	statements, err := Parse(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 3 {
		t.Fatalf("statements = %d", len(statements))
	}
	create := statements[0].(*CreateTable)
	if create.Name != "Mixed;Name" || create.Columns[0].Name != "integer" || create.Columns[0].Nullable || create.Columns[1].Name != "a\"b" || !create.Columns[1].Nullable || create.Columns[3].Type != Int8 {
		t.Fatalf("create = %#v", create)
	}
	insert := statements[1].(*Insert)
	want := [][]Value{
		{{Kind: IntegerValue, Integer: 12}, {Kind: StringValue, Text: "O'Brien; $99 \\\\ stays"}, {Kind: BooleanValue, Boolean: true}, {Kind: IntegerValue, Integer: math.MinInt64}},
		{{Kind: NullValue}, {Kind: StringValue, Text: "firstsecond"}, {Kind: BooleanValue}, {Kind: IntegerValue, Integer: math.MaxInt64}},
	}
	for i := range insert.Rows {
		for j := range insert.Rows[i] {
			got := insert.Rows[i][j]
			got.Pos = Position{}
			if !reflect.DeepEqual(got, want[i][j]) {
				t.Errorf("row %d value %d: %#v != %#v", i, j, got, want[i][j])
			}
		}
	}
	selectStmt := statements[2].(*Select)
	if !reflect.DeepEqual(selectStmt.Columns, []string{"integer", "a\"b"}) || selectStmt.Where.Value.Parameter != 2 || selectStmt.Order.Column != "active" || !selectStmt.Order.Desc {
		t.Fatalf("select = %#v", selectStmt)
	}
}

func TestIdentifierNormalization(t *testing.T) {
	long := strings.Repeat("A", 62) + "éZ"
	statements, err := Parse(`CREATE TABLE ` + long + ` ("` + long + `" TEXT, ÉCOLE INT, bigint BOOL, values TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	got := statements[0].(*CreateTable)
	if got.Name != strings.Repeat("a", 62) || got.Columns[0].Name != strings.Repeat("A", 62) || got.Columns[1].Name != "École" || got.Columns[2].Name != "bigint" || got.Columns[3].Name != "values" {
		t.Fatalf("normalized = %#v", got)
	}
}

func TestEmptyScripts(t *testing.T) {
	for _, query := range []string{"", " \n\t", "-- comment", "/* nested /* x */ */", ";;; -- trailing"} {
		got, err := Parse(query)
		if err != nil || len(got) != 0 {
			t.Errorf("Parse(%q) = %#v, %v", query, got, err)
		}
	}
}

func TestParserErrorBoundaries(t *testing.T) {
	cases := []struct{ query, code string }{
		{"CREATE TABLE t ()", "42601"}, {"CREATE TABLE t (x INT", "42601"},
		{"SELECT FROM t", "42601"}, {"SELECT * t", "42601"},
		{"SELECT * FROM t; nonsense", "42601"}, {"INSERT INTO t VALUES ('a' 'b')", "42601"},
		{"INSERT INTO t VALUES ('a' /*\n*/ 'b')", "42601"},
		{"INSERT INTO t VALUES ($0)", "42P02"}, {"INSERT INTO t VALUES ($000)", "42P02"},
		{"INSERT INTO t VALUES ($2147483648)", "42601"}, {"INSERT INTO t VALUES ($1junk)", "42601"},
		{"INSERT INTO t VALUES (?)", "42601"}, {"INSERT INTO t VALUES (:name)", "42601"},
		{"INSERT INTO t VALUES (9223372036854775808)", "22003"},
		{"INSERT INTO t VALUES (-9223372036854775809)", "22003"},
		{"INSERT INTO t VALUES (1.0)", "0A000"}, {"INSERT INTO t VALUES (.1)", "0A000"},
		{"INSERT INTO t VALUES (1e3)", "0A000"}, {"INSERT INTO t VALUES (1z)", "42601"},
		{"INSERT INTO t VALUES (E'a')", "0A000"}, {"INSERT INTO t VALUES ($tag$a$tag$)", "0A000"},
		{"INSERT INTO t VALUES (U&'a')", "0A000"}, {"CREATE TABLE U&\"t\" (x INT)", "0A000"},
		{"CREATE TABLE t (x TIMESTAMP)", "0A000"}, {"CREATE TABLE t (x NUMERIC(8))", "0A000"},
		{"CREATE TABLE t (x INT PRIMARY KEY)", "0A000"}, {"SELECT * FROM public.t", "0A000"},
		{"SELECT * FROM t LIMIT 1", "0A000"}, {"INSERT INTO t VALUES (1) RETURNING x", "0A000"},
		{"SELECT * FROM t alias", "0A000"}, {"SELECT x alias FROM t", "0A000"},
		{"SELECT x FROM t ORDER BY x, y", "0A000"}, {"SELECT f(x) FROM t", "0A000"},
		{"SELECT x + 1 FROM t", "0A000"}, {"SELECT 1 FROM t", "0A000"},
		{"CREATE TABLE IF NOT EXISTS t (x INT)", "0A000"}, {"CREATE TABLE t (x INT[])", "0A000"},
		{"SELECT * FROM t JOIN u ON t.x = u.x", "0A000"}, {"SELECT * FROM t WHERE x = 1::INT", "0A000"},
		{"UPDATE t SET x = 1", "0A000"}, {"DELETE FROM t", "0A000"}, {"BEGIN", "0A000"},
		{"START TRANSACTION", "0A000"}, {"COMMIT", "0A000"}, {"ROLLBACK", "0A000"},
		{"SAVEPOINT x", "0A000"}, {"RELEASE x", "0A000"},
		{"CREATE TABLE \"\" (x INT)", "42601"}, {"SELECT * FROM \"unterminated", "42601"},
		{"INSERT INTO t VALUES ('unterminated)", "42601"}, {"/* unterminated", "42601"},
		{"SELECT * FROM t\x00", "42601"}, {"INSERT INTO t VALUES ('\x00')", "42601"},
		{"SELECT * FROM \xff", "42601"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got, err := Parse(tc.query)
			var sqlErr *sqlstate.Error
			if !errors.As(err, &sqlErr) || sqlErr.Code != tc.code {
				t.Fatalf("Parse = %#v, %v; want SQLSTATE %s", got, err, tc.code)
			}
			if got != nil {
				t.Fatalf("partial script returned: %#v", got)
			}
		})
	}
}

func TestMarkersAndRepeatedParameters(t *testing.T) {
	got, err := Parse(`INSERT INTO t VALUES ('$17', $2, $2, $0001) /* $9 */`)
	if err != nil {
		t.Fatal(err)
	}
	values := got[0].(*Insert).Rows[0]
	if values[0].Kind != StringValue || values[0].Text != "$17" || values[1].Parameter != 2 || values[2].Parameter != 2 || values[3].Parameter != 1 {
		t.Fatalf("values = %#v", values)
	}
}
