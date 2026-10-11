package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec(`CREATE TABLE "qc.shape" (ii) AS SELECT 7 AS i,'x' AS s,NULL AS n`, 1))
			run(fixture.Select(`SELECT * FROM "qc.shape"`, []string{"ii", "s", "n"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "x", nil}}))
			run(fixture.Exec("CREATE TABLE qc_constraints(i INT NOT NULL UNIQUE CHECK(i>0),d INT DEFAULT 9,g INT GENERATED ALWAYS AS (i+1) STORED,id INT GENERATED ALWAYS AS IDENTITY)", 0))
			run(fixture.Exec("INSERT INTO qc_constraints(i) VALUES(1)", 1))
			run(fixture.Exec("CREATE TABLE qc_unconstrained AS SELECT * FROM qc_constraints", 1))
			run(fixture.Exec("INSERT INTO qc_unconstrained(i,d,g,id) VALUES(NULL,NULL,-7,99),(1,NULL,-7,99),(1,NULL,-7,99)", 3))
			run(fixture.Exec("INSERT INTO qc_unconstrained(i) VALUES(-1)", 1))
			run(fixture.Select("SELECT d,g,id FROM qc_unconstrained WHERE i=-1", []string{"d", "g", "id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{nil, nil, nil}}))
			fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, true, "SELECT * FROM qc_unconstrained", []string{"i", "d", "g", "id"}, []string{"INT4", "INT4", "INT4", "INT4"}, []bool{true, true, true, true}, 5)
			run(fixture.Exec("CREATE TABLE qc_zero_source()", 0))
			for range 3 {
				run(fixture.Exec("INSERT INTO qc_zero_source DEFAULT VALUES", 1))
			}
			run(fixture.Exec("CREATE TABLE qc_zero AS SELECT FROM qc_zero_source", 3))
			run(fixture.Exec("SELECT INTO qc_zero_into FROM qc_zero_source", 3))
			for _, name := range []string{"qc_zero", "qc_zero_into"} {
				run(fixture.Select("SELECT * FROM "+name, []string{}, []string{}, [][]any{{}, {}, {}}))
				run(fixture.Select("SELECT count(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
		})
	}
}

func TestReferenceQueryTableCreateTypes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE DOMAIN qc_domain AS INTEGER", 0))
			run(fixture.Exec("CREATE TYPE qc_enum AS ENUM('low','high')", 0))
			run(fixture.Exec("CREATE TABLE qc_array_input(a integer[])", 0))
			run(fixture.Exec("INSERT INTO qc_array_input VALUES('{1,2}')", 1))
			run(fixture.Exec(`CREATE TABLE qc_types AS SELECT 'abcd'::varchar(4) AS v,12.34::numeric(5,2) AS n,'12:34:56.123'::time(2) AS tm,'x' COLLATE "C" AS s,7::qc_domain AS d,'high'::qc_enum AS e,a FROM qc_array_input`, 1))
			// Genuine PG catalog companion: native pg_attribute is outside this owner.
			if b.PG {
				run(fixture.Select("SELECT attname::text AS n,atttypmod AS m FROM pg_attribute WHERE attrelid='qc_types'::regclass AND attnum>0 ORDER BY attnum", []string{"n", "m"}, []string{"TEXT", "INT4"}, [][]any{{"v", int64(8)}, {"n", int64(327686)}, {"tm", int64(2)}, {"s", int64(-1)}, {"d", int64(-1)}, {"e", int64(-1)}, {"a", int64(-1)}}))
				run(fixture.Select("SELECT c.collname::text AS c FROM pg_attribute a JOIN pg_collation c ON c.oid=a.attcollation WHERE a.attrelid='qc_types'::regclass AND a.attname='s'", []string{"c"}, []string{"TEXT"}, [][]any{{"C"}}))
			}
			run(fixture.Select("SELECT v,n,tm::text AS tm,s,d::integer AS d,e::text AS e,a::text AS a FROM qc_types", []string{"v", "n", "tm", "s", "d", "e", "a"}, []string{"VARCHAR", "NUMERIC", "TEXT", "TEXT", "INT4", "TEXT", "TEXT"}, [][]any{{"abcd", "12.34", "12:34:56.12", "x", int64(7), "high", "{1,2}"}}))
			metadata := fixture.Select("SELECT v,n FROM qc_types", []string{"v", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"abcd", "12.34"}})
			metadata.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 2 {
					t.Fatal(columns, err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, columns, metadata.Columns, metadata.Types, []bool{true, true})
				length, known := columns[0].Length()
				if length != 4 || !known {
					t.Fatalf("varchar typmod Length=%d/%t want4/true", length, known)
				}
				precision, scale, known := columns[1].DecimalSize()
				if precision != 5 || scale != 2 || !known {
					t.Fatalf("numeric typmod DecimalSize=%d/%d/%t want5/2/true", precision, scale, known)
				}
			}
			run(fixture.Prepared(metadata))
			run(fixture.Exec("INSERT INTO qc_types(d) VALUES(0),(NULL)", 2))
			run(fixture.Select("SELECT d::integer AS d FROM qc_types ORDER BY d NULLS LAST", []string{"d"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(7)}, {nil}}))
			if b.PG {
				// Domain constraint DDL belongs to PostgreSQL's existing domain owner,
				// not the native query-table implementation.
				run(fixture.Exec("CREATE DOMAIN qc_checked AS INTEGER CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE qc_checked_copy AS SELECT 7::qc_checked AS d", 1))
				run(fixture.ImplicitError("INSERT INTO qc_checked_copy(d) VALUES(0)", fixture.Diagnostic{Code: "23514", Message: `value for domain qc_checked violates check constraint "qc_checked_check"`, Schema: "public", DataType: "qc_checked", Constraint: "qc_checked_check"}))
			}
		})
	}
}

func TestReferenceQueryTableCreateQueryConsumers(t *testing.T) {
	cases := []struct {
		name, body   string
		names, types []string
		rows         [][]any
	}{
		{"star", "SELECT * FROM qc_source ORDER BY i", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}}},
		{"hidden", "SELECT s FROM qc_source ORDER BY i", []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {nil}}},
		{"values", "VALUES(1,'x'),(2,NULL)", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), nil}}},
		{"derived", "SELECT d.i FROM (VALUES(1),(2)) AS d(i)", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}},
		{"distinct", "SELECT DISTINCT i FROM qc_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}},
		{"group", "SELECT i,count(*) AS n FROM qc_source GROUP BY i HAVING count(*)>0", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}},
		{"bounds", "SELECT i FROM qc_source ORDER BY i OFFSET 1 LIMIT 1", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}},
		{"inner", "SELECT a.i,b.s AS matched FROM qc_source a JOIN qc_right b USING(i)", []string{"i", "matched"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "m"}, {int64(1), "m"}}},
		{"left", "SELECT a.i,b.s AS matched FROM qc_source a LEFT JOIN qc_right b ON a.i=b.i", []string{"i", "matched"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "m"}, {int64(1), "m"}, {int64(2), nil}}},
		{"right", "SELECT a.i,b.s AS matched FROM qc_right b RIGHT JOIN qc_source a ON a.i=b.i", []string{"i", "matched"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "m"}, {int64(1), "m"}, {int64(2), nil}}},
		{"full", "SELECT a.i,b.s AS matched FROM qc_source a FULL JOIN qc_right b ON a.i=b.i", []string{"i", "matched"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "m"}, {int64(1), "m"}, {int64(2), nil}}},
		{"scalar", "SELECT i,(SELECT max(b.i) FROM qc_source b WHERE b.i=a.i) AS m FROM qc_source a", []string{"i", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(2), int64(2)}}},
		{"exists", "SELECT i FROM qc_source a WHERE EXISTS(SELECT 1/0 FROM qc_right b WHERE b.i=a.i)", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}},
		{"in", "SELECT i FROM qc_source WHERE i IN(SELECT i FROM qc_right)", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}},
		{"any", "SELECT i FROM qc_source WHERE i=ANY(SELECT i FROM qc_right)", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}},
		{"all", "SELECT i FROM qc_source WHERE i>=ALL(SELECT i FROM qc_right)", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}},
		{"row", "SELECT i FROM qc_source WHERE (i,s) IN(SELECT i,'a' FROM qc_right)", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}},
		{"view", "SELECT i,s FROM qc_view", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}}},
		{"catalog", "SELECT relname AS n FROM pg_class WHERE relname='qc_source'", []string{"n"}, []string{"NAME"}, [][]any{{"qc_source"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					_, run := fixture.QueryTableCreateSession(t, p, b)
					fixture.QueryTableCreateSource(run)
					run(fixture.Exec("CREATE TABLE qc_right(i INT,s TEXT)", 0))
					run(fixture.Exec("INSERT INTO qc_right VALUES(1,'m')", 1))
					run(fixture.Exec("CREATE VIEW qc_view0 AS SELECT * FROM qc_source", 0))
					run(fixture.Exec("CREATE VIEW qc_view AS SELECT * FROM qc_view0", 0))
					run(fixture.Prepared(fixture.Exec("CREATE TABLE qc_consumer AS "+tc.body, int64(len(tc.rows)))))
					s := fixture.Select("SELECT * FROM qc_consumer", tc.names, tc.types, tc.rows)
					s.Bag = true
					run(s)
				})
			}
		})
	}
}
