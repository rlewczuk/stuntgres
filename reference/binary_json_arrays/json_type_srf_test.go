package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeSRF(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		s := fixture.Select("SELECT 7 AS i,"+family+"_object_keys('{\"b\":1,\"a\":2}') AS k", []string{"i", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "a"}, {int64(7), "b"}})
		s.Bag = true
		jsonTypeBoth(t, p, b, c, s)
		for _, input := range []string{"'{}'", "NULL"} {
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT 7 AS i,"+family+"_object_keys("+input+") AS k", []string{"i", "k"}, []string{"INT4", "TEXT"}, [][]any{}))
		}
		s = fixture.Select("SELECT upper("+family+"_object_keys('{\"b\":1,\"a\":2}')) AS k", []string{"k"}, []string{"TEXT"}, [][]any{{"A"}, {"B"}})
		s.Bag = true
		jsonTypeBoth(t, p, b, c, s)
	}
	s := fixture.Select(`SELECT json_object_keys('{"b":1,"a":2}') AS j,jsonb_object_keys('{"x":1}') AS b`, []string{"j", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"b", "x"}, {"a", nil}})
	s.Bag = true
	jsonTypeBoth(t, p, b, c, s)
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT * FROM ROWS FROM(json_object_keys('{"b":1,"a":2}'),string_to_table('x',','),regexp_matches('q1 q2','q([0-9])','g')) WITH ORDINALITY AS r(k,s,m,n) ORDER BY n`, []string{"k", "s", "m", "n"}, []string{"TEXT", "TEXT", "_TEXT", "INT8"}, [][]any{{"b", "x", "{1}", int64(1)}, {"a", nil, "{2}", int64(2)}}))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jt_lateral(id int,j json,b jsonb)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jt_lateral VALUES(1,'{"b":1,"a":2}','{"zz":1,"a":2}'),(2,'{}','{}'),(3,NULL,NULL),(4,'{"x":1}','{"x":1}')`, 4))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,k,n FROM jt_lateral,json_object_keys(j) WITH ORDINALITY AS x(k,n) ORDER BY id,n", []string{"id", "k", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "b", int64(1)}, {int64(1), "a", int64(2)}, {int64(4), "x", int64(1)}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,k,n FROM jt_lateral,jsonb_object_keys(b) WITH ORDINALITY AS x(k,n) ORDER BY id,n", []string{"id", "k", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {int64(1), "zz", int64(2)}, {int64(4), "x", int64(1)}}))
	// Nested target sets run the inner stage, not a scalar first-item shortcut.
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT json_object_keys(json_object_keys('{"{}":1}')::json) AS k`, []string{"k"}, []string{"TEXT"}, [][]any{}))
	for _, family := range []string{"json", "jsonb"} {
		name := family + "_object_keys"
		jsonTypeAnalysisError(t, p, b, c, jsonTypeError("SELECT 1 WHERE "+name+"('{}') IS NULL", "0A000", "set-returning functions are not allowed in WHERE", 16))
		q := "SELECT * FROM " + name + "(" + name + "('{\"a\":1}')::" + family + ")"
		inner := strings.LastIndex(q, name)
		jsonTypeAnalysisError(t, p, b, c, jsonTypeError(q, "0A000", "set-returning functions must appear at top level of FROM", len([]rune(q[:inner]))+1))
		for _, tc := range []struct{ query, construct string }{
			{"SELECT CASE WHEN false THEN " + name + "('{}') ELSE 'x' END", "CASE"},
			{"SELECT COALESCE('x'," + name + "('{}'))", "COALESCE"},
		} {
			pos := strings.Index(tc.query, name)
			s := jsonTypeError(tc.query, "0A000", "set-returning functions are not allowed in "+tc.construct, len([]rune(tc.query[:pos]))+1)
			s.Diagnostic.Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
			jsonTypeAnalysisError(t, p, b, c, s)
		}
	}
}

func TestReferenceJSONTypeSRF(t *testing.T) { jsonTypeScenario(t, jsonTypeSRF) }
