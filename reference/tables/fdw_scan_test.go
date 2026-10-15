package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWScan(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		want := fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}})
		run(want)
		run(fixture.Prepared(want))
		run(fixture.Select("SELECT count(*) FROM fdw_data", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_zero() SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Select("SELECT count(*) FROM fdw_zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Select("SELECT id FROM fdw_data ORDER BY id LIMIT 1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fdwSourceExec(t, p, b, reader, run, "UPDATE fdw_source.data SET label='changed' WHERE id=3", 1)
		run(fixture.Select("SELECT label FROM fdw_data WHERE id=3", []string{"label"}, []string{"TEXT"}, [][]any{{"changed"}}))
		fdwSourceExec(t, p, b, reader, run, "DELETE FROM fdw_source.data", 3)
		run(fdwRows("SELECT id,label,flag FROM fdw_data", nil))
	})
}

func TestReferenceFDWProjectionTypes(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT label,id,label FROM fdw_data ORDER BY id", []string{"label", "id", "label"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"one", int64(1), "one"}, {nil, int64(2), nil}, {"three", int64(3), "three"}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT label FROM fdw_data WHERE id=3", []string{"label"}, []string{"TEXT"}, [][]any{{"three"}}))
		q := "SELECT label FROM fdw_data WHERE id=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{{int64(1), [][]any{{"one"}}}, {int64(2), [][]any{{nil}}}, {nil, nil}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"label"}, []string{"TEXT"}, tc.rows, tc.arg))
		}
	})
}

func TestReferenceFDWLocalRechecks(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		for _, tc := range []struct {
			where string
			rows  [][]any
		}{
			{"flag", [][]any{{int64(1)}}}, {"flag IS NULL", [][]any{{int64(3)}}}, {"id=NULL", nil}, {"id>1 AND (flag=false OR flag IS NULL)", [][]any{{int64(2)}, {int64(3)}}},
		} {
			s := fixture.Select("SELECT id FROM fdw_data WHERE "+tc.where+" ORDER BY id", []string{"id"}, []string{"INT4"}, tc.rows)
			run(s)
			run(fixture.Prepared(s))
		}
	})
}

func TestReferenceFDWRescan(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		if !b.PG {
			restore := fdwBorrowedBytes(reader)
			defer restore()
		}
		run(fixture.Exec("CREATE TABLE fdw_keys(ord integer,k integer)", 0))
		run(fixture.Exec("INSERT INTO fdw_keys VALUES(1,1),(2,2),(3,1)", 3))
		s := fixture.Select("SELECT keys.ord,keys.k AS id,(SELECT f.label FROM fdw_data f WHERE f.id=keys.k OFFSET 0) AS label FROM fdw_keys keys ORDER BY keys.ord", []string{"ord", "id", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "one"}, {int64(2), int64(2), nil}, {int64(3), int64(1), "one"}})
		for _, step := range []fixture.Step{s, fixture.Prepared(s)} {
			before := reader.snapshot()
			run(step)
			if !b.PG {
				after := fdwReleased(t, reader, before, 1)
				if after.rescans-before.rescans != 2 || after.generations-before.generations != 3 || after.iterations-before.iterations != 9 {
					t.Fatal("correlation did not genuinely restart the same reader", before, after)
				}
				reader.mu.Lock()
				parameters := reader.parameters
				reader.mu.Unlock()
				if len(parameters) < 3 {
					t.Fatal("missing real invocation parameters", parameters)
				}
				for i, parameter := range parameters[len(parameters)-3:] {
					key := int64(1)
					if i == 1 {
						key = 2
					}
					if len(parameter.Values) != 0 || len(parameter.Correlation) != 2 || parameter.Correlation[0] != int64(i+1) || parameter.Correlation[1] != key {
						t.Fatal("stale/incorrect evaluated correlation", i, parameter)
					}
				}
			}
		}
		before := reader.snapshot()
		run(fixture.Select("SELECT keys.ord,f.id FROM fdw_keys keys CROSS JOIN fdw_data f WHERE f.id=1 ORDER BY keys.ord", []string{"ord", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}}))
		if !b.PG {
			after := fdwReleased(t, reader, before, 1)
			if after.rescans != before.rescans {
				t.Fatal("noncorrelated cached replay gratuitously restarted its reader", before, after)
			}
		}
	})
}

func TestReferenceFDWPreparedIdentity(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		q := "SELECT label FROM fdw_data WHERE id=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"label"}, []string{"TEXT"}, [][]any{{"one"}}, int64(1)))
		fdwSourceExec(t, p, b, reader, run, "UPDATE fdw_source.data SET label='fresh' WHERE id=1", 1)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"label"}, []string{"TEXT"}, [][]any{{"fresh"}}, int64(1)))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data OPTIONS(SET table_name 'data')", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"label"}, []string{"TEXT"}, [][]any{{nil}}, int64(2)))
	})
}
