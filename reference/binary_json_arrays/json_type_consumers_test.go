package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jt_stored(id int,j json DEFAULT '[1]',b jsonb DEFAULT '[1,2]',dk text DEFAULT json_typeof('null'),bk text DEFAULT jsonb_typeof('true'),dl int DEFAULT json_array_length('[]'),bl int DEFAULT jsonb_array_length('[null]'),jk text GENERATED ALWAYS AS (json_typeof(j)) STORED,bt text GENERATED ALWAYS AS (jsonb_typeof(b)) STORED,jl int GENERATED ALWAYS AS (json_array_length(j)) STORED,bn int GENERATED ALWAYS AS (jsonb_array_length(b)) STORED,CHECK(json_array_length(j)<=3),CHECK(jsonb_array_length(b)<=3),CHECK(json_typeof(j)='array'),CHECK(jsonb_typeof(b)='array'))`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE INDEX jt_stored_idx ON jt_stored(json_typeof(j),jsonb_array_length(b))", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jt_stored(id) VALUES(1)", 1))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jt_stored(id,j,b) VALUES(2,'[]','[1]')`, 1))
	check := fixture.Select("SELECT id,dk,bk,dl,bl,jk,bt,jl,bn FROM jt_stored ORDER BY id", []string{"id", "dk", "bk", "dl", "bl", "jk", "bt", "jl", "bn"}, []string{"INT4", "TEXT", "TEXT", "INT4", "INT4", "TEXT", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), "null", "boolean", int64(0), int64(1), "array", "array", int64(1), int64(2)}, {int64(2), "null", "boolean", int64(0), int64(1), "array", "array", int64(0), int64(1)}})
	// All nine projections are declared nullable jt_stored columns, not
	// computed function outputs. Assert the source descriptors on fresh readers.
	columns := []fixture.JSONTypeSourceColumn{
		{Index: 0, Nullable: true}, {Index: 1, Nullable: true}, {Index: 2, Nullable: true},
		{Index: 3, Nullable: true}, {Index: 4, Nullable: true}, {Index: 5, Nullable: true},
		{Index: 6, Nullable: true}, {Index: 7, Nullable: true}, {Index: 8, Nullable: true},
	}
	check = fixture.JSONTypeWithMetadata(check, b.PG, columns...)
	jsonTypeBoth(t, p, b, c, check)
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jt_objects(id int,j json,b jsonb)`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jt_objects VALUES(1,'{"b":1,"a":2}','{"zz":1,"a":2}')`, 1))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE VIEW jt_key_view AS SELECT id,json_typeof(j) AS kind,k,n FROM jt_objects,json_object_keys(j) WITH ORDINALITY AS x(k,n)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE VIEW jt_bkey_view AS SELECT id,jsonb_typeof(b) AS kind,k,n FROM jt_objects,jsonb_object_keys(b) WITH ORDINALITY AS x(k,n)", 0))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,kind,k,n FROM jt_key_view ORDER BY id,n", []string{"id", "kind", "k", "n"}, []string{"INT4", "TEXT", "TEXT", "INT8"}, [][]any{{int64(1), "object", "b", int64(1)}, {int64(1), "object", "a", int64(2)}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,kind,k,n FROM jt_bkey_view ORDER BY id,n", []string{"id", "kind", "k", "n"}, []string{"INT4", "TEXT", "TEXT", "INT8"}, [][]any{{int64(1), "object", "a", int64(1)}, {int64(1), "object", "zz", int64(2)}}))
	return check
}

func jsonTypeConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	jsonTypeStored(t, p, b, c)
	jsonTypeRun(t, p, b, c, fixture.Exec(`UPDATE jt_stored SET j='[1,2,3]',b='[]' WHERE id=2`, 1))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT jk,bt,jl,bn FROM jt_stored WHERE id=2", []string{"jk", "bt", "jl", "bn"}, []string{"TEXT", "TEXT", "INT4", "INT4"}, [][]any{{"array", "array", int64(3), int64(0)}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT json_typeof(j) AS k,sum(json_array_length(j)) AS n FROM jt_stored GROUP BY json_typeof(j)", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"array", int64(4)}}))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jt_sink(k text)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jt_sink VALUES('sentinel')", 1))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jt_bad(id int,j json)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jt_bad VALUES(1,'{"ok":1}'),(2,'{"ok":1,"nested":{"x":"\u0000"}}')`, 2))
	q := "INSERT INTO jt_sink SELECT json_object_keys(j) FROM jt_bad ORDER BY id"
	for _, prepared := range []bool{false, true} {
		s := jsonTypeError(q, "22P05", "unsupported Unicode escape sequence", 0)
		s.SelectRows = false
		s.Diagnostic.Detail = `\u0000 cannot be converted to text.`
		s.Diagnostic.Context = `JSON data, line 1: {"ok":1,"nested":{"x":"\u0000...`
		if prepared {
			s = fixture.Prepared(s)
		}
		jsonTypeRun(t, p, b, c, s)
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT k FROM jt_sink ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
	}
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jt_sink SELECT json_object_keys('{"a":1}')`, 1))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT k FROM jt_sink ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {"sentinel"}}))
	for _, family := range []string{"json", "jsonb"} {
		name := family + "_object_keys"
		for _, tc := range []struct{ query, context string }{
			{"CREATE TABLE jt_reject(v text DEFAULT " + name + "('{}'))", "DEFAULT expressions"},
			{"CREATE TABLE jt_reject(v text CHECK(" + name + "('{}') IS NULL))", "check constraints"},
			{"CREATE TABLE jt_reject(j " + family + ",v text GENERATED ALWAYS AS (" + name + "(j)) STORED)", "column generation expressions"},
			{"CREATE INDEX jt_reject ON jt_objects (" + name + "('{}'))", "index expressions"},
		} {
			pos := strings.Index(tc.query, name)
			// Utility-statement bodies are analyzed at execution, even when prepared.
			// Keep both public routes and the complete placement diagnostic.
			jsonTypeBoth(t, p, b, c, jsonTypeError(tc.query, "0A000", "set-returning functions are not allowed in "+tc.context, len([]rune(tc.query[:pos]))+1))
		}
	}
}

func TestReferenceJSONTypeConsumers(t *testing.T) { jsonTypeScenario(t, jsonTypeConsumers) }
