package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsFingerprintUtilityBound(t *testing.T) {
	e := engine.New()
	bound := func(t *testing.T, source string) (analyze.StatementJumble, int) {
		t.Helper()
		root := enginetest.Statement(t, source)
		var child parser.Statement
		switch s := root.(type) {
		case *parser.CreateQueryTable:
			child = s.Query
		case *parser.DeclareCursor:
			child = s.Query
		case *parser.Explain:
			child = s.Statement
		case *parser.CreateView:
			child = s.Query
		default:
			t.Fatalf("unexpected utility fixture %T", root)
		}
		_, binding := pgssBindStatement(t, e, source, child)
		return analyze.JumbleUtilityBound(root, binding)
	}
	for _, tc := range []struct {
		left, right, want string
		same              bool
	}{
		{"CREATE TABLE pgss_out AS SELECT 11 AS first", "CREATE TABLE pgss_out AS SELECT 22 AS second", "CREATE TABLE pgss_out AS SELECT $1 AS first", true},
		{"DECLARE pgss_cur CURSOR FOR SELECT 11 AS first", "DECLARE pgss_cur CURSOR FOR SELECT 22 AS second", "DECLARE pgss_cur CURSOR FOR SELECT $1 AS first", true},
		{"EXPLAIN SELECT 11 AS first", "EXPLAIN SELECT 22 AS second", "EXPLAIN SELECT $1 AS first", true},
		{"EXPLAIN SELECT 11::integer", "EXPLAIN SELECT 22::bigint", "EXPLAIN SELECT $1::integer", false},
		{"CREATE VIEW pgss_out AS SELECT 11 AS first", "CREATE VIEW pgss_out AS SELECT 22 AS second", "CREATE VIEW pgss_out AS SELECT 11 AS first", false},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, anchor := bound(t, tc.left)
			right, _ := bound(t, tc.right)
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("analyzed utility child relationship: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
			normalized, _ := parser.NormalizeStatementText(tc.left, anchor, left.HighestParameter, left.Locations, left.Lists, left.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if normalized != tc.want {
				t.Fatalf("normalized %q, want %q", normalized, tc.want)
			}
		})
	}
}
