package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func sqlPreparedValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	sqlPreparedSetup(t, p, b, c)
	for _, declaration := range []string{"sel(int)", "inferred"} {
		name := "sel"
		if declaration == "inferred" {
			name = declaration
		}
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE "+declaration+" AS SELECT v FROM prep_t WHERE v=$1 ORDER BY v", 0))
		for _, actual := range []string{"7", "8", "NULL", "7"} {
			var rows [][]any
			if actual == "7" {
				rows = [][]any{{int64(7)}}
			}
			if actual == "8" {
				rows = [][]any{{int64(8)}}
			}
			s := fixture.Select("EXECUTE "+name+"("+actual+")", []string{"v"}, []string{"INT4"}, rows)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				fixture.DriverBasicMetadata(t, r, b.PG, []string{"INT4"}, []bool{true})
			}
			sqlPreparedStep(t, p, b, c, s)
			s.Prepared = true
			sqlPreparedStep(t, p, b, c, s)
		}
	}
	for _, q := range []string{"PREPARE ins(int,text,bool) AS INSERT INTO prep_t VALUES($1,$2,$3)", "PREPARE inferred_ins AS INSERT INTO prep_t VALUES($1,$2,$3)"} {
		sqlPreparedStep(t, p, b, c, fixture.Exec(q, 0))
	}
	for _, q := range []string{"EXECUTE ins(9,'nine',TRUE)", "EXECUTE inferred_ins(10,'ten',FALSE)", "EXECUTE ins(11,NULL,NULL)", "EXECUTE inferred_ins(12,'twelve',TRUE)"} {
		sqlPreparedStep(t, p, b, c, fixture.Prepared(fixture.Exec(q, 1)))
	}
	sqlPreparedStep(t, p, b, c, fixture.Select("SELECT v,s,f FROM prep_t ORDER BY v", []string{"v", "s", "f"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "seven", true}, {int64(8), "eight", false}, {int64(9), "nine", true}, {int64(10), "ten", false}, {int64(11), nil, nil}, {int64(12), "twelve", true}}))
	sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE target(int) AS SELECT $1 FROM prep_t WHERE v=$1", 0))
	sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE target(7)", "?column?", "INT4", int64(7)))
}

func TestReferenceSQLPreparedTypedAndInferred(t *testing.T) {
	sqlPreparedScenario(t, sqlPreparedValues)
}

func TestReferenceSQLPreparedDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, definition, query, column, typ string
		value                                any
	}{
		{"appended", "PREPARE p(int) AS SELECT $2::text", "EXECUTE p(99,'x')", "text", "TEXT", "x"},
		{"unused", "PREPARE p(int,text) AS SELECT 1", "EXECUTE p(99,'x')", "?column?", "INT4", int64(1)},
		{"unknown_inferred", "PREPARE p(unknown) AS SELECT $1", "EXECUTE p('text')", "?column?", "TEXT", "text"},
		{"ignored_modifier", "PREPARE p(varchar(2)) AS SELECT $1", "EXECUTE p('long')", "?column?", "VARCHAR", "long"},
		{"catalog_type", "PREPARE p(pg_catalog.uuid) AS SELECT $1", "EXECUTE p('00000000-0000-0000-0000-000000000007')", "?column?", "UUID", "00000000-0000-0000-0000-000000000007"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlPreparedStep(t, p, b, c, fixture.Prepared(fixture.Exec(tc.definition, 0)))
				for _, prepared := range []bool{false, true} {
					s := sqlPreparedScalar(tc.query, tc.column, tc.typ, tc.value)
					s.Prepared = prepared
					sqlPreparedStep(t, p, b, c, s)
				}
				if tc.name == "unused" {
					d := sqlPreparedDiagnostic("42601", `wrong number of parameters for prepared statement "p"`)
					d.Detail = "Expected 2 parameters but got 1."
					sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p(99)", d))
				}
			})
		})
	}
}

func TestReferenceSQLPreparedDestinationModifiers(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_short(v varchar(2))", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE p(varchar(2)) AS INSERT INTO prep_short VALUES($1)", 0))
		sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p('long')", sqlPreparedDiagnostic("22001", "value too long for type character varying(2)")))
		sqlPreparedStep(t, p, b, c, fixture.Select("SELECT v FROM prep_short", []string{"v"}, []string{"VARCHAR"}, nil))
		sqlPreparedStep(t, p, b, c, fixture.Exec("EXECUTE p('ok')", 1))
		sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_assignment(i int,b bigint)", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE same(int) AS INSERT INTO prep_assignment VALUES($1,$1)", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("EXECUTE same(7)", 1))
		sqlPreparedStep(t, p, b, c, fixture.Select("SELECT i,b FROM prep_assignment", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(7)}}))
	})
}
