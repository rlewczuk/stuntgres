package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedViewMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_meta AS SELECT i,s FROM mv_base", 3))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_meta_no AS SELECT i,s FROM mv_base WITH NO DATA", 0))
			var owner string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			definition := " SELECT i,\n    s\n   FROM mv_base;"
			s := fixture.Select("SELECT * FROM pg_matviews WHERE matviewname='mv_meta'", []string{"schemaname", "matviewname", "matviewowner", "tablespace", "hasindexes", "ispopulated", "definition"}, []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "TEXT"}, [][]any{{"public", "mv_meta", owner, nil, false, true, definition}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, cols, s.Columns, s.Types, []bool{true, true, true, true, true, true, true})
			}
			run(s)
			run(fixture.Prepared(s))
			fixture.MaterializedViewPopulation(run, "mv_meta", true)
			fixture.MaterializedViewPopulation(run, "mv_meta_no", false)
			run(fixture.Select("SELECT matviewname,ispopulated FROM pg_matviews WHERE matviewname='mv_meta_no'", []string{"matviewname", "ispopulated"}, []string{"NAME", "BOOL"}, [][]any{{"mv_meta_no", false}}))
			run(fixture.Select("SELECT tablename FROM pg_tables WHERE tablename='mv_meta'", []string{"tablename"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT viewname FROM pg_views WHERE viewname='mv_meta'", []string{"viewname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT pg_get_viewdef('mv_meta'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{definition}}))
			run(fixture.Select("SELECT c.relnatts,m.schemaname AS nspname,m.matviewowner AS rolname,w.rulename,w.ev_class=c.oid AS linked,w.oid<>c.oid AS distinct_id,c.relnamespace=b.relnamespace AS namespace_linked,c.relowner=b.relowner AS owner_linked FROM ((pg_class c JOIN pg_class b ON b.relname='mv_base') JOIN pg_matviews m ON m.matviewname=c.relname) JOIN pg_rewrite w ON w.ev_class=c.oid WHERE c.relname='mv_meta' AND m.schemaname='public'", []string{"relnatts", "nspname", "rolname", "rulename", "linked", "distinct_id", "namespace_linked", "owner_linked"}, []string{"INT2", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(2), "public", owner, "_RETURN", true, true, true, true}}))
			run(fixture.Exec("ALTER TABLE mv_base RENAME TO mv_renamed", 0))
			run(fixture.Exec("ALTER TABLE mv_renamed RENAME COLUMN i TO key", 0))
			run(materializedViewBag("mv_meta"))
			run(fixture.Select("SELECT pg_get_viewdef('mv_meta'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT key AS i,\n    s\n   FROM mv_renamed;"}}))
			run(fixture.Exec("CREATE TABLE mv_notnull(i INT NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO mv_notnull VALUES(1)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_nullable AS SELECT i FROM mv_notnull", 1))
			fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, true, "SELECT * FROM mv_nullable", []string{"i"}, []string{"INT4"}, []bool{true}, 1)
			run(fixture.Exec(`CREATE MATERIALIZED VIEW mv_typed AS SELECT 'abcd'::varchar(4) AS v,12.34::numeric(5,2) AS n,'x' COLLATE "C" AS s`, 1))
			typed := fixture.Select("SELECT * FROM mv_typed", []string{"v", "n", "s"}, []string{"VARCHAR", "NUMERIC", "TEXT"}, [][]any{{"abcd", "12.34", "x"}})
			typed.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, cols, typed.Columns, typed.Types, []bool{true, true, true})
				length, known := cols[0].Length()
				if length != 4 || !known {
					t.Fatalf("varchar length=%d/%t want4/true", length, known)
				}
				precision, scale, known := cols[1].DecimalSize()
				if precision != 5 || scale != 2 || !known {
					t.Fatalf("numeric=%d/%d/%t want5/2/true", precision, scale, known)
				}
			}
			run(fixture.Prepared(typed))
			run(fixture.Select("SELECT t.typrelid=c.oid AS linked FROM pg_type t JOIN pg_class c ON c.reltype=t.oid WHERE c.relname='mv_meta'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
			if b.PG {
				run(fixture.Select("SELECT c.collname::text AS c FROM pg_attribute a JOIN pg_collation c ON c.oid=a.attcollation WHERE a.attrelid='mv_typed'::regclass AND a.attname='s'", []string{"c"}, []string{"TEXT"}, [][]any{{"C"}}))
			}
		})
	}
}
