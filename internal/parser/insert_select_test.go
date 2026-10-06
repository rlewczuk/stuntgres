package parser

import "testing"

func TestInsertSelectSources(t *testing.T) {
	for _, tc := range []struct {
		sql           string
		query, values bool
		targets       int
	}{
		{"INSERT INTO t SELECT i FROM s WHERE i=1 ORDER BY i", true, false, 0},
		{"INSERT INTO t(i) ((SELECT $1))", true, false, 1},
		{"INSERT INTO t (SELECT)", true, false, 0},
		{"INSERT INTO t SELECT TRUE AND FALSE", true, false, 0},
		{"INSERT INTO t VALUES ('1')", false, false, 0},
		{"INSERT INTO t ((VALUES ('1')))", false, false, 0},
		{"INSERT INTO t(i) (VALUES (1),(2) ORDER BY column1 DESC)", true, true, 1},
		{"INSERT INTO t VALUES (1),(2) ORDER BY 1", true, true, 0},
	} {
		stmts, err := Parse(tc.sql)
		if err != nil || len(stmts) != 1 {
			t.Fatal(tc.sql, stmts, err)
		}
		s := stmts[0].(*Insert)
		if (s.Source != nil) != tc.query || len(s.Targets) != tc.targets {
			t.Fatal(tc.sql, s)
		}
		if tc.query {
			if len(s.Rows) != 0 || (s.Source.Select == nil) != tc.values {
				t.Fatal(tc.sql, s)
			}
		} else if len(s.Rows) != 1 {
			t.Fatal(tc.sql, s)
		}
	}
}
func TestInsertSelectBoundaries(t *testing.T) {
	for _, q := range []string{
		"INSERT INTO t SELECT 1 RETURNING i", "INSERT INTO t VALUES(1) ORDER BY 1 LIMIT 1",
		"INSERT INTO t WITH x AS (SELECT 1) SELECT 1", "INSERT INTO t DEFAULT VALUES",
		"INSERT INTO t VALUES()", "VALUES(1)", "SELECT 1",
		"INSERT INTO t VALUES(1) ORDER BY 1,2", "INSERT INTO t VALUES(1) ORDER BY 1 NULLS FIRST",
		"INSERT INTO t SELECT 1 FROM s AS x", "INSERT INTO t VALUES(1); INSERT INTO t (SELECT 1) VALUES(2)",
	} {
		stmts, err := Parse(q)
		if err == nil || stmts != nil {
			t.Fatal("boundary escaped", q, stmts, err)
		}
	}
}

func TestInsertSelectOrderSyntax(t *testing.T) {
	for _, tc := range []struct {
		key, number, sign string
		kind              ValueKind
		real              bool
		ordinal           int64
		isOrdinal         bool
	}{
		{key: "1", number: "1", kind: NumericValue, ordinal: 1, isOrdinal: true},
		{key: "2147483647", number: "2147483647", kind: NumericValue, ordinal: 2147483647, isOrdinal: true},
		{key: "2147483648", number: "2147483648", kind: NumericValue},
		{key: "-2147483647", number: "2147483647", sign: "-", kind: NumericValue, ordinal: -2147483647, isOrdinal: true},
		{key: "-2147483648", number: "2147483648", sign: "-", kind: NumericValue},
		{key: "+2", number: "2", sign: "+", kind: NumericValue},
		{key: "+1.5 DESC", number: "1.5", sign: "+", kind: NumericValue, real: true},
		{key: "+1e0 ASC", number: "1e0", sign: "+", kind: NumericValue, real: true},
		{key: "+1e131072", number: "1e131072", sign: "+", kind: NumericValue, real: true},
		{key: "1.5", number: "1.5", kind: NumericValue, real: true},
		{key: "1e0", number: "1e0", kind: NumericValue, real: true},
		{key: "'x'", number: "x", kind: StringValue},
		{key: "TRUE", kind: BooleanValue},
		{key: "NULL", kind: NullValue},
	} {
		t.Run(tc.key, func(t *testing.T) {
			// Even invalid ORDER semantics must not terminate raw parsing.
			q := "INSERT INTO t VALUES(1) ORDER BY " + tc.key + "; INSERT INTO t VALUES(9)"
			stmts, err := Parse(q)
			if err != nil || len(stmts) != 2 {
				t.Fatal(stmts, err)
			}
			order := stmts[0].(*Insert).Source.Order
			c := order.Constant
			if c == nil || order.Column != "" || c.Value.Kind != tc.kind || c.Value.Text != tc.number || c.Sign != tc.sign || c.Real != tc.real {
				t.Fatalf("ORDER syntax %#v, constant %#v", order, c)
			}
			if ordinal, ok := c.Ordinal(); ordinal != tc.ordinal || ok != tc.isOrdinal {
				t.Fatalf("ordinal %d/%v, want %d/%v", ordinal, ok, tc.ordinal, tc.isOrdinal)
			}
			if order.Pos.Offset != len("INSERT INTO t VALUES(1) ORDER BY ") || order.Desc != (tc.key == "+1.5 DESC") {
				t.Fatal("key position/direction", order)
			}
			if tc.sign != "" && c.Value.Pos.Offset != order.Pos.Offset+1 {
				t.Fatal("unsigned operand lost its own position", c)
			}
		})
	}
	keywordDiagnostic(t, "INSERT INTO t VALUES(1) ORDER BY 'x'; CREATE TABLE select(x INT)", "42601", `syntax error at or near "select"`, "52")
}
