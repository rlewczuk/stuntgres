package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

const pgMatviewProbe = "SELECT * FROM pg_catalog.pg_matviews WHERE schemaname=$1 AND matviewname=$2"

func TestReferencePGMatviewsIndexHistory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE TABLE pgmv_base(n INT)", 0))
			run(fixture.Exec("INSERT INTO pgmv_base VALUES(7)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW pgmv_index AS SELECT n FROM pgmv_base WITH NO DATA", 0))
			stmt, err := conn.PrepareContext(p.Ctx, pgMatviewProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: pgMatviewProbe}
			check := func(indexed, populated, present bool) {
				var rows [][]any
				if present {
					rows = [][]any{fixture.PGMatviewRow("public", "pgmv_index", fixture.PGTableViewsOwner(b), indexed, populated, " SELECT n\n   FROM pgmv_base;")}
				}
				s := fixture.PGTableViewSelect(b, pgMatviewProbe, fixture.PGMatviewColumns, fixture.PGMatviewTypes, rows, "public", "pgmv_index")
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			check(false, false, true)
			run(fixture.Exec("CREATE INDEX pgmv_source_i ON pgmv_base(n)", 0))
			check(false, false, true)
			run(fixture.Exec("CREATE INDEX pgmv_index_i ON pgmv_index(n)", 0))
			check(true, false, true)
			run(fixture.Exec("REFRESH MATERIALIZED VIEW pgmv_index WITH DATA", 0))
			check(true, true, true)
			s := fixture.Select("SELECT n FROM pgmv_index", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}})
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("DROP INDEX pgmv_index_i", 0))
			check(true, true, true)
			run(fixture.Exec("VACUUM pgmv_index", 0))
			check(false, true, true)
			run(fixture.Exec("REFRESH MATERIALIZED VIEW pgmv_index WITH NO DATA", 0))
			check(false, false, true)
			run(fixture.MaterializedViewUnpopulated("pgmv_index"))
			run(fixture.Prepared(fixture.MaterializedViewUnpopulated("pgmv_index")))
			run(fixture.Exec("DROP MATERIALIZED VIEW pgmv_index", 0))
			check(false, false, false)
		})
	}
}
