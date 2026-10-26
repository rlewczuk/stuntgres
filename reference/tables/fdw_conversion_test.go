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
		t.Run("alias_compaction", func(t *testing.T) {
			fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.alias_conversion(unused text,v text,tail text)", 0)
			fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.alias_conversion VALUES('99','7','9')", 1)
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_alias_conversion(unused integer,v integer,tail integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'alias_conversion')", 0))
			run(fixture.Exec("CREATE FUNCTION fdw_alias_conversion_run() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT picked FROM fdw_alias_conversion AS f(skipped,picked,last); END", 0))
			var routineID, sourceID int64
			identitySQL := "SELECT 'fdw_alias_conversion_run()'::regprocedure::oid::bigint AS routine_oid,'fdw_alias_conversion'::regclass::oid::bigint AS source_oid"
			if err := c.QueryRowContext(p.Ctx, identitySQL).Scan(&routineID, &sourceID); err != nil {
				t.Fatal(err)
			}
			state := func(compacted bool, value string) {
				t.Helper()
				run(fixture.Select(identitySQL, []string{"routine_oid", "source_oid"}, []string{"INT8", "INT8"}, [][]any{{routineID, sourceID}}))
				run(fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='fdw_alias_conversion_run()'::regprocedure AND refclassid='pg_class'::regclass AND refobjid='fdw_alias_conversion'::regclass ORDER BY refobjsubid,deptype",
					[]string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"},
					[][]any{{int64(1255), routineID, int64(0), int64(1259), sourceID, int64(2), "n"}}))
				attrs := [][]any{{"unused", int64(1)}, {"v", int64(2)}, {"tail", int64(3)}}
				if compacted {
					attrs = [][]any{{"v", int64(2)}, {"tail", int64(3)}}
				}
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='fdw_alias_conversion'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, attrs))
				fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT unused,v,tail FROM fdw_source.alias_conversion", []string{"unused", "v", "tail"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"99", value, "9"}}))
			}
			query := "SELECT fdw_alias_conversion_run() AS v"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("prepare before alias compaction", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			invoke := func(x fixture.Executor) {
				t.Helper()
				before := r.snapshot()
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				if !b.PG {
					fdwReleased(t, r, before, 1)
				}
			}
			state(false, "7")
			invoke(c)
			invoke(held)
			run(fixture.Exec("ALTER FOREIGN TABLE fdw_alias_conversion DROP unused RESTRICT", 0))
			state(true, "7")
			invoke(held)
			invoke(held)
			invoke(c)
			fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.alias_conversion SET v='nope'", 1)
			defer fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.alias_conversion SET v='7'", 1)
			state(true, "nope")
			for _, route := range []struct {
				name string
				exec fixture.Executor
			}{{"held", held}, {"direct", c}} {
				before := r.snapshot()
				rows, err := route.exec.QueryContext(p.Ctx, query)
				if rows != nil {
					for rows.Next() {
						t.Errorf("%s conversion unexpectedly returned a row", route.name)
					}
					err = rows.Err()
					if closeErr := rows.Close(); err == nil {
						err = closeErr
					}
				}
				want := fdwDiagnostic("22P02", `invalid input syntax for type integer: "nope"`)
				want.Context = "column \"picked\" of foreign table \"f\"\nSQL function \"fdw_alias_conversion_run\" statement 1"
				fixture.AssertDiagnostic(t, err, &want)
				if !b.PG {
					fdwReleased(t, r, before, 1)
				}
			}
			fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.alias_conversion SET v='7'", 1)
			state(true, "7")
			invoke(held)
			invoke(c)
			before := r.snapshot()
			run(fixture.Select("SELECT v,tail FROM fdw_alias_conversion", []string{"v", "tail"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
			if !b.PG {
				fdwReleased(t, r, before, 1)
			}
		})
	})
}
