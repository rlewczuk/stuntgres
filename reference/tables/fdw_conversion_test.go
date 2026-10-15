package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWConversion(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_zero() SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Select("SELECT count(*) FROM fdw_zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.convert(payload text)", 0)
		fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.convert VALUES('nope')", 1)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_conversion(payload integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'convert')", 0))
		conversionError := func(query, code, message, column, alias string) {
			t.Helper()
			d := fdwDiagnostic(code, message)
			d.Context = `column "` + column + `" of foreign table "` + alias + `"`
			before := r.snapshot()
			run(fixture.ErrorDiagnostic(query, d))
			if !b.PG {
				fdwReleased(t, r, before, 1)
			}
		}
		conversionError("SELECT p FROM fdw_conversion AS f(p)", "22P02", `invalid input syntax for type integer: "nope"`, "p", "f")
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.convert SET payload='2147483648'", 1)
		conversionError("SELECT payload FROM fdw_conversion f", "22003", `value "2147483648" is out of range for type integer`, "payload", "f")
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_short(payload varchar(3)) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'convert')", 0))
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.convert SET payload='abcd'", 1)
		conversionError("SELECT payload FROM fdw_short f", "22001", "value too long for type character varying(3)", "payload", "f")
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.convert SET payload='abc   '", 1)
		run(fixture.Select("SELECT payload FROM fdw_short", []string{"payload"}, []string{"VARCHAR"}, [][]any{{"abc"}}))
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.convert SET payload=NULL", 1)
		before := r.snapshot()
		run(fixture.Select("SELECT payload FROM fdw_conversion", []string{"payload"}, []string{"INT4"}, [][]any{{nil}}))
		if !b.PG {
			fdwReleased(t, r, before, 1)
		}
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.convert SET payload='2'", 1)
		before = r.snapshot()
		run(fixture.Select("SELECT payload FROM fdw_conversion", []string{"payload"}, []string{"INT4"}, [][]any{{int64(2)}}))
		if !b.PG {
			fdwReleased(t, r, before, 1)
		}
	})
}
